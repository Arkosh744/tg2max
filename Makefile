.PHONY: build build-bot build-all build-docker test lint clean restart up down logs logs-f deploy

# --- Build ---

build:
	go build -o bin/tg2max ./cmd/tg2max

build-bot:
	go build -o bin/tg2max-bot ./cmd/tg2max-bot

build-all: build build-bot

build-docker:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/tg2max-bot-linux ./cmd/tg2max-bot

# --- Test ---

test:
	go test ./... -count=1

test-cover:
	go test ./... -count=1 -cover

lint:
	golangci-lint run ./...

clean:
	rm -rf bin/

# --- Docker (primary workflow) ---

deploy: build-docker
	docker compose up -d --build tg2max-bot

up: build-docker
	docker compose up -d --build

down:
	docker compose down

restart:
	docker compose restart tg2max-bot

logs:
	docker compose logs tg2max-bot --tail=50

logs-f:
	docker compose logs tg2max-bot -f

# --- Local (without Docker, needs source .env) ---

BOT_LOG := /tmp/tg2max-bot.log

run-local: build-bot
	@set -a && source .env && set +a && ./bin/tg2max-bot --verbose > $(BOT_LOG) 2>&1 & echo "PID: $$!"

logs-local:
	@tail -30 $(BOT_LOG)

logs-local-f:
	@tail -f $(BOT_LOG)

.DEFAULT_GOAL := build-all
