package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"nano-banana-image-skill/internal/keyer"

	"google.golang.org/genai"
)

const defaultModel = "gemini-3.1-flash-image-preview"

var modelAliases = map[string]string{
	"flash":  "gemini-3.1-flash-image-preview",
	"nb2":    "gemini-3.1-flash-image-preview",
	"pro":    "gemini-3-pro-image-preview",
	"nb-pro": "gemini-3-pro-image-preview",
}

var validSizes = map[string]bool{"512": true, "1K": true, "2K": true, "4K": true}
var validAspects = map[string]bool{
	"1:1": true, "16:9": true, "9:16": true, "4:3": true, "3:4": true,
	"3:2": true, "2:3": true, "4:5": true, "5:4": true, "21:9": true,
	"1:4": true, "1:8": true, "4:1": true, "8:1": true,
}

type outputMode string

const (
	modeHuman outputMode = "human"
	modePlain outputMode = "plain"
	modeJSON  outputMode = "json"
)

type options struct {
	Prompt           string
	Output           string
	Size             string
	OutputDir        string
	References       []string
	Transparent      bool
	APIKey           string
	Model            string
	AspectRatio      string
	Seed             *int32
	PersonGeneration string
	ThinkingLevel    string
	ShowCosts        bool
	OutputMode       outputMode
	JQ               string
}

type costRate struct {
	Input       float64
	ImageOutput float64
}

type costEntry struct {
	Timestamp    string  `json:"timestamp"`
	Model        string  `json:"model"`
	Size         string  `json:"size"`
	Aspect       *string `json:"aspect"`
	PromptTokens int32   `json:"prompt_tokens"`
	OutputTokens int32   `json:"output_tokens"`
	Estimated    float64 `json:"estimated_cost"`
	OutputFile   string  `json:"output_file"`
}

type result struct {
	OK       bool      `json:"ok"`
	Model    string    `json:"model,omitempty"`
	Prompt   string    `json:"prompt,omitempty"`
	Size     string    `json:"size,omitempty"`
	Aspect   string    `json:"aspect,omitempty"`
	Files    []string  `json:"files,omitempty"`
	Cost     float64   `json:"cost,omitempty"`
	Error    string    `json:"error,omitempty"`
	Warnings []string  `json:"warnings,omitempty"`
	At       time.Time `json:"at"`
}

var costRates = map[string]costRate{
	"gemini-3.1-flash-image-preview": {Input: 0.25, ImageOutput: 60},
	"gemini-3-pro-image-preview":     {Input: 2.0, ImageOutput: 120},
}

func main() {
	opts, err := parseArgs(os.Args[1:])
	if err != nil {
		exitError(err, modeHuman)
	}

	if opts.ShowCosts {
		if err := printCosts(opts.OutputMode); err != nil {
			exitError(err, opts.OutputMode)
		}
		return
	}

	if opts.JQ != "" && opts.OutputMode != modeJSON {
		exitError(errors.New("--jq 只能和 --json 一起使用"), opts.OutputMode)
	}

	apiKey, err := resolveAPIKey(opts.APIKey)
	if err != nil {
		exitError(err, opts.OutputMode)
	}

	res, err := generate(context.Background(), opts, apiKey)
	if err != nil {
		exitError(err, opts.OutputMode)
	}

	if opts.Transparent {
		processed := make([]string, 0, len(res.Files))
		for _, file := range res.Files {
			out, rerr := removeBackground(file, opts.OutputMode)
			if rerr != nil {
				logLine(opts.OutputMode, "warn", "transparent failed: %s (%v)", file, rerr)
				processed = append(processed, file)
				continue
			}
			processed = append(processed, out)
		}
		res.Files = processed
	}

	switch opts.OutputMode {
	case modeJSON:
		_ = json.NewEncoder(os.Stdout).Encode(res)
	case modePlain:
		for _, f := range res.Files {
			fmt.Fprintln(os.Stdout, f)
		}
	default:
		logLine(opts.OutputMode, "info", "generated %d image(s)", len(res.Files))
		for _, f := range res.Files {
			fmt.Fprintf(os.Stderr, "  + %s\n", f)
		}
	}
}

func parseArgs(args []string) (options, error) {
	opts := options{Output: fmt.Sprintf("nano-gen-%d", time.Now().UnixMilli()), Size: "1K", OutputDir: mustWd(), Model: defaultModel, OutputMode: modeHuman}
	if len(args) == 0 {
		if p, err := readStdinPipe(); err == nil && p != "" {
			opts.Prompt = p
			return opts, nil
		}
		printHelp()
		os.Exit(0)
	}

	promptParts := []string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "-h", "--help":
			printHelp()
			os.Exit(0)
		case "--costs":
			opts.ShowCosts = true
		case "--json":
			opts.OutputMode = modeJSON
		case "--plain":
			opts.OutputMode = modePlain
		case "--jq":
			i++
			if i >= len(args) {
				return opts, errors.New("--jq 缺少表达式")
			}
			opts.JQ = args[i]
		case "-o", "--output":
			i++
			if i >= len(args) {
				return opts, errors.New("--output 缺少值")
			}
			opts.Output = args[i]
		case "-s", "--size":
			i++
			if i >= len(args) {
				return opts, errors.New("--size 缺少值")
			}
			if !validSizes[args[i]] {
				return opts, fmt.Errorf("invalid size %q", args[i])
			}
			opts.Size = args[i]
		case "-a", "--aspect":
			i++
			if i >= len(args) {
				return opts, errors.New("--aspect 缺少值")
			}
			if !validAspects[args[i]] {
				return opts, fmt.Errorf("invalid aspect %q", args[i])
			}
			opts.AspectRatio = args[i]
		case "-m", "--model":
			i++
			if i >= len(args) {
				return opts, errors.New("--model 缺少值")
			}
			opts.Model = resolveModel(args[i])
		case "-d", "--dir":
			i++
			if i >= len(args) {
				return opts, errors.New("--dir 缺少值")
			}
			opts.OutputDir = args[i]
		case "-r", "--ref":
			i++
			if i >= len(args) {
				return opts, errors.New("--ref 缺少值")
			}
			opts.References = append(opts.References, args[i])
		case "-t", "--transparent":
			opts.Transparent = true
		case "--seed":
			i++
			if i >= len(args) {
				return opts, errors.New("--seed 缺少值")
			}
			n, err := strconv.ParseInt(args[i], 10, 32)
			if err != nil {
				return opts, fmt.Errorf("invalid seed %q: %w", args[i], err)
			}
			v := int32(n)
			opts.Seed = &v
		case "--person":
			i++
			if i >= len(args) {
				return opts, errors.New("--person 缺少值")
			}
			switch strings.ToUpper(args[i]) {
			case "ALL", "ALLOW_ALL":
				opts.PersonGeneration = "ALLOW_ALL"
			case "ADULT", "ALLOW_ADULT":
				opts.PersonGeneration = "ALLOW_ADULT"
			case "NONE", "ALLOW_NONE":
				opts.PersonGeneration = "ALLOW_NONE"
			default:
				return opts, fmt.Errorf("invalid --person value %q (use ALL, ADULT, NONE)", args[i])
			}
		case "--thinking":
			i++
			if i >= len(args) {
				return opts, errors.New("--thinking 缺少值")
			}
			switch strings.ToLower(args[i]) {
			case "minimal", "low", "medium", "high":
				opts.ThinkingLevel = strings.ToUpper(args[i])
			default:
				return opts, fmt.Errorf("invalid --thinking value %q (use minimal, low, medium, high)", args[i])
			}
		case "--api-key":
			i++
			if i >= len(args) {
				return opts, errors.New("--api-key 缺少值")
			}
			opts.APIKey = args[i]
		default:
			if strings.HasPrefix(a, "-") {
				return opts, fmt.Errorf("unknown option: %s", a)
			}
			promptParts = append(promptParts, a)
		}
	}
	if opts.Size == "512" && opts.Model == "gemini-3-pro-image-preview" {
		opts.Size = "1K"
	}
	if !opts.ShowCosts {
		opts.Prompt = strings.TrimSpace(strings.Join(promptParts, " "))
		if opts.Prompt == "" {
			if stdinPrompt, err := readStdinPipe(); err == nil && stdinPrompt != "" {
				opts.Prompt = stdinPrompt
			} else {
				return opts, errors.New("no prompt provided")
			}
		}
	}
	return opts, nil
}

func generate(ctx context.Context, opts options, apiKey string) (result, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{APIKey: apiKey, Backend: genai.BackendGeminiAPI})
	if err != nil {
		return result{}, err
	}
	parts := []*genai.Part{}
	for _, r := range opts.References {
		data, mimeType, err := loadReference(r)
		if err != nil {
			return result{}, err
		}
		parts = append(parts, genai.NewPartFromBytes(data, mimeType))
	}
	prompt := opts.Prompt
	if opts.Transparent {
		prompt += ". Place the subject on a solid bright green background (#00FF00). The background must be a single flat green color with no gradients, shadows, or variation."
	}
	parts = append(parts, genai.NewPartFromText(prompt))
	contents := []*genai.Content{genai.NewContentFromParts(parts, genai.RoleUser)}
	imgCfg := &genai.ImageConfig{ImageSize: imageSize(opts.Size)}
	if opts.AspectRatio != "" {
		imgCfg.AspectRatio = opts.AspectRatio
	}
	if opts.PersonGeneration != "" {
		imgCfg.PersonGeneration = opts.PersonGeneration
	}
	cfg := &genai.GenerateContentConfig{ResponseModalities: []string{"IMAGE", "TEXT"}, ImageConfig: imgCfg, Tools: []*genai.Tool{{GoogleSearch: &genai.GoogleSearch{}}}}
	if opts.Seed != nil {
		cfg.Seed = opts.Seed
	}
	if opts.ThinkingLevel != "" {
		cfg.ThinkingConfig = &genai.ThinkingConfig{ThinkingLevel: genai.ThinkingLevel(opts.ThinkingLevel)}
	}
	logLine(opts.OutputMode, "info", "generating image...")
	resp, err := client.Models.GenerateContent(ctx, opts.Model, contents, cfg)
	if err != nil {
		return result{}, err
	}
	if err := os.MkdirAll(opts.OutputDir, 0o755); err != nil {
		return result{}, err
	}
	files := []string{}
	idx := 0
	for _, c := range resp.Candidates {
		if c == nil || c.Content == nil {
			continue
		}
		for _, p := range c.Content.Parts {
			if p.InlineData != nil && len(p.InlineData.Data) > 0 {
				ext := extFromMime(p.InlineData.MIMEType)
				name := opts.Output
				if idx > 0 {
					name = fmt.Sprintf("%s_%d", opts.Output, idx)
				}
				out := filepath.Join(opts.OutputDir, name+ext)
				if err := os.WriteFile(out, p.InlineData.Data, 0o644); err != nil {
					return result{}, err
				}
				files = append(files, out)
				idx++
			} else if p.Text != "" {
				logLine(opts.OutputMode, "info", "%s", p.Text)
			}
		}
	}
	if len(files) == 0 {
		return result{}, errors.New("no images generated")
	}
	var cost float64
	if resp.UsageMetadata != nil {
		cost = calculateCost(opts.Model, resp.UsageMetadata.PromptTokenCount, resp.UsageMetadata.CandidatesTokenCount)
		_ = logCost(costEntry{Timestamp: time.Now().UTC().Format(time.RFC3339), Model: opts.Model, Size: opts.Size, Aspect: nullable(opts.AspectRatio), PromptTokens: resp.UsageMetadata.PromptTokenCount, OutputTokens: resp.UsageMetadata.CandidatesTokenCount, Estimated: cost, OutputFile: files[0]})
	}
	return result{OK: true, Model: opts.Model, Prompt: opts.Prompt, Size: opts.Size, Aspect: opts.AspectRatio, Files: files, Cost: cost, At: time.Now().UTC()}, nil
}

func printHelp() {
	fmt.Print(`nano-banana – generate images via Gemini

Usage:
  nano-banana [options] <prompt...>

Options:
  -h, --help            Show this help message
  -o, --output NAME     Output file base name (default: nano-gen-<timestamp>)
  -s, --size SIZE       Image size: 512, 1K, 2K, 4K (default: 1K)
  -a, --aspect RATIO    Aspect ratio (e.g. 16:9, 4:3, 1:1)
  -m, --model MODEL     Model name or alias: flash, pro (default: flash)
  -d, --dir DIR         Output directory (default: current directory)
  -r, --ref FILE        Reference image (can be repeated)
  -t, --transparent     Remove background (pure Go, no external tools)
      --seed N          Random seed for reproducible generation
      --person MODE     Person generation: ALL, ADULT, NONE (default: ALL)
      --thinking LEVEL  Thinking level: minimal, low, medium, high
      --api-key KEY     Gemini API key (or set GEMINI_API_KEY)
      --costs           Show accumulated cost summary
      --json            JSON output mode
      --plain           Plain output mode (filenames only)
      --jq EXPR         Filter JSON output with jq expression

Stdin:
  Prompt can be piped via stdin when no positional prompt is given.
  echo "a cat in a spacesuit" | nano-banana -s 2K

Examples:
  nano-banana "a cat in a spacesuit"
  nano-banana -s 2K -a 16:9 "sunset over mountains"
  nano-banana -r style.png "apply this style to a forest"
  nano-banana --json "logo for a coffee shop" | jq .files
  nano-banana --costs
`)
}
func resolveAPIKey(flag string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	if v := strings.TrimSpace(os.Getenv("GEMINI_API_KEY")); v != "" {
		return v, nil
	}
	paths := []string{}
	if wd, err := os.Getwd(); err == nil {
		paths = append(paths, filepath.Join(wd, ".env"))
	}
	if exe, err := os.Executable(); err == nil {
		paths = append(paths, filepath.Join(filepath.Dir(exe), "..", ".env"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".nano-banana", ".env"))
	}
	for _, p := range paths {
		if v := readDotEnvValue(p, "GEMINI_API_KEY"); v != "" {
			return v, nil
		}
	}
	return "", errors.New("GEMINI_API_KEY is required")
}
func readDotEnvValue(path, key string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[0]) == key {
			return strings.Trim(strings.TrimSpace(parts[1]), "\"'")
		}
	}
	return ""
}
func resolveModel(input string) string {
	if v, ok := modelAliases[strings.ToLower(input)]; ok {
		return v
	}
	return input
}
func imageSize(size string) string {
	if size == "512" {
		return "1K"
	}
	return size
}
func loadReference(path string) ([]byte, string, error) {
	p := path
	if !filepath.IsAbs(p) {
		wd, _ := os.Getwd()
		p = filepath.Join(wd, p)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, "", fmt.Errorf("reference not found: %s", path)
	}
	ext := strings.ToLower(filepath.Ext(p))
	m := mime.TypeByExtension(ext)
	if m == "" {
		if ext == ".jpg" || ext == ".jpeg" {
			m = "image/jpeg"
		} else if ext == ".webp" {
			m = "image/webp"
		} else if ext == ".gif" {
			m = "image/gif"
		} else {
			m = "image/png"
		}
	}
	return b, m, nil
}
func removeBackground(input string, mode outputMode) (string, error) {
	f, err := os.Open(input)
	if err != nil {
		return "", err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return "", fmt.Errorf("decode %s: %w", input, err)
	}
	result := keyer.RemoveBackground(img)
	dir := filepath.Dir(input)
	base := strings.TrimSuffix(filepath.Base(input), filepath.Ext(input))
	out := filepath.Join(dir, base+".png")
	w, err := os.Create(out)
	if err != nil {
		return "", err
	}
	defer w.Close()
	if err := png.Encode(w, result); err != nil {
		return "", err
	}
	_ = mode
	return out, nil
}
func calculateCost(model string, promptTokens, outputTokens int32) float64 {
	rate := costRates[model]
	if rate.Input == 0 {
		rate = costRates[defaultModel]
	}
	return (float64(promptTokens)/1_000_000)*rate.Input + (float64(outputTokens)/1_000_000)*rate.ImageOutput
}
func costLogPath() string {
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".nano-banana", "costs.json")
}
func logCost(entry costEntry) error {
	path := costLogPath()
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	entries := []costEntry{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &entries)
	}
	entries = append(entries, entry)
	b, _ := json.MarshalIndent(entries, "", "  ")
	return os.WriteFile(path, b, 0o644)
}
func printCosts(mode outputMode) error {
	path := costLogPath()
	b, err := os.ReadFile(path)
	if err != nil {
		if mode == modeJSON {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": true, "total": 0, "entries": 0})
			return nil
		}
		fmt.Fprintln(os.Stderr, "No cost data found")
		return nil
	}
	entries := []costEntry{}
	if err := json.Unmarshal(b, &entries); err != nil {
		return err
	}
	total := 0.0
	by := map[string]float64{}
	for _, e := range entries {
		total += e.Estimated
		by[e.Model] += e.Estimated
	}
	if mode == modeJSON {
		names := make([]string, 0, len(by))
		for m := range by {
			names = append(names, m)
		}
		sort.Strings(names)
		models := make([]map[string]any, 0, len(names))
		for _, m := range names {
			models = append(models, map[string]any{"model": m, "cost": by[m]})
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": true, "entries": len(entries), "total": total, "models": models})
	}
	fmt.Fprintf(os.Stderr, "Total generations: %d\n", len(entries))
	fmt.Fprintf(os.Stderr, "Total cost: $%.4f\n", total)
	return nil
}
func logLine(mode outputMode, level, format string, a ...any) {
	switch mode {
	case modeHuman:
		fmt.Fprintf(os.Stderr, "[nano-banana] "+format+"\n", a...)
	case modePlain:
		fmt.Fprintf(os.Stderr, format+"\n", a...)
	}
}
func exitError(err error, mode outputMode) {
	if mode == modeJSON {
		_ = json.NewEncoder(os.Stdout).Encode(result{OK: false, Error: err.Error(), At: time.Now().UTC()})
	} else {
		fmt.Fprintln(os.Stderr, "Error:", err.Error())
	}
	os.Exit(1)
}
func extFromMime(mt string) string {
	if exts, err := mime.ExtensionsByType(mt); err == nil && len(exts) > 0 {
		return exts[0]
	}
	if strings.Contains(mt, "/") {
		return "." + strings.SplitN(mt, "/", 2)[1]
	}
	return ".png"
}
func mustWd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}
func readStdinPipe() (string, error) {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return "", err
	}
	if fi.Mode()&os.ModeCharDevice != 0 {
		return "", nil // interactive terminal, not a pipe
	}
	b, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}
func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
