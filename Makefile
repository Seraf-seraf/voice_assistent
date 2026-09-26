.DEFAULT_GOAL := up

GO ?= go

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

webui:
	docker run -d \
	--name open-webui \
	--restart always \
	-p 3000:8080 \
	--add-host=host.docker.internal:host-gateway \
	-v open-webui:/app/backend/data \
	ghcr.io/open-webui/open-webui:main
.PHONY: webui

up:
	docker compose -f docker/docker-compose.yaml up --build --pull missing
.PHONY: up

down:
	docker compose -f docker/docker-compose.yaml down
.PHONY: down
