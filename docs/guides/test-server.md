# 测试服：启动、备份与迁移

测试服是一台机器上直接运行的 `cohive-server`（目前在 lee 的 Mac 上，监听 `127.0.0.1:8787`，经 Cloudflare 隧道对外）。本文用仓库里的两个脚本管理它：

- `scripts/cohive-server.sh start|stop|restart|status|run`：启动、停止、查看状态
- `scripts/backup-db.sh`：备份数据库，保留最近 N 份

正式部署（Docker 镜像、compose）是后续里程碑 M6-b 的事，本文只管单机。

## 1. 目录

全部放在一个固定目录里，缺省 `~/.local/share/cohive-server`，可用环境变量 `COHIVE_HOME` 改：

```
~/.local/share/cohive-server/
  server.env        非秘密配置：数据库地址、监听地址、允许的域名、备份参数（权限 600）
  data/             服务器数据目录（COHIVE_DATA_DIR），首次启动时初始管理员密码文件写在这里
  logs/server.log   服务输出（追加写）
  logs/backup.log   定时备份的输出
  run/server.pid    启动脚本记下的进程号
  backups/          数据库备份（COHIVE_BACKUP_DIR 可改）
```

不放在 macOS 的 `~/Library/Application Support`：那个路径带空格，手敲命令容易出错。

**数据库不在这个目录里。** 测试服的库 `cohive_test` 在本机 Docker 的开发用 Postgres 里（容器 `cohive-dev-postgres`，数据卷 `cohive-dev-pg`）。重启电脑不会丢，但它和单元测试共用同一个容器：**不要执行 `docker compose down -v` 或删除这个数据卷**，否则测试服数据一并没了。这正是要定时备份的原因。

## 2. 首次准备

1. 在仓库根目录构建：`make build`（看板、五个平台的命令行安装包、服务端，同一个版本号）。
2. 写配置：

   ```bash
   mkdir -p ~/.local/share/cohive-server
   cp scripts/server.env.example ~/.local/share/cohive-server/server.env
   chmod 600 ~/.local/share/cohive-server/server.env
   ```

   按实际修改 `COHIVE_DATABASE_URL`、`COHIVE_ADDR`。对外地址可以在网页“服务器管理 → 设置”里填，也可以写 `COHIVE_EXTERNAL_URL` 锁定；它的主机名自动加入 MCP 白名单，`COHIVE_ALLOWED_HOSTS` 只在还要用别的域名访问时才需要。环境变量里已有的值优先于文件。

   **升级到第九号迁移（M6-d）时**：迁移会把编号最小的、能认出来的工作空间公网地址提升为服务器级对外地址，然后删掉工作空间上的这一列。启动后先在“服务器管理 → 设置”里核对一下（迁移日志里的 NOTICE 会说明丢弃或去掉路径的值）。这一步之后旧程序不能再连这个库，要回退只能用迁移前的备份。
3. Telegram 机器人令牌：用 `scripts/set-telegram-token.sh` 保存到 `~/.config/cohive/telegram.env`。启动脚本只在服务进程里加载它，不打印、不写日志、不进 `server.env`。要换文件用 `COHIVE_TELEGRAM_ENV` 指定。

**同一个机器人令牌同时只能有一个程序收消息。** 本机另起调试服务时，用 `COHIVE_TELEGRAM_ENV=/不存在的路径` 让它不加载令牌，否则会和测试服抢消息，审批按钮时灵时不灵。

## 3. 启动、停止、状态

```bash
scripts/cohive-server.sh start     # 后台启动，等健康检查通过（最多 15 秒）
scripts/cohive-server.sh status    # 是否在运行、版本、库名、数据目录、日志位置
scripts/cohive-server.sh stop      # 停止
scripts/cohive-server.sh restart   # 换了新构建的程序后用它
scripts/cohive-server.sh run       # 前台运行（交给 launchd、systemd 等托管时用）
```

- `start` 时如果端口已被别的程序占用，只报出是哪个进程，**不会结束它**。
- `stop` 只停进程号文件里记录、且确实是 `cohive-server` 的进程；进程号文件指向别的程序时拒绝动手。先发 `TERM`，10 秒没退出就提示，不会强杀。
- 启动服务时会自动执行数据库迁移；输出里有版本号与命令行安装包信息。
- 日志里不出现数据库密码与机器人令牌（脚本只打印库名）。

## 4. 备份与恢复

### 手动备份

```bash
scripts/backup-db.sh
```

写出 `backups/cohive-<库名>-YYYYmmdd-HHMMSS.dump`（`pg_dump` 自定义格式）。先写 `.partial`，用 `pg_restore --list` 校验能读后才改名；**只在本次成功后**删除超出 `COHIVE_BACKUP_KEEP`（缺省 14）份的旧备份，且只删同一个库、同一命名规则的文件。失败时退出码非零，旧备份一份不动。

**版本陷阱**：本机 Homebrew 的 `pg_dump` 是 15，开发库容器是 Postgres 17，`pg_dump` 拒绝备份更新版本的服务器。两种解决办法（脚本检测到会提示）：

- 在 `server.env` 里设 `COHIVE_PG_CONTAINER=cohive-dev-postgres`：脚本改用 `docker exec` 在容器里跑同版本的 `pg_dump` 与 `pg_restore`（示例配置已经这样写）；
- 或安装同版本客户端：`brew install postgresql@17`。

密码只经 `PGPASSWORD` 环境变量传给 `pg_dump`，不出现在命令参数里。

### 恢复（一律恢复到新库）

不要直接覆盖正在用的库。先恢复到新库，核对后再让服务改用它：

```bash
# 用容器里的同版本工具；文件换成要恢复的那份
docker exec cohive-dev-postgres createdb -U cohive cohive_test_restore
docker exec -i cohive-dev-postgres pg_restore -U cohive -d cohive_test_restore --no-owner \
  < ~/.local/share/cohive-server/backups/cohive-cohive_test-20261017-033000.dump
psql 'postgres://cohive:cohive@localhost:55432/cohive_test_restore?sslmode=disable' \
  -c 'select count(*) from issues' -c 'select count(*) from accounts'
```

核对无误后：`scripts/cohive-server.sh stop`，把 `server.env` 里的 `COHIVE_DATABASE_URL` 改成新库，`scripts/cohive-server.sh start`。原库保留到确认不再需要。

## 5. 定时备份（macOS launchd）

模板在 `scripts/launchd/cohive-backup.plist.template`：每天 03:30 运行 `scripts/backup-db.sh`，输出写到 `logs/backup.log`。

```bash
repo=/Users/jiali/MyCode/cohive          # 仓库绝对路径
ch="$HOME/.local/share/cohive-server"    # 数据根目录
mkdir -p "$ch/logs" ~/Library/LaunchAgents
sed -e "s#__REPO__#$repo#g" -e "s#__HOME__#$HOME#g" -e "s#__COHIVE_HOME__#$ch#g" \
  "$repo/scripts/launchd/cohive-backup.plist.template" \
  > ~/Library/LaunchAgents/com.anormlylee.cohive.backup.plist
plutil -lint ~/Library/LaunchAgents/com.anormlylee.cohive.backup.plist

launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.anormlylee.cohive.backup.plist
launchctl kickstart gui/$(id -u)/com.anormlylee.cohive.backup    # 立即试跑一次
tail ~/.local/share/cohive-server/logs/backup.log
```

- 撤销：`launchctl bootout gui/$(id -u)/com.anormlylee.cohive.backup`，再删掉 plist 文件。
- launchd 不读 shell 配置：模板里的 `PATH` 已含 `/usr/local/bin` 与 `/opt/homebrew/bin`（`docker`、`pg_dump` 通常在这里）；装在别处要改模板。
- 睡眠期间错过的定时任务会在唤醒后补跑一次；关机期间错过的不补。
- Docker Desktop 没有运行时备份会失败（`logs/backup.log` 里能看到），旧备份不受影响。

## 6. 从临时目录迁到新数据目录（测试服切换步骤）

2026-10-16 起测试服由 `…/scratchpad/test-restart.sh` 启动，脚本和数据目录都在 `/private/tmp` 下的临时目录里。下面把它切换到本文的脚本与固定目录。**切换由主脑执行**；全程不动数据库内容，隧道配置不变（仍指向 `127.0.0.1:8787`）。

### 旧数据目录里有什么、要搬什么

旧数据目录：`/private/tmp/claude-503/-Users-jiali-MyCode-cohive/f7d08f07-8081-4977-b6d1-3be664ac942f/scratchpad/test-data/`（下称 `$OLD`）。

| 文件 | 是什么 | 要不要搬 |
|---|---|---|
| `initial_admin_password` | 服务端首次启动生成的初始管理员密码；首登改密后自动删除 | 已不存在，不用管；如果还在说明初始管理员还没改密，要一起搬 |
| `admin-password-new.txt`、`member-password.txt` | 主脑手记的测试账号密码（服务端不读） | 建议搬，方便以后查；权限保持 600 |

服务端的全部状态（账号、问题单、令牌、群配置）都在数据库 `cohive_test` 里，**不在数据目录**；临时目录里的 `test-server.log` 是旧日志，可不搬。

### 步骤

前提：M6-a 已合入 main，并在 `/Users/jiali/MyCode/cohive` 执行过 `make build`（程序在 `bin/`）。以下命令都在这个目录执行。

1. **先备份一次**（用新脚本，环境变量直接给，不依赖配置文件）：

   ```bash
   COHIVE_HOME=$HOME/.local/share/cohive-server \
   COHIVE_DATABASE_URL='postgres://cohive:cohive@localhost:55432/cohive_test?sslmode=disable' \
   COHIVE_PG_CONTAINER=cohive-dev-postgres scripts/backup-db.sh
   ```

   确认输出里有 `已备份库 cohive_test`。

2. **写配置**：

   ```bash
   mkdir -p ~/.local/share/cohive-server && chmod 700 ~/.local/share/cohive-server
   printf '%s\n' \
     'COHIVE_DATABASE_URL=postgres://cohive:cohive@localhost:55432/cohive_test?sslmode=disable' \
     'COHIVE_ADDR=127.0.0.1:8787' \
     'COHIVE_ALLOWED_HOSTS=cohive-test.anormlylee.com' \
     'COHIVE_BACKUP_KEEP=14' \
     'COHIVE_PG_CONTAINER=cohive-dev-postgres' > ~/.local/share/cohive-server/server.env
   chmod 600 ~/.local/share/cohive-server/server.env
   ```

3. **搬数据目录里的文件**：

   ```bash
   OLD=/private/tmp/claude-503/-Users-jiali-MyCode-cohive/f7d08f07-8081-4977-b6d1-3be664ac942f/scratchpad/test-data
   mkdir -p ~/.local/share/cohive-server/data && chmod 700 ~/.local/share/cohive-server/data
   ls -la "$OLD"
   cp -p "$OLD"/* ~/.local/share/cohive-server/data/
   ls -la ~/.local/share/cohive-server/data
   ```

4. **停旧测试服**（旧脚本按端口找进程，新脚本没有它的进程号文件，所以这一步手工做；先核对再停）：

   ```bash
   lsof -nP -iTCP:8787 -sTCP:LISTEN          # 记下 PID
   ps -p <PID> -o command=                    # 必须是 …/bin/cohive-server serve --addr 127.0.0.1:8787
   kill <PID>                                 # 只停这一个
   ```

5. **用新脚本启动并核对**：

   ```bash
   scripts/cohive-server.sh start
   scripts/cohive-server.sh status
   curl -s https://cohive-test.anormlylee.com/healthz
   curl -s https://cohive-test.anormlylee.com/api/v1/version
   ```

   日志 `~/.local/share/cohive-server/logs/server.log` 里应有“群通知：Telegram 已启用”（说明令牌已加载）和命令行安装包信息。网页用原账号登录一次确认数据都在；机器人是否在收消息，可以（可选）在群里发 `/chatid` 看它是否回复。

6. **装定时备份**：按第 5 节执行，`kickstart` 试跑一次并看 `logs/backup.log`。

7. **收尾**：台账里的重启命令改为 `scripts/cohive-server.sh restart`。旧临时目录确认新服务正常后不再使用，是否删除由主脑决定。

### 回退

新脚本启动不起来时：`scripts/cohive-server.sh stop`（若已启动），再执行旧的 `…/scratchpad/test-restart.sh /Users/jiali/MyCode/cohive`。数据库没有被改动，两边用的是同一个 `cohive_test`。
