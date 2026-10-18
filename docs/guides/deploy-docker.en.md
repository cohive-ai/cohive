# Deploy the Cohive server with Docker

[中文](deploy-docker.md)

The Cohive server is a single `cohive-server` program. The web UI and the `cohive` CLI installers for five platforms are embedded in it, and all data lives in Postgres. `deploy/docker-compose.yml` in the repository starts it together with Postgres 17, with data kept in two Docker volumes.

You need a machine with Docker (including the Compose plugin). You do not need Go, Node or pnpm: everything is built inside the image.

## 1. Start the server

```bash
git clone https://github.com/cohive-ai/cohive.git
cd cohive/deploy
cp .env.example .env
```

Edit `.env` and set at least `POSTGRES_PASSWORD` (letters and digits only; `openssl rand -hex 16` works). Then:

```bash
docker compose up -d --build
```

The first build downloads base images and compiles the web UI and the CLI for five platforms, which takes a few minutes. Later starts take seconds. When `docker compose ps` shows both services as `healthy`, you are done.

By default the server listens only on `127.0.0.1:8787`, and a tunnel or reverse proxy on the same machine exposes it (see section 4). If it is for internal use only and teammates connect to it directly, set `COHIVE_BIND=0.0.0.0` in `.env` and run `docker compose up -d` again.

## 2. Sign in for the first time

On first start, the server creates an initial admin account `admin` (a server admin, and also the admin of the `default` workspace). Its random password is written only to a file in the data volume, never to the logs:

```bash
docker compose exec cohive cat /data/initial_admin_password
```

Sign in to the web UI (`http://localhost:8787`) with the username and password in that file. You must change the password on first sign-in, and the file is deleted once you do. If you forget the password:

```bash
docker compose exec cohive cohive-server admin reset-password --name admin
```

The new password is shown once in the terminal. Change it right after signing in.

After signing in, do these three things in order:

1. **External URL**: under Server admin → Settings, confirm the address your teammates use to reach the server (for example `https://cohive.example.com`). You can also lock it by setting `COHIVE_EXTERNAL_URL` in `.env`. Links in group messages and the host name used when coding tools reach MCP through a tunnel both come from this setting.
2. **Workspace**: one project, one chat group, one workspace. A `default` workspace already exists; create more under Server admin → Workspaces.
3. **Invite members**: in a workspace, go to Admin → Invites, generate an invite link and send it to your teammates. For what they do next, see [Onboarding new members](onboarding.en.md).

## 3. Group notifications (optional)

1. In Telegram, create a bot with @BotFather and put its token in `COHIVE_TELEGRAM_BOT_TOKEN` in `.env`. Then run `docker compose up -d`. (After editing `.env`, use `up -d` to recreate the container; `restart` does not pick up new values.)
2. Add the bot to your project chat group, send `/chatid` in the group to get the group ID, and enter it under Admin → Workspace in that workspace.
3. Check that it works: `docker compose exec cohive cohive-server notify test --workspace default`.

Cohive works without a bot; you just don't get @-mentions or approval buttons in the group. Notifications waiting to be sent are kept and delivered after you configure the bot and restart. See [Group notifications and public access](group-notify.en.md) for details.

## 4. Let remote members connect

How you make the server reachable is up to your team: a Cloudflare Tunnel, a reverse proxy with HTTPS (Caddy, Nginx) or an internal VPN all work. The key points are to use HTTPS from outside, and to set the external URL to the address your teammates actually use.

**Cloudflare Tunnel** (an optional service is included in the compose file):

1. In the Cloudflare dashboard, create a tunnel and point a public hostname (for example `cohive.example.com`) at `http://cohive:8787`.
2. Put the tunnel token in `CLOUDFLARE_TUNNEL_TOKEN` in `.env`.
3. Run `docker compose --profile tunnel up -d`.

The tunnel can also run on the host and point at `http://127.0.0.1:8787`.

**Client addresses and rate limiting**: sign-in, registration, device authorization and similar endpoints are rate limited per client address. For requests arriving through a Docker port mapping, the container sees the Docker gateway as the source. So the compose file by default trusts the `CF-Connecting-IP` and `X-Forwarded-For` headers forwarded from private network ranges (`COHIVE_TRUSTED_PROXIES`) and counts requests by the real client address. If you expose the port directly to an untrusted network, narrow this setting to the address range of your tunnel or proxy only.

## 5. Backup and restore

All data is in Postgres (the `pgdata` volume). The `data` volume only holds the initial admin password file.

**Back up** (no need to stop the server):

```bash
docker compose exec -T postgres pg_dump -U cohive -d cohive -Fc > cohive-$(date +%Y%m%d-%H%M%S).dump
```

Run this on a schedule with cron or a systemd timer, and copy the backups to another machine.

**Restore** (this overwrites the current data):

```bash
docker compose stop cohive
docker compose exec -T postgres pg_restore -U cohive -d cohive --clean --if-exists --single-transaction < cohive-xxxx.dump
docker compose start cohive
```

`docker compose down` removes only containers, not data. **`docker compose down -v` also deletes the data volumes.** Do not add `-v` unless you really want to wipe everything.

## 6. Upgrade

Back up first, then:

```bash
git pull
docker compose up -d --build
```

The server runs database migrations automatically on startup. The CLI installers are upgraded together with the server: members just run the install command from Profile again, and `cohive status` warns when the versions differ.

At build time you can set `COHIVE_VERSION` and `COHIVE_COMMIT` in `.env`; they are shown in the web UI and in `cohive version`.

## 7. Server commands

Day-to-day administration happens in the web UI. The server command line is a fallback, run inside the container:

```bash
docker compose exec cohive cohive-server help
docker compose exec cohive cohive-server account list
docker compose exec cohive cohive-server audit list
```

## 8. Environment variables

All of these go in `deploy/.env` (`.env.example` has comments):

| Variable | Purpose |
|---|---|
| `POSTGRES_PASSWORD` | Database password. Required |
| `COHIVE_EXTERNAL_URL` | External URL. When set, it is locked and cannot be changed in the web UI |
| `COHIVE_DEFAULT_LANGUAGE` | Server default language, `en` or `zh-CN`; English when unset. Set it to `zh-CN` if your team mainly uses Chinese. Members can override their own CLI language with `COHIVE_LANG` |
| `COHIVE_TELEGRAM_BOT_TOKEN` | Telegram bot token |
| `COHIVE_DINGTALK_CLIENT_ID`, `COHIVE_DINGTALK_CLIENT_SECRET` | Credentials of a DingTalk internal enterprise app. Set both to enable DingTalk (see [DingTalk integration](dingtalk.md), Chinese) |
| `COHIVE_DINGTALK_NOTICE_TEMPLATE`, `COHIVE_DINGTALK_APPROVAL_TEMPLATE` | Template IDs for DingTalk notification and approval cards; falls back to plain messages when unset |
| `CLOUDFLARE_TUNNEL_TOKEN` | Tunnel token, used with `--profile tunnel` |
| `COHIVE_BIND`, `COHIVE_PORT` | Listen address and port on the host. Default `127.0.0.1` and `8787` |
| `COHIVE_ALLOWED_HOSTS` | Extra host names allowed to reach MCP, besides the external URL (comma-separated) |
| `COHIVE_TRUSTED_PROXIES` | Which addresses may forward client address headers. Default: private ranges |
| `COHIVE_IMAGE`, `COHIVE_VERSION`, `COHIVE_COMMIT` | Image name, and the version and commit baked in at build time |

To run `cohive-server` directly on a machine without Docker, see [Run on a single host without Docker](single-host.en.md).
