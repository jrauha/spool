AIR_VERSION := v1.61.7

.PHONY: test build run watch watch-server watch-worker watch-scheduler

test:
	go test ./...

build:
	go build -o bin/spool ./cmd/spool

run:
	go run ./cmd/spool

watch:
	$(MAKE) --no-print-directory -j3 watch-server watch-worker watch-scheduler

watch-server:
	go run github.com/air-verse/air@$(AIR_VERSION) -c .air.toml

watch-worker:
	go run github.com/air-verse/air@$(AIR_VERSION) -c .air.worker.toml

watch-scheduler:
	go run github.com/air-verse/air@$(AIR_VERSION) -c .air.scheduler.toml
