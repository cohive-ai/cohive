COMPOSE := docker compose -f deploy/docker-compose.dev.yml
export COHIVE_DATABASE_URL ?= postgres://cohive:cohive@localhost:55432/cohive?sslmode=disable

.PHONY: db-up db-down test build run

db-up:
	$(COMPOSE) up -d --wait

db-down:
	$(COMPOSE) down

test:
	go test ./...

build:
	go build -o bin/cohive-server ./cmd/cohive-server

run: build
	./bin/cohive-server migrate && ./bin/cohive-server serve
