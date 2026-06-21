GOOS   = linux
GOARCH = amd64

GATEWAY_MAKE_DIR  = api-gateway/cmd/milton-prism-gateway
GATEWAY_BIN_DIR   = api-gateway/bin

export GOOS GOARCH

.PHONY: all build build-gateway clean help

all: build

build: build-gateway
	@echo "Build completed."

build-gateway:
	@echo "Building API gateway..."
	$(MAKE) -C $(GATEWAY_MAKE_DIR) build

clean:
	@echo "Cleaning generated binaries..."
	rm -f $(GATEWAY_BIN_DIR)/*
	@echo "Cleanup complete."

help:
	@echo "Available commands:"
	@echo "  build    : Builds all binaries."
	@echo "  all      : (Default) Alias for build."
	@echo "  clean    : Cleans all generated binaries."
	@echo "  help     : Shows this help."