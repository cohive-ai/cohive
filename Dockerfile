# Cohive 协作服务镜像：网页与五个平台的命令行安装包都嵌在同一个 cohive-server 程序里。
#   docker build -t cohive --build-arg VERSION=v0.1.0 --build-arg COMMIT=$(git rev-parse --short HEAD) .
# 部署用 deploy/docker-compose.yml，说明见 docs/guides/deploy-docker.md。

FROM node:22-alpine AS web
WORKDIR /src/web
RUN corepack enable && corepack prepare pnpm@9.15.9 --activate
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

FROM golang:1.25-alpine AS build
ARG VERSION=dev
ARG COMMIT=
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
# 与 Makefile 的 build 目标一致：先交叉编译命令行安装包，再构建嵌入它们的服务端
RUN LDFLAGS="-s -w -X github.com/cohive-ai/cohive/internal/buildinfo.Version=${VERSION} -X github.com/cohive-ai/cohive/internal/buildinfo.Commit=${COMMIT}" \
 && go run ./internal/clidist/mkdist -out internal/clidist/files -version "${VERSION}" -commit "${COMMIT}" -ldflags "${LDFLAGS}" \
 && CGO_ENABLED=0 go build -trimpath -ldflags "${LDFLAGS}" -o /out/cohive-server ./cmd/cohive-server

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata \
 && adduser -D -H -u 10001 cohive \
 && mkdir -p /data && chown cohive:cohive /data && chmod 700 /data
COPY --from=build /out/cohive-server /usr/local/bin/cohive-server
USER cohive
# 数据目录：首次启动时初始管理员的登录名与密码写在 /data/initial_admin_password
ENV COHIVE_DATA_DIR=/data
VOLUME /data
EXPOSE 8787
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8787/healthz >/dev/null || exit 1
ENTRYPOINT ["cohive-server"]
CMD ["serve", "--addr", ":8787"]
