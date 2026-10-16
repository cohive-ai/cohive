COMPOSE := docker compose -f deploy/docker-compose.dev.yml
export COHIVE_DATABASE_URL ?= postgres://cohive:cohive@localhost:55432/cohive?sslmode=disable

.PHONY: db-up db-down test web web-dev build run cli cli-dist install-cli

# 版本号：有标签时取最近的标签，否则取提交号；工作树有未提交改动时带 -dirty。服务端与各平台命令行共用
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null)
LDFLAGS := -s -w -X github.com/cohive-ai/cohive/internal/buildinfo.Version=$(VERSION) -X github.com/cohive-ai/cohive/internal/buildinfo.Commit=$(COMMIT)

db-up:
	$(COMPOSE) up -d --wait

db-down:
	$(COMPOSE) down

test:
	go test ./...

# 构建看板，产物写入 web/dist，由服务端嵌入
web:
	cd web && pnpm install --frozen-lockfile && pnpm build && pnpm test

# 看板开发模式：http://localhost:5173，接口转发到本机 8787 端口的协作服务
web-dev:
	cd web && pnpm dev

# 服务端：看板与五个平台的命令行安装包都嵌在里面，同一个版本号
build: web cli cli-dist
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/cohive-server ./cmd/cohive-server

# 成员本机用的命令行
cli:
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/cohive ./cmd/cohive

# 交叉编译 macOS、Linux、Windows 的命令行，压缩后放进 internal/clidist/files，由服务端嵌入并提供下载
cli-dist:
	go run ./internal/clidist/mkdist -out internal/clidist/files -version '$(VERSION)' -commit '$(COMMIT)' -ldflags '$(LDFLAGS)'

# 安装到 Go 的 bin 目录（通常是 ~/go/bin），编程工具要能在 PATH 里找到 cohive
install-cli:
	go install -ldflags '$(LDFLAGS)' ./cmd/cohive

run: build
	./bin/cohive-server serve
