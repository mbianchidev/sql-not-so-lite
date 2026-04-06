.PHONY: all build test clean proto gui install uninstall dev

VERSION := 0.1.0
BINARY := sqnsl
GO_FILES := $(shell find . -name '*.go' -not -path './web/*')
WEB_DIR := web
STATIC_DIR := internal/server/static

all: gui build

# Build Go binary (includes embedded GUI)
build:
	go build -ldflags="-s -w" -o $(BINARY) ./cmd/sqnsl/

# Run tests
test:
	go test ./... -timeout 60s -v

# Run tests (short)
test-short:
	go test ./... -timeout 30s

# Generate protobuf code
proto:
	protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		api/proto/sqnsl.proto

# Build GUI
gui:
	cd $(WEB_DIR) && npm install && npm run build

# Dev mode: run GUI dev server (proxy to Go backend)
dev-gui:
	cd $(WEB_DIR) && npm run dev

# Dev mode: run Go backend
dev-backend:
	go run ./cmd/sqnsl/ start

# Install binary to PATH
install: all
	cp $(BINARY) /usr/local/bin/$(BINARY)
	@echo "Installed $(BINARY) to /usr/local/bin/"
	@echo "Run 'sqnsl install' to set up as a system service"

# Uninstall binary
uninstall:
	rm -f /usr/local/bin/$(BINARY)
	@echo "Uninstalled $(BINARY)"

# Clean build artifacts
clean:
	rm -f $(BINARY)
	rm -rf $(STATIC_DIR)
	cd $(WEB_DIR) && rm -rf node_modules dist

# Docker build
docker-build:
	docker build -t sql-not-so-lite:$(VERSION) -f deploy/docker/Dockerfile --no-cache .

# Docker run
docker-run:
	docker run -d --name sqnsl \
		-p 50051:50051 -p 8080:8080 \
		-v sqnsl-data:/data \
		sql-not-so-lite:$(VERSION)

# Docker compose
docker-up:
	docker compose -f deploy/docker/docker-compose.yml up -d --build

docker-down:
	docker compose -f deploy/docker/docker-compose.yml down

# Show help
help:
	@echo "sql-not-so-lite build targets:"
	@echo "  all          - Build GUI + Go binary (default)"
	@echo "  build        - Build Go binary"
	@echo "  test         - Run all tests"
	@echo "  proto        - Regenerate protobuf code"
	@echo "  gui          - Build React GUI"
	@echo "  dev-gui      - Run GUI dev server"
	@echo "  dev-backend  - Run Go backend"
	@echo "  install      - Install binary to /usr/local/bin"
	@echo "  uninstall    - Remove binary from /usr/local/bin"
	@echo "  clean        - Remove build artifacts"
	@echo "  docker-build - Build Docker image"
	@echo "  docker-up    - Start with docker compose"
	@echo "  docker-down  - Stop docker compose"
