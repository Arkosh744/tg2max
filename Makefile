.PHONY: build build-bot test lint clean

build:
	go build -o bin/tg2max ./cmd/tg2max

build-bot:
	go build -o bin/tg2max-bot ./cmd/tg2max-bot

build-all: build build-bot

test:
	go test ./... -count=1

lint:
	golangci-lint run ./...

clean:
	rm -rf bin/

.DEFAULT_GOAL := build-all
