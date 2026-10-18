# Run on a single host without Docker

[中文](single-host.md)

Run `cohive-server` directly on one machine and manage it with the scripts in the repository. If you want the easy route, use [Docker deployment](deploy-docker.en.md); this guide is for running the program directly, or for keeping a long-lived test server on your own Mac.

- `scripts/cohive-server.sh start|stop|restart|status|run`: start, stop and inspect the server
- `scripts/backup-db.sh`: back up the database and keep the latest N copies
- `scripts/launchd/`: launchd templates for running the server and tunnel as persistent services on macOS, and for scheduled backups (see sections 5 and 6)

Below, `/path/to/cohive` stands for the repository directory and `cohive.example.com` for the public domain name.

## 1. Directory layout

Everything lives in one fixed directory, `~/.local/share/cohive-server` by default; change it with the environment variable `COHIVE_HOME`:

```
~/.local/share/cohive-server/
  server.env        non-secret config: database URL, listen address, allowed hosts, backup settings (permissions 600)
  data/             server data directory (COHIVE_DATA_DIR); the initial admin password file is written here on first start
  logs/server.log   server output (append-only)
  logs/tunnel.log   output of the persistent tunnel (section 6)
  logs/backup.log   output of scheduled backups
  run/server.pid    process ID recorded by the start script
  cloudflared.yml   config for the persistent tunnel (section 6, copied from scripts/launchd/cloudflared.yml)
  backups/          database backups (change with COHIVE_BACKUP_DIR)
```

It is not placed in macOS's `~/Library/Application Support` because that path contains a space, which makes typed commands error-prone.

**The database is not in this directory.** You provide a Postgres (17 or later) yourself. The easiest way is the development one from the repository: `make db-up` starts a Docker container `cohive-dev-postgres` (`localhost:55432`, volume `cohive-dev-pg`), and you create a dedicated database in it. Note that it shares the container with the unit tests: **do not run `docker compose down -v` or delete that volume**, or the data goes with it. This is also why you should schedule backups.

## 2. First-time setup

1. Build in the repository root: `make build` (web UI, CLI installers for five platforms and the server, all with the same version). You need Go 1.26, Node 20 or later, and pnpm.
2. Write the config:

   ```bash
   mkdir -p ~/.local/share/cohive-server && chmod 700 ~/.local/share/cohive-server
   cp scripts/server.env.example ~/.local/share/cohive-server/server.env
   chmod 600 ~/.local/share/cohive-server/server.env
   ```

   Edit `COHIVE_DATABASE_URL` and `COHIVE_ADDR` to match your setup. You can set the external URL in the web UI under Server admin → Settings, or lock it with `COHIVE_EXTERNAL_URL`; its host name is added to the MCP allowlist automatically, and `COHIVE_ALLOWED_HOSTS` is only needed if you also reach the server through other domains. The server's default language is set with `COHIVE_DEFAULT_LANGUAGE` (`en` or `zh-CN`; English when unset, so set it to `zh-CN` if your team mainly uses Chinese), and a member's CLI language can be overridden with `COHIVE_LANG`. **Values already in the environment take precedence over the file**: a stray `COHIVE_DATABASE_URL` left in your terminal makes the server connect to a different database. When starting by hand you can use `env -u COHIVE_DATABASE_URL scripts/cohive-server.sh start`.
3. Telegram bot token: save it with `scripts/set-telegram-token.sh` to `~/.config/cohive/telegram.env`. The start script loads it only inside the server process; it is never printed, logged or written to `server.env`. To use a different file, set `COHIVE_TELEGRAM_ENV`.
   DingTalk app credentials work the same way: save them with `scripts/set-dingtalk-credentials.sh` to `~/.config/cohive/dingtalk.env` (use `COHIVE_DINGTALK_ENV` for a different file); see [DingTalk integration](dingtalk.md) (Chinese).

**Only one program at a time can receive messages for a given bot token.** When you start a separate debug server on the same machine, set `COHIVE_TELEGRAM_ENV=/nonexistent/path` so it doesn't load the token. Otherwise the two compete for messages and approval buttons work only some of the time.

After the first start, the initial admin's username and password are in `data/initial_admin_password`; see [Onboarding new members](onboarding.en.md).

## 3. Start, stop, status

```bash
scripts/cohive-server.sh start     # start in the background and wait for the health check (up to 15 seconds)
scripts/cohive-server.sh status    # whether it's running, version, database name, data directory, log location
scripts/cohive-server.sh stop      # stop
scripts/cohive-server.sh restart   # use after replacing the binary with a new build
scripts/cohive-server.sh run       # run in the foreground (for launchd, systemd and similar)
```

- If the port is already taken by another program, `start` only reports which process holds it; it **does not kill it**.
- `stop` stops only the process recorded in the PID file, and only if it really is `cohive-server`; it refuses to act if the PID file points at another program. It sends `TERM` first, and if the process hasn't exited after 10 seconds it tells you instead of force-killing it.
- The server runs database migrations automatically on start; the output includes the version and CLI installer information.
- Logs never contain the database password or the bot token (the script prints only the database name).
- **Once the persistent service is installed (section 6), `start`, `stop` and `restart` operate on the launchd service** instead of starting a background process themselves, so you don't end up with two copies. `start` loads or kickstarts it, `restart` is `launchctl kickstart -k`, and `stop` unloads it from launchd (it is loaded again at the next sign-in). If a process previously started in the background with `start` is still running, it is stopped by PID first. `status` adds a line saying it is managed by launchd. The script decides this by whether `~/Library/LaunchAgents/com.anormlylee.cohive.server.plist` exists (change the label with `COHIVE_LAUNCHD_LABEL`).
- Under launchd, the service's environment comes only from the plist and `server.env`, **so a stray `COHIVE_DATABASE_URL` in the terminal you run commands from has no effect**. The database name shown by `status` is still computed from the current terminal's environment; if there is a leftover value, trust the startup banner line at the top of the log, which shows the database name.
- On Linux with systemd, have the unit run `scripts/cohive-server.sh run`.

## 4. Backup and restore

### Manual backup

```bash
scripts/backup-db.sh
```

This writes `backups/cohive-<dbname>-YYYYmmdd-HHMMSS.dump` (`pg_dump` custom format). It first writes a `.partial` file, and renames it only after `pg_restore --list` confirms it can be read. **Only after this run succeeds** does it delete backups beyond `COHIVE_BACKUP_KEEP` (default 14), and only files of the same database and the same naming pattern. On failure it exits non-zero and leaves old backups untouched.

**Version trap**: `pg_dump` refuses to back up a server that is newer than itself (for example Homebrew installed 15 locally but the database is 17). There are two ways around it (the script detects this and tells you):

- If the database runs in a Docker container, set `COHIVE_PG_CONTAINER=<container name>` in `server.env`: the script then uses `docker exec` to run the matching `pg_dump` and `pg_restore` inside the container;
- or install a client of the same version, for example `brew install postgresql@17`.

The password reaches `pg_dump` only through the `PGPASSWORD` environment variable and never appears in command arguments.

### Restore (always into a new database)

Never overwrite the database in use. Restore into a new database, check it, and then point the server at it (the example uses the development container and the database name `cohive_prod`; adjust to your setup):

```bash
docker exec cohive-dev-postgres createdb -U cohive cohive_restore
docker exec -i cohive-dev-postgres pg_restore -U cohive -d cohive_restore --no-owner \
  < ~/.local/share/cohive-server/backups/cohive-cohive_prod-20261017-033000.dump
psql 'postgres://cohive:cohive@localhost:55432/cohive_restore?sslmode=disable' \
  -c 'select count(*) from issues' -c 'select count(*) from accounts'
```

Once it checks out: run `scripts/cohive-server.sh stop`, change `COHIVE_DATABASE_URL` in `server.env` to the new database, and run `scripts/cohive-server.sh start`. Keep the original database until you are sure you no longer need it.

## 5. Scheduled backups (macOS launchd)

The template is `scripts/launchd/cohive-backup.plist.template`: it runs `scripts/backup-db.sh` every day at 03:30 and writes output to `logs/backup.log`.

```bash
repo=/path/to/cohive                     # absolute path of the repository
ch="$HOME/.local/share/cohive-server"    # data root directory
mkdir -p "$ch/logs" ~/Library/LaunchAgents
sed -e "s#__REPO__#$repo#g" -e "s#__HOME__#$HOME#g" -e "s#__COHIVE_HOME__#$ch#g" \
  "$repo/scripts/launchd/cohive-backup.plist.template" \
  > ~/Library/LaunchAgents/com.anormlylee.cohive.backup.plist
plutil -lint ~/Library/LaunchAgents/com.anormlylee.cohive.backup.plist

launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.anormlylee.cohive.backup.plist
launchctl kickstart gui/$(id -u)/com.anormlylee.cohive.backup    # run once now as a test
tail ~/.local/share/cohive-server/logs/backup.log
```

- To undo: `launchctl bootout gui/$(id -u)/com.anormlylee.cohive.backup`, then delete the plist file.
- launchd does not read shell config: the `PATH` in the template already includes `/usr/local/bin` and `/opt/homebrew/bin` (where `docker` and `pg_dump` usually are); edit the template if yours are elsewhere.
- A job missed while the Mac was asleep runs once after wake-up; one missed while it was powered off is not made up.
- If the Docker that hosts the database isn't running, the backup fails (visible in `logs/backup.log`); existing backups are not affected.
- On Linux, run `scripts/backup-db.sh` from cron or a systemd timer.

## 6. Persistent services (macOS launchd)

A server and tunnel started in a terminal stop when you close the terminal or restart the computer. For long-term use, hand the server and the tunnel to launchd:

| Service | Label | Template | Log |
|---|---|---|---|
| Cohive server | `com.anormlylee.cohive.server` | `scripts/launchd/cohive-server.plist.template` | `logs/server.log` |
| Cloudflare Tunnel | `com.anormlylee.cohive.tunnel` | `scripts/launchd/cohive-tunnel.plist.template` | `logs/tunnel.log` |

Both start at sign-in (`RunAtLoad`) and are restarted when the process exits (`KeepAlive`, at least 10 seconds apart).

- Server: runs `scripts/cohive-server.sh run` in the foreground. Before starting it clears the environment with `env -i`, leaving only `HOME`, `COHIVE_HOME`, `PATH` and `LANG`; all other settings are read from `server.env`. As before, the script loads the bot token only inside the server process, so it never appears in the plist or in logs. If the Docker that hosts the database isn't up yet, the server fails to start and exits; launchd retries every 10 seconds and it connects once Docker is ready.
- Tunnel: runs `cloudflared tunnel --protocol http2 run --token-file ~/.config/cohive/cf-tunnel-token` in the foreground, plus `--config <data root>/cloudflared.yml`. By default cloudflared reads `~/.cloudflared/config.yml`, which may belong to another project on this machine, and this keeps it from affecting this tunnel. The tunnel is remotely managed: the route from your domain to `http://127.0.0.1:8787` is configured in the Cloudflare dashboard. The token lives only in the token file (permissions 600), never in the plist or logs. If you don't use a Cloudflare Tunnel, install only the server service.

### Install

```bash
repo=/path/to/cohive                     # absolute path of the repository (the plist points at scripts and bin/ here)
ch="$HOME/.local/share/cohive-server"    # data root directory
la=~/Library/LaunchAgents
mkdir -p "$ch/logs" "$la"
cp "$repo/scripts/launchd/cloudflared.yml" "$ch/cloudflared.yml"
for s in server tunnel; do
  sed -e "s#__REPO__#$repo#g" -e "s#__HOME__#$HOME#g" -e "s#__COHIVE_HOME__#$ch#g" \
    "$repo/scripts/launchd/cohive-$s.plist.template" > "$la/com.anormlylee.cohive.$s.plist"
  plutil -lint "$la/com.anormlylee.cohive.$s.plist"
done
```

Install the tunnel first, confirm it connects, and only then switch the server. **A remotely managed tunnel allows several connectors online at the same time**, so you can leave the tunnel you were running in a terminal alone and stop it only after the new one has registered:

```bash
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.anormlylee.cohive.tunnel.plist
grep 'Registered tunnel connection' ~/.local/share/cohive-server/logs/tunnel.log | tail -4   # should show 4 lines
curl -s -o /dev/null -w '%{http_code}\n' https://cohive.example.com/healthz                # 200
```

To switch the server, stop the copy running in your terminal and then load the service (a one or two second interruption):

```bash
env -u COHIVE_DATABASE_URL scripts/cohive-server.sh stop
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.anormlylee.cohive.server.plist
curl -s http://127.0.0.1:8787/api/v1/version
grep 'Telegram enabled' ~/.local/share/cohive-server/logs/server.log | tail -1
```

Once the plist is installed, `scripts/cohive-server.sh start` also stops the background process first and then loads the service, with the same effect.

### Day-to-day

```bash
scripts/cohive-server.sh restart                                   # after replacing the binary with a new build: same as kickstart -k
scripts/cohive-server.sh status
launchctl print gui/$(id -u)/com.anormlylee.cohive.server | grep -E 'state|pid =|runs|last exit'
launchctl kickstart -k gui/$(id -u)/com.anormlylee.cohive.tunnel   # restart the tunnel
tail -f ~/.local/share/cohive-server/logs/server.log
tail -f ~/.local/share/cohive-server/logs/tunnel.log
```

- Don't stop the service with `kill`: `KeepAlive` restarts it immediately. To stop it, use `scripts/cohive-server.sh stop` (or `launchctl bootout`).
- Logs are append-only and launchd does not rotate them. If a log gets too large, run `scripts/cohive-server.sh stop`, move the old log away, then `start`.
- When you start a separate debug server on the same machine, don't use the same port or load the same bot token (see section 2).
- In scripts, when a variable name is directly followed by Chinese text, always write it as `${name}`: under launchd the locale is `en_US.UTF-8`, and the bash 3.2 shipped with macOS treats the following multibyte characters as part of the variable name, which stops the service from starting (`scripts/scripts_test.go` checks this).

### Uninstall (go back to running by hand in a terminal)

```bash
launchctl bootout gui/$(id -u)/com.anormlylee.cohive.server
launchctl bootout gui/$(id -u)/com.anormlylee.cohive.tunnel
rm ~/Library/LaunchAgents/com.anormlylee.cohive.server.plist ~/Library/LaunchAgents/com.anormlylee.cohive.tunnel.plist
```

After the server's plist is deleted, `scripts/cohive-server.sh start|stop|restart` go back to the background-process mode.

### About "start at boot" and sleep

- These are **user-level services** (LaunchAgents) that start after a user signs in; if the computer restarts and sits at the login screen, the service is down. Docker Desktop, which hosts the database, also runs only after sign-in, so switching to a system-level service that starts at boot (a LaunchDaemon, which needs admin rights) would not make the service available any sooner. To recover unattended after a restart, turn on automatic login under System Settings → Users & Groups and make Docker Desktop start at login. Automatic login lowers the security of the machine, so weigh that yourself.
- **The service is unavailable while the Mac sleeps**: both the server and the tunnel stop responding when it sleeps, and the tunnel reconnects after wake-up. To keep it open to teammates over the long term:
  - Preferred: in System Settings → Battery (or Energy Saver), turn on "Prevent automatic sleeping on power adapter when the display is off". It only applies on power, the display still turns off, and closing the lid still sleeps (a laptop needs an external display to stay awake with the lid closed).
  - Temporary: `caffeinate -s` prevents system sleep while it runs in a terminal (`-s` only works on power) and stops working when the terminal closes, so it suits short demos.
  - `pmset -g` shows the current power settings.
