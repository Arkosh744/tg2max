.PHONY: build build-bot test lint clean restart logs kill up down logs-docker logs-docker-f

BOT_LOG := /tmp/tg2max-bot.log
BOT_BIN := bin/tg2max-bot
BOT_CFG := config.yaml

build:
	go build -o bin/tg2max ./cmd/tg2max

build-bot:
	go build -o $(BOT_BIN) ./cmd/tg2max-bot

build-all: build build-bot

test:
	go test ./... -count=1

lint:
	golangci-lint run ./...

clean:
	rm -rf bin/

# Kill running bot (use pidfile to avoid killing make itself)
kill:
	@if [ -f /tmp/tg2max-bot.pid ]; then kill $$(cat /tmp/tg2max-bot.pid) 2>/dev/null || true; rm -f /tmp/tg2max-bot.pid; fi

# Build, kill old, start new, show logs
restart: build-bot kill
	@sleep 1
	@nohup ./$(BOT_BIN) --config $(BOT_CFG) --verbose > $(BOT_LOG) 2>&1 & echo $$! > /tmp/tg2max-bot.pid; echo "PID: $$!"
	@sleep 2
	@tail -3 $(BOT_LOG)

# Tail logs
logs:
	@tail -30 $(BOT_LOG)

# Follow logs
logs-f:
	@tail -f $(BOT_LOG)

# Docker Compose
up:
	docker compose up -d --build

down:
	docker compose down

logs-docker:
	docker compose logs --tail=50

logs-docker-f:
	docker compose logs -f

.DEFAULT_GOAL := build-all
