.PHONY: all build test lint fmt vet check clean hooks

# Build tags required for OAuth client support (upstream MCP server auth).
TAGS := -tags mcp_go_client_oauth

# Default: run all checks then build
all: check build

# Build all binaries
build:
	go build $(TAGS) -o bin/prism ./cmd/prism
	go build $(TAGS) -o bin/prism-bridge ./cmd/prism-bridge
	go build $(TAGS) -o bin/prism-auth ./cmd/prism-auth

# Run tests with race detector
test:
	go test $(TAGS) -count=1 ./...

# Run golangci-lint (all linters including gocyclo, misspell, etc.)
lint:
	golangci-lint run ./...

# Format all Go files
fmt:
	gofmt -s -w .

# Go vet
vet:
	go vet $(TAGS) ./...

# Full check: fmt verification + vet + lint + test
check: fmt-check vet lint test

# Verify formatting without modifying files (CI-friendly)
fmt-check:
	@test -z "$$(gofmt -s -l .)" || (echo "Files not formatted:"; gofmt -s -l .; exit 1)

# Install git hooks
hooks:
	cp scripts/pre-commit .git/hooks/pre-commit
	chmod +x .git/hooks/pre-commit
	@echo "pre-commit hook installed"

# Clean build artifacts
clean:
	rm -rf bin/
