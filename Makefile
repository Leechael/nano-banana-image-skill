CLI_NAME   := nano-banana
BUILD_TARGET := ./cmd/nano-banana

.PHONY: build test vet fmt lint clean tidy

build:
	go build -v -o $(CLI_NAME) $(BUILD_TARGET)

test:
	go test ./... -count=1

vet:
	go vet ./...

fmt:
	gofmt -w ./cmd

fmt-check:
	@unformatted=$$(gofmt -l ./cmd); \
	if [ -n "$$unformatted" ]; then \
		echo "Unformatted files:"; echo "$$unformatted"; exit 1; \
	fi

lint: vet fmt-check

tidy:
	go mod tidy

clean:
	rm -f $(CLI_NAME)
	rm -rf dist/

ci: tidy fmt-check vet test build
