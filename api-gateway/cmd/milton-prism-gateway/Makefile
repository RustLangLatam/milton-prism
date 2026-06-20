VERSION := 1.0.0-dev
BUILD_TIME := $(shell date +%Y-%m-%dT%H:%M:%S%z)
GOMEMLIMIT := 100
GOGC := 100
GRPC_GO_LOG_SEVERITY_LEVEL := info

CONFIG_PACKAGE := milton_prism/pkg/config

BINARY_NAME := milton_prism_api_gateway
OUTPUT_DIR := ../../bin
MAIN_SOURCE := main.go
CONFIG_PATH := config.toml

.PHONY: all
all: build info run

.PHONY: build clean
build:
	@echo "Building $(BINARY_NAME)..."
	@GOMEMLIMIT=$(GOMEMLIMIT) GOGC=$(GOGC) \
	go build -ldflags "-X '$(CONFIG_PACKAGE).Version=$(VERSION)' -X '$(CONFIG_PACKAGE).BuildTime=$(BUILD_TIME)' -s -w" -o $(OUTPUT_DIR)/$(BINARY_NAME) -v $(MAIN_SOURCE)
	@echo "Build complete: $(OUTPUT_DIR)/$(BINARY_NAME)"

.PHONY: run
run: build
	@echo "Running $(BINARY_NAME)..."
	@GOMEMLIMIT=$(GOMEMLIMIT) GOGC=$(GOGC) GRPC_GO_LOG_SEVERITY_LEVEL=$(GRPC_GO_LOG_SEVERITY_LEVEL) $(OUTPUT_DIR)/$(BINARY_NAME) --config-file-path=$(CONFIG_PATH)

.PHONY: clean
clean:
	@echo "Cleaning up..."
	@rm -f $(OUTPUT_DIR)/$(BINARY_NAME)
	@echo "Clean complete."

.PHONY: info
info:
	@echo "Version: $(VERSION)"
	@echo "Build Time: $(BUILD_TIME)"
	@echo "Binary Name: $(BINARY_NAME)"
	@echo "Output Directory: $(OUTPUT_DIR)"
