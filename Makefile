.DEFAULT_GOAL := up

GO ?= go
YZMA_VERSION := v1.28.0
LLAMA_CPP_VERSION := v0.5.0
NATIVE_OS ?= $(shell $(GO) env GOOS)
NATIVE_ARCH := $(shell $(GO) env GOARCH)
LLAMA_LIB_DIR ?= $(if $(ASSISTANT_LLM_LIBRARY_DIR),$(ASSISTANT_LLM_LIBRARY_DIR),$(CURDIR)/.native/llama-$(LLAMA_CPP_VERSION)/$(NATIVE_OS)-$(NATIVE_ARCH)-cuda12)

build:
	mkdir -p bin
	$(GO) build -o bin/assistant ./cmd/assistant
.PHONY: build

build-windows:
	mkdir -p bin
	GOOS=windows GOARCH=amd64 $(GO) build -o bin/assistant.exe ./cmd/assistant
.PHONY: build-windows

test:
	$(GO) test ./...
.PHONY: test

test-race:
	$(GO) test -race ./...
.PHONY: test-race

lint:
	test -z "$$(gofmt -l cmd internal)"
	$(GO) vet ./...
.PHONY: lint

run:
	$(GO) run ./cmd/assistant -config config/assistant.yaml
.PHONY: run

up:
	docker compose -f docker/docker-compose.yaml up --build --pull missing
.PHONY: up

down:
	docker compose -f docker/docker-compose.yaml down
.PHONY: down

llm-native:
	$(GO) run github.com/hybridgroup/yzma@$(YZMA_VERSION) install --version $(LLAMA_CPP_VERSION) --processor cuda-12 --os "$(NATIVE_OS)" --lib "$(LLAMA_LIB_DIR)" --verify require
	$(MAKE) llm-native-verify GO="$(GO)" LLAMA_LIB_DIR="$(LLAMA_LIB_DIR)" NATIVE_OS="$(NATIVE_OS)"
.PHONY: llm-native

llm-native-verify:
	$(GO) run github.com/hybridgroup/yzma@$(YZMA_VERSION) verify --version $(LLAMA_CPP_VERSION) --lib "$(LLAMA_LIB_DIR)" --strict
.PHONY: llm-native-verify

test-native:
	ASSISTANT_LLM_LIBRARY_DIR="$${ASSISTANT_LLM_LIBRARY_DIR:-$(LLAMA_LIB_DIR)}" $(GO) test -tags=llm_integration -count=1 -timeout=180s ./internal/llm/llamacpp
.PHONY: test-native
