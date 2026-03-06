# Nano Banana Image Skill

AI image generation CLI built in Go, powered by Gemini image models. Rewritten from [nano-banana-2-skill](https://github.com/kingbootoshi/nano-banana-2-skill/).

Key differences from the original:
- Restructured codebase with full `--help` output
- Stdin pipe support (`echo "prompt" | nano-banana`)
- Human-readable logging with `[nano-banana]` prefix
- `--costs --json` includes per-model breakdown
- Pure Go green screen background removal (no external tools needed)
- Unit tests
- GitHub Actions CI / Release workflows

## Install

Requires: Go 1.25+

```bash
go mod tidy
go build -o ~/.local/bin/nano-banana ./cmd/nano-banana
```

Make sure `~/.local/bin` is in your PATH.

## API Key

Gemini API key is resolved in this order:

1. `--api-key` flag
2. `GEMINI_API_KEY` environment variable
3. `.env` in current directory
4. `.env` next to the executable's parent directory
5. `~/.nano-banana/.env`

```bash
# Recommended: environment variable
export GEMINI_API_KEY=your_key_here

# Or use a dotenv file
mkdir -p ~/.nano-banana
echo "GEMINI_API_KEY=your_key_here" > ~/.nano-banana/.env
```

Get an API key: https://aistudio.google.com/apikey

## Usage

```bash
nano-banana "minimal dashboard UI with dark theme"
nano-banana "luxury product mockup" -o product -s 2K
nano-banana "cinematic scene" -a 16:9 -s 4K
nano-banana "change to white background" -r input.png -o output
nano-banana "robot mascot" -t -o mascot

# Read prompt from stdin
echo "a cat in a spacesuit" | nano-banana
pbpaste | nano-banana -s 2K -o result
```

## Options

| Option | Default | Description |
|--------|---------|-------------|
| `-o, --output` | `nano-gen-{ts}` | Output filename (no extension) |
| `-s, --size` | `1K` | Image size: `512`, `1K`, `2K`, `4K` |
| `-a, --aspect` | model default | Aspect ratio: `1:1`, `16:9`, `9:16`, `4:3`, `3:4`, etc. |
| `-m, --model` | `flash` | Model: `flash`/`nb2`, `pro`/`nb-pro`, or any model ID |
| `-d, --dir` | current directory | Output directory |
| `-r, --ref` | - | Reference image (can be used multiple times) |
| `-t, --transparent` | - | Green screen background removal (pure Go, no external tools) |
| `--seed` | random | Fixed seed for reproducible generation |
| `--person` | `ALL` | Person generation: `ALL`, `ADULT`, `NONE` |
| `--thinking` | model default | Thinking level: `minimal`, `low`, `medium`, `high` |
| `--api-key` | - | Gemini API key (highest priority) |
| `--costs` | - | Show cost summary |
| `--json` | - | JSON output to stdout (script-friendly) |
| `--plain` | - | Plain text output to stdout |
| `--jq EXPR` | - | Filter JSON output (requires `--json`) |

## Models

| Alias | Model | Use case |
|-------|-------|----------|
| `flash`, `nb2` | Gemini 3.1 Flash | Default, fast and cheap |
| `pro`, `nb-pro` | Gemini 3 Pro | Highest quality |

## Transparent Mode

`-t` automatically adds green screen instructions to the prompt, then removes the background using a built-in pure Go implementation of colorkey + despill + trim. No external tools required.

```bash
nano-banana "robot mascot" -t -o mascot
```

## Cost Tracking

Every generation is logged to `~/.nano-banana/costs.json`:

```bash
nano-banana --costs
nano-banana --costs --json
```

## Development

```bash
make test     # Run tests
make build    # Build binary
make lint     # vet + format check
make ci       # Full CI pipeline
```
