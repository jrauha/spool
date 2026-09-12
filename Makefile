.PHONY: test build run

test:
	go test ./...

build:
	go build -o bin/spool ./cmd/spool

run:
	go run ./cmd/spool
