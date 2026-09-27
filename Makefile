.DEFAULT_GOAL := up

YZMA_VERSION := v1.28.0
LLAMA_CPP_VERSION := v0.5.0
NATIVE_OS ?= $(shell go env GOOS)
NATIVE_ARCH := $(shell go env GOARCH)
LLAMA_LIB_DIR ?= $(if $(ASSISTANT_LLM_LIBRARY_DIR),$(ASSISTANT_LLM_LIBRARY_DIR),$(CURDIR)/.native/llama-$(LLAMA_CPP_VERSION)/$(NATIVE_OS)-$(NATIVE_ARCH)-cuda12)

build:
	mkdir -p bin
	go build -o bin/assistant ./cmd/assistant
.PHONY: build

build-windows:
	mkdir -p bin
	GOOS=windows GOARCH=amd64 go build -o bin/assistant.exe ./cmd/assistant
.PHONY: build-windows

test:
	go test ./...
.PHONY: test

test-race:
	go test -race ./...
.PHONY: test-race

lint:
	test -z "$$(gofmt -l cmd internal)"
	go vet ./...
.PHONY: lint

run:
	go run ./cmd/assistant -config config/assistant.yaml
.PHONY: run

up:
	docker compose -f docker/docker-compose.yaml up -d --build --pull missing
.PHONY: up

down:
	docker compose -f docker/docker-compose.yaml down
.PHONY: down

llm-native:
	go run github.com/hybridgroup/yzma@$(YZMA_VERSION) install --version $(LLAMA_CPP_VERSION) --processor cuda-12 --os "$(NATIVE_OS)" --lib "$(LLAMA_LIB_DIR)" --verify require
.PHONY: llm-native

test-native:
	ASSISTANT_LLM_LIBRARY_DIR="$${ASSISTANT_LLM_LIBRARY_DIR:-$(LLAMA_LIB_DIR)}" go test -tags=llm_integration -count=1 -timeout=180s ./internal/llm/llamacpp
.PHONY: test-native
