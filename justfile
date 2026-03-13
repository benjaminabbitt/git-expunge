# git-expunge task runner

default:
    @just --list

# Fix Go module cache permissions
fix-perms:
    #!/bin/bash
    set -e
    if [ -d "/go" ]; then
        if command -v sudo >/dev/null 2>&1; then
            sudo chown -R $(whoami):$(whoami) /go 2>/dev/null || true
            sudo chmod -R u+w /go 2>/dev/null || true
        else
            chown -R $(whoami):$(whoami) /go 2>/dev/null || true
            chmod -R u+w /go 2>/dev/null || true
        fi
    fi
    if [ -d "$HOME/go" ]; then
        chmod -R u+w $HOME/go 2>/dev/null || true
    fi
    mkdir -p /go/pkg/mod 2>/dev/null || true
    mkdir -p $HOME/go/pkg/mod 2>/dev/null || true

fix-git-dubious-ownership-warning:
    git config --global --add safe.directory /workspace 2>/dev/null || true

# Initialize project (run once after cloning)
init: fix-perms fix-git-dubious-ownership-warning
    #!/bin/bash
    set -e
    echo "Initializing git-expunge project..."
    go mod download
    go mod tidy
    mkdir -p bin/
    echo "Initialization complete!"

# Build the binary (static)
build: fix-git-dubious-ownership-warning
    #!/bin/bash
    set -e
    mkdir -p bin/
    VERSION=$(cat VERSION 2>/dev/null || echo "dev")
    echo "Building git-expunge $VERSION (static binary)..."
    CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=$VERSION" -trimpath -o bin/git-expunge ./cmd/git-expunge
    echo "Build completed: bin/git-expunge"

# Run all tests
test:
    go test ./...

# Run tests with coverage
test-coverage:
    go test -cover ./...

# Run unit tests only (short mode)
test-unit:
    go test -short ./...

# Run integration tests
test-integration:
    go test -v ./tests/integration/...

# Run linter
lint:
    #!/bin/bash
    if command -v golangci-lint >/dev/null 2>&1; then
        golangci-lint run
    else
        echo "golangci-lint not found, running go vet instead..."
        go vet ./...
    fi

# Format code
fmt:
    go fmt ./...

# Run the CLI with arguments
run *args:
    go run ./cmd/git-expunge {{args}}

# Clean build artifacts
clean:
    rm -rf bin/
    rm -rf tests/integration/fixtures/repos/
    go clean

# Install dependencies
deps: fix-perms
    go mod download
    go mod tidy

# Check for outdated dependencies
outdated:
    go list -u -m all

# Generate test coverage report
cover:
    go test -coverprofile=coverage.out ./...
    go tool cover -html=coverage.out -o coverage.html
    @echo "Coverage report: coverage.html"

# Verify the module
verify:
    go mod verify

# Build for all platforms with static linking
build-all: fix-perms fix-git-dubious-ownership-warning
    #!/bin/bash
    set -e
    VERSION=$(cat VERSION 2>/dev/null || echo "dev")
    LDFLAGS="-s -w -X main.version=$VERSION"
    echo "Building git-expunge $VERSION for all platforms with static linking..."
    mkdir -p bin/

    # Linux amd64
    echo "Building for Linux amd64..."
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="$LDFLAGS" -trimpath -o bin/git-expunge-linux-amd64 ./cmd/git-expunge

    # Linux arm64
    echo "Building for Linux arm64..."
    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="$LDFLAGS" -trimpath -o bin/git-expunge-linux-arm64 ./cmd/git-expunge

    # macOS amd64 (Intel)
    echo "Building for macOS amd64..."
    CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags="$LDFLAGS" -trimpath -o bin/git-expunge-darwin-amd64 ./cmd/git-expunge

    # macOS arm64 (Apple Silicon)
    echo "Building for macOS arm64..."
    CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags="$LDFLAGS" -trimpath -o bin/git-expunge-darwin-arm64 ./cmd/git-expunge

    # Windows amd64
    echo "Building for Windows amd64..."
    CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags="$LDFLAGS" -trimpath -o bin/git-expunge-windows-amd64.exe ./cmd/git-expunge

    # Windows arm64
    echo "Building for Windows arm64..."
    CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build -ldflags="$LDFLAGS" -trimpath -o bin/git-expunge-windows-arm64.exe ./cmd/git-expunge

    # FreeBSD amd64
    echo "Building for FreeBSD amd64..."
    CGO_ENABLED=0 GOOS=freebsd GOARCH=amd64 go build -ldflags="$LDFLAGS" -trimpath -o bin/git-expunge-freebsd-amd64 ./cmd/git-expunge

    echo "All builds completed successfully!"
    ls -lh bin/

# Show current version
version:
    @cat VERSION 2>/dev/null || echo "dev"

# Install the binary to ~/.local/bin
install: build
    mkdir -p ~/.local/bin
    cp bin/git-expunge ~/.local/bin/
    @echo "Installed to ~/.local/bin/git-expunge"
