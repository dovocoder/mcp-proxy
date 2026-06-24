.PHONY: build test vet lint frontend clean

# Build the Go binary (without frontend embed)
build:
	go build -ldflags="-s -w" -o mcp-proxy .

# Build frontend + embed into Go binary
build-all: frontend build

# Run all Go tests
test:
	go test ./... -v

# Run go vet
vet:
	go vet ./...

# Run golangci-lint (if installed)
lint:
	golangci-lint run ./...

# Build the frontend
frontend:
	cd web && npm run build

# Clean build artifacts
clean:
	rm -f mcp-proxy
	rm -rf web/dist
	rm -f web/tsconfig.tsbuildinfo

# Install frontend dependencies
install:
	cd web && npm install

# Run the development server
dev:
	go run . &
	cd web && npm run dev

# Run tests with coverage
coverage:
	go test ./... -coverprofile=coverage.out -covermode=atomic
	go tool cover -func=coverage.out | tail -5
