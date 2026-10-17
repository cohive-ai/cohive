# 用 Docker 部署协作服务

协作服务是一个 `cohive-server` 程序，网页和五个平台的 `cohive` 命令行安装包都嵌在里面；数据只存在 Postgres 里。仓库里的 `deploy/docker-compose.yml` 把它和 Postgres 17 一起起来，数据放在两个 Docker 数据卷里。

需要：一台装了 Docker（含 Compose 插件）的机器。不需要装 Go、Node 或 pnpm，构建在镜像里完成。

## 1. 起服务

```bash
git clone https://github.com/cohive-ai/cohive.git
cd cohive/deploy
cp .env.example .env
```

编辑 `.env`，至少填 `POSTGRES_PASSWORD`（只用字母和数字，可以用 `openssl rand -hex 16` 生成）。然后：

```bash
docker compose up -d --build
```

第一次构建要下载基础镜像、编译网页与五个平台的命令行，几分钟；之后启动只要几秒。`docker compose ps` 里两个服务都是 `healthy` 即可。

缺省只监听本机 `127.0.0.1:8787`，由同一台机器上的隧道或反向代理对外（见第 4 节）。只在内网用、让同事直接访问时，在 `.env` 里设 `COHIVE_BIND=0.0.0.0`，然后 `docker compose up -d`。

## 2. 第一次登录

首次启动时会建一个初始管理员 `admin`（服务器管理员，也是 `default` 工作空间的管理员）。随机密码只写在数据卷里的文件中，不进日志：

```bash
docker compose exec cohive cat /data/initial_admin_password
```

用里面的登录名和密码登录网页（`http://localhost:8787`），第一次登录必须改密码，改完这个文件自动删除。忘记密码时：

```bash
docker compose exec cohive cohive-server admin reset-password --name admin
```

新密码只在终端显示一次，登录后须立即修改。

登录后按顺序做三件事：

1. **对外地址**：“服务器管理 → 设置”里确认同事访问用的地址（例如 `https://cohive.example.com`）。也可以在 `.env` 里设 `COHIVE_EXTERNAL_URL` 锁定。群消息里的链接、编程工具经隧道访问 MCP 时的主机名都用它。
2. **工作空间**：一个项目一个群、一个工作空间。初始就有 `default`，可在“服务器管理 → 工作空间”里再建。
3. **邀请成员**：工作空间的“管理 → 邀请”里生成链接发给同事。成员接下来怎么做见 [新成员接入](onboarding.md)。

## 3. 群通知（可选）

1. 在 Telegram 里找 @BotFather 创建机器人，把令牌写进 `.env` 的 `COHIVE_TELEGRAM_BOT_TOKEN`，然后 `docker compose up -d`（改了 `.env` 要用 `up -d` 重建容器，`restart` 不会读新值）。
2. 把机器人拉进项目群，在群里发 `/chatid` 拿到群编号，填进工作空间“管理 → 工作空间”。
3. 确认接通：`docker compose exec cohive cohive-server notify test --workspace default`。

不配机器人也能用，只是没有群里 @ 人和群里审批按钮；待发的通知会保留，配好后重启即补发。详见 [群通知与公网访问](group-notify.md)。

## 4. 让异地成员访问

网络打通由团队自己选方式：Cloudflare 隧道、带 HTTPS 的反向代理（Caddy、Nginx）、内网 VPN 都可以。要点是对外用 HTTPS，并把对外地址设成同事实际访问的地址。

**Cloudflare 隧道**（compose 里带了一个可选服务）：

1. 在 Cloudflare 后台建一条隧道，公网主机名（例如 `cohive.example.com`）指向 `http://cohive:8787`。
2. 把隧道令牌写进 `.env` 的 `CLOUDFLARE_TUNNEL_TOKEN`。
3. `docker compose --profile tunnel up -d`。

隧道也可以跑在宿主机上，指向 `http://127.0.0.1:8787`。

**来源地址与限频**：登录、注册、设备授权等接口按来源地址限频。经 Docker 端口映射进来的请求，容器看到的来源是 Docker 网关，所以 compose 缺省信任私有网段（`COHIVE_TRUSTED_PROXIES`）转发来的 `CF-Connecting-IP` 与 `X-Forwarded-For`，按真实来源计数。把端口直接开在不可信的网络上时，请把它改成只含你的隧道或代理所在的地址段。

## 5. 备份与恢复

所有数据都在 Postgres 里（数据卷 `pgdata`）；数据卷 `data` 只放初始管理员密码文件。

**备份**（服务不用停）：

```bash
docker compose exec -T postgres pg_dump -U cohive -d cohive -Fc > cohive-$(date +%Y%m%d-%H%M%S).dump
```

建议用 cron 或 systemd 定时执行，并把备份拷到另一台机器。

**恢复**（会覆盖当前数据）：

```bash
docker compose stop cohive
docker compose exec -T postgres pg_restore -U cohive -d cohive --clean --if-exists --single-transaction < cohive-xxxx.dump
docker compose start cohive
```

`docker compose down` 只删容器、不删数据；**`docker compose down -v` 会连数据卷一起删掉**，除非确实要清空，不要加 `-v`。

## 6. 升级

先备份，然后：

```bash
git pull
docker compose up -d --build
```

服务启动时自动执行数据库迁移。命令行安装包随服务端一起升级，成员再执行一次个人中心里的安装命令即可；版本不一致时 `cohive status` 会提醒。

构建时可以在 `.env` 里填 `COHIVE_VERSION`、`COHIVE_COMMIT`，它们会显示在网页和 `cohive version` 里。

## 7. 服务器命令

日常管理都在网页上做；服务器命令行作为兜底，在容器里执行：

```bash
docker compose exec cohive cohive-server help
docker compose exec cohive cohive-server account list
docker compose exec cohive cohive-server audit list
```

## 8. 环境变量

都写在 `deploy/.env` 里（`.env.example` 有注释）：

| 变量 | 作用 |
|---|---|
| `POSTGRES_PASSWORD` | 数据库密码，必填 |
| `COHIVE_EXTERNAL_URL` | 对外地址；设了就锁定，网页上不能改 |
| `COHIVE_TELEGRAM_BOT_TOKEN` | Telegram 机器人令牌 |
| `CLOUDFLARE_TUNNEL_TOKEN` | 配合 `--profile tunnel` 使用的隧道令牌 |
| `COHIVE_BIND`、`COHIVE_PORT` | 宿主机上的监听地址与端口，缺省 `127.0.0.1`、`8787` |
| `COHIVE_ALLOWED_HOSTS` | 除对外地址外，还允许访问 MCP 的域名（逗号分隔） |
| `COHIVE_TRUSTED_PROXIES` | 信任哪些地址转发来的来源地址头，缺省为私有网段 |
| `COHIVE_IMAGE`、`COHIVE_VERSION`、`COHIVE_COMMIT` | 镜像名与构建时写入的版本号、提交号 |

不用 Docker、直接在机器上运行 `cohive-server` 的方式见 [不用 Docker 的单机运行](single-host.md)。
