COMPOSE := docker compose -f deploy/docker-compose.dev.yml
export COHIVE_DATABASE_URL ?= postgres://cohive:cohive@localhost:55432/cohive?sslmode=disable

.PHONY: db-up db-down test web web-dev build run

db-up:
	$(COMPOSE) up -d --wait

db-down:
	$(COMPOSE) down

test:
	go test ./...

# 构建看板，产物写入 web/dist，由服务端嵌入
web:
	cd web && pnpm install --frozen-lockfile && pnpm build

# 看板开发模式：http://localhost:5173，接口转发到本机 8787 端口的协作服务
web-dev:
	cd web && pnpm dev

build: web
	go build -o bin/cohive-server ./cmd/cohive-server

run: build
	./bin/cohive-server serve
