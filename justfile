# nomad-storage-gateway justfile

set dotenv-load := true

# List all available recipes
default:
    @just --list

# Run the storage gateway locally
run *args:
    go run ./cmd/gateway {{args}}

# Build local binary
build:
    mkdir -p bin
    go build -trimpath -o bin/gateway ./cmd/gateway

# Run test suite with race detector and coverage
test *args:
    go test -v -race -cover ./... {{args}}

# Run performance benchmarks
bench *args:
    go test -v -bench=. -benchmem ./... {{args}}

# Download & clean up module dependencies
tidy:
    go mod tidy

# Format and analyze code
lint:
    go fmt ./...
    go vet ./...

# Clean build artifacts
clean:
    rm -rf bin/ coverage.out
