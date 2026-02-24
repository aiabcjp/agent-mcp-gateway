BINARY_NAME := qa-gateway
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X main.Version=$(VERSION)"
GOFLAGS := -trimpath

.PHONY: all build test lint vet clean docker run help

all: lint test build

build:
	go build $(GOFLAGS) $(LDFLAGS) -o bin/$(BINARY_NAME) ./cmd/qa-gateway

test:
	go test -race -cover ./...

test-coverage:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

lint: vet
	@which staticcheck > /dev/null 2>&1 || go install honnef.co/go/tools/cmd/staticcheck@latest
	staticcheck ./...

vet:
	go vet ./...

clean:
	rm -rf bin/ coverage.out coverage.html

docker:
	docker build -t $(BINARY_NAME):$(VERSION) .

run: build
	./bin/$(BINARY_NAME) serve --config config.yaml

fmt:
	gofmt -s -w .

tidy:
	go mod tidy

help:
	@echo "Available targets:"
	@echo "  build          - Build the binary"
	@echo "  test           - Run tests with race detection"
	@echo "  test-coverage  - Run tests with coverage report"
	@echo "  lint           - Run staticcheck linter"
	@echo "  vet            - Run go vet"
	@echo "  clean          - Remove build artifacts"
	@echo "  docker         - Build Docker image"
	@echo "  run            - Build and run the server"
	@echo "  fmt            - Format code"
	@echo "  tidy           - Run go mod tidy"
