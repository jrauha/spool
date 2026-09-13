AIR_VERSION := v1.61.7

.PHONY: test build run watch

test:
	go test ./...

build:
	go build -o bin/spool ./cmd/spool

run:
	go run ./cmd/spool

watch:
	go run github.com/air-verse/air@$(AIR_VERSION)
