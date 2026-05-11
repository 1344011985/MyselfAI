BINARY   := myself-ai
DIST_DIR := dist
PKG      := main
COMMIT   := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE     := $(shell date -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || echo "unknown")
LDFLAGS  := -X $(PKG).GitCommit=$(COMMIT) -X $(PKG).BuildDate=$(DATE)

PLATFORMS := linux/amd64 linux/arm64 windows/amd64 darwin/amd64 darwin/arm64

GO := go

.PHONY: build build-all release test lint clean

## build: build for the current platform
build:
	$(GO) build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY) ./cmd/bot/

## build-all: cross-compile for all target platforms
build-all: $(DIST_DIR)
	@for platform in $(PLATFORMS); do \
		os=$$(echo $$platform | cut -d/ -f1); \
		arch=$$(echo $$platform | cut -d/ -f2); \
		output=$(DIST_DIR)/$(BINARY)-$$os-$$arch; \
		if [ "$$os" = "windows" ]; then output=$$output.exe; fi; \
		echo "Building $$output ..."; \
		GOOS=$$os GOARCH=$$arch $(GO) build -ldflags "$(LDFLAGS)" -o $$output ./cmd/bot/ || exit 1; \
	done

## release: build-all then compress each binary
release: build-all
	@for platform in $(PLATFORMS); do \
		os=$$(echo $$platform | cut -d/ -f1); \
		arch=$$(echo $$platform | cut -d/ -f2); \
		binary=$(DIST_DIR)/$(BINARY)-$$os-$$arch; \
		if [ "$$os" = "windows" ]; then \
			zip -j $(DIST_DIR)/$(BINARY)-$$os-$$arch.zip $$binary.exe; \
		else \
			tar -czf $(DIST_DIR)/$(BINARY)-$$os-$$arch.tar.gz -C $(DIST_DIR) $(BINARY)-$$os-$$arch; \
		fi; \
	done

## test: run all tests
test:
	$(GO) test ./... -count=1

## lint: run golangci-lint
lint:
	golangci-lint run ./...

## clean: remove build artifacts
clean:
	rm -rf $(DIST_DIR)

$(DIST_DIR):
	mkdir -p $(DIST_DIR)
