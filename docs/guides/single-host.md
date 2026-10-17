# 不用 Docker 的单机运行

在一台机器上直接运行 `cohive-server`，用仓库里的脚本管理它。想省事的话用 [Docker 部署](deploy-docker.md)；本文适合想直接跑程序、或者在自己的 Mac 上长期放一台测试服的情况。

- `scripts/cohive-server.sh start|stop|restart|status|run`：启动、停止、查看状态
- `scripts/backup-db.sh`：备份数据库，保留最近 N 份
- `scripts/launchd/`：macOS 上协作服务、隧道常驻运行与定时备份的 launchd 模板（见第 5、6 节）

下文用 `/path/to/cohive` 代表仓库目录，`cohive.example.com` 代表对外域名。

## 1. 目录

全部放在一个固定目录里，缺省 `~/.local/share/cohive-server`，可用环境变量 `COHIVE_HOME` 改：

```
~/.local/share/cohive-server/
  server.env        非秘密配置：数据库地址、监听地址、允许的域名、备份参数（权限 600）
  data/             服务器数据目录（COHIVE_DATA_DIR），首次启动时初始管理员密码文件写在这里
  logs/server.log   服务输出（追加写）
  logs/tunnel.log   常驻隧道的输出（第 6 节）
  logs/backup.log   定时备份的输出
  run/server.pid    启动脚本记下的进程号
  cloudflared.yml   常驻隧道用的配置（第 6 节，从 scripts/launchd/cloudflared.yml 复制）
  backups/          数据库备份（COHIVE_BACKUP_DIR 可改）
```

不放在 macOS 的 `~/Library/Application Support`：那个路径带空格，手敲命令容易出错。

**数据库不在这个目录里**，由你自己准备一个 Postgres（17 或以上）。最简单的是用仓库里开发用的那个：`make db-up` 起一个 Docker 容器 `cohive-dev-postgres`（`localhost:55432`，数据卷 `cohive-dev-pg`），再在里面建一个专用的库。注意它和单元测试共用同一个容器：**不要执行 `docker compose down -v` 或删除这个数据卷**，否则数据一并没了。这也是要定时备份的原因。

## 2. 首次准备

1. 在仓库根目录构建：`make build`（看板、五个平台的命令行安装包、服务端，同一个版本号）。需要 Go 1.25、Node 20 以上与 pnpm。
2. 写配置：

   ```bash
   mkdir -p ~/.local/share/cohive-server && chmod 700 ~/.local/share/cohive-server
   cp scripts/server.env.example ~/.local/share/cohive-server/server.env
   chmod 600 ~/.local/share/cohive-server/server.env
   ```

   按实际修改 `COHIVE_DATABASE_URL`、`COHIVE_ADDR`。对外地址可以在网页“服务器管理 → 设置”里填，也可以写 `COHIVE_EXTERNAL_URL` 锁定；它的主机名自动加入 MCP 白名单，`COHIVE_ALLOWED_HOSTS` 只在还要用别的域名访问时才需要。**环境变量里已有的值优先于文件**：终端里残留的 `COHIVE_DATABASE_URL` 会让服务连到别的库，手动启动时可以用 `env -u COHIVE_DATABASE_URL scripts/cohive-server.sh start`。
3. Telegram 机器人令牌：用 `scripts/set-telegram-token.sh` 保存到 `~/.config/cohive/telegram.env`。启动脚本只在服务进程里加载它，不打印、不写日志、不进 `server.env`。要换文件用 `COHIVE_TELEGRAM_ENV` 指定。

**同一个机器人令牌同时只能有一个程序收消息。** 同一台机器上另起调试服务时，用 `COHIVE_TELEGRAM_ENV=/不存在的路径` 让它不加载令牌，否则两边会抢消息，审批按钮时灵时不灵。

首次启动后，初始管理员的登录名与密码在 `data/initial_admin_password`，见 [新成员接入](onboarding.md)。

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
- **装了常驻服务（第 6 节）后，`start`、`stop`、`restart` 改为操作 launchd 里的那个服务**，不再自己后台起进程，免得两套方式各起一份：`start` 装载或拉起，`restart` 即 `launchctl kickstart -k`，`stop` 从 launchd 卸下（下次登录会自动装回）。之前用 `start` 后台起的进程还在时，会先按进程号停掉它。`status` 会多一行“由 launchd 托管”。判断依据是 `~/Library/LaunchAgents/com.anormlylee.cohive.server.plist` 是否存在（标签可用 `COHIVE_LAUNCHD_LABEL` 改）。
- 交给 launchd 后，服务的环境只来自 plist 与 `server.env`，**不受执行命令的终端里残留的 `COHIVE_DATABASE_URL` 影响**；`status` 显示的库名仍按当前终端的环境算，有残留时以日志开头的“启动（库 …）”为准。
- Linux 上用 systemd 托管时，让服务执行 `scripts/cohive-server.sh run` 即可。

## 4. 备份与恢复

### 手动备份

```bash
scripts/backup-db.sh
```

写出 `backups/cohive-<库名>-YYYYmmdd-HHMMSS.dump`（`pg_dump` 自定义格式）。先写 `.partial`，用 `pg_restore --list` 校验能读后才改名；**只在本次成功后**删除超出 `COHIVE_BACKUP_KEEP`（缺省 14）份的旧备份，且只删同一个库、同一命名规则的文件。失败时退出码非零，旧备份一份不动。

**版本陷阱**：`pg_dump` 拒绝备份比它新的服务器（例如本机 Homebrew 装的是 15，数据库是 17）。两种解决办法（脚本检测到会提示）：

- 数据库跑在 Docker 容器里时，在 `server.env` 里设 `COHIVE_PG_CONTAINER=<容器名>`：脚本改用 `docker exec` 在容器里跑同版本的 `pg_dump` 与 `pg_restore`；
- 或安装同版本客户端，例如 `brew install postgresql@17`。

密码只经 `PGPASSWORD` 环境变量传给 `pg_dump`，不出现在命令参数里。

### 恢复（一律恢复到新库）

不要直接覆盖正在用的库。先恢复到新库，核对后再让服务改用它（下例用开发用的容器与库名 `cohive_prod`，按实际替换）：

```bash
docker exec cohive-dev-postgres createdb -U cohive cohive_restore
docker exec -i cohive-dev-postgres pg_restore -U cohive -d cohive_restore --no-owner \
  < ~/.local/share/cohive-server/backups/cohive-cohive_prod-20261017-033000.dump
psql 'postgres://cohive:cohive@localhost:55432/cohive_restore?sslmode=disable' \
  -c 'select count(*) from issues' -c 'select count(*) from accounts'
```

核对无误后：`scripts/cohive-server.sh stop`，把 `server.env` 里的 `COHIVE_DATABASE_URL` 改成新库，`scripts/cohive-server.sh start`。原库保留到确认不再需要。

## 5. 定时备份（macOS launchd）

模板在 `scripts/launchd/cohive-backup.plist.template`：每天 03:30 运行 `scripts/backup-db.sh`，输出写到 `logs/backup.log`。

```bash
repo=/path/to/cohive                     # 仓库绝对路径
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
- 数据库所在的 Docker 没有运行时备份会失败（`logs/backup.log` 里能看到），旧备份不受影响。
- Linux 上用 cron 或 systemd 定时器执行 `scripts/backup-db.sh` 即可。

## 6. 常驻服务（macOS launchd）

在终端里起的服务和隧道，终端一关、电脑一重启就断了。长期运行时把协作服务和隧道交给 launchd：

| 服务 | 标签 | 模板 | 日志 |
|---|---|---|---|
| 协作服务 | `com.anormlylee.cohive.server` | `scripts/launchd/cohive-server.plist.template` | `logs/server.log` |
| Cloudflare 隧道 | `com.anormlylee.cohive.tunnel` | `scripts/launchd/cohive-tunnel.plist.template` | `logs/tunnel.log` |

两个都是登录后启动（`RunAtLoad`），进程退出就重新拉起（`KeepAlive`，两次之间至少隔 10 秒）。

- 协作服务：前台运行 `scripts/cohive-server.sh run`。启动前用 `env -i` 清空环境，只留 `HOME`、`COHIVE_HOME`、`PATH`、`LANG`，其余配置一律读 `server.env`。机器人令牌照旧由脚本只在服务进程里加载，不进 plist、不进日志。数据库所在的 Docker 还没起来时服务会启动失败退出，launchd 每 10 秒重试，Docker 就绪后自动连上。
- 隧道：前台运行 `cloudflared tunnel --protocol http2 run --token-file ~/.config/cohive/cf-tunnel-token`，另加 `--config <数据根目录>/cloudflared.yml`：cloudflared 缺省会读 `~/.cloudflared/config.yml`，那里可能是本机别的项目的配置，不让它影响这条隧道。隧道是远程托管的，域名到 `http://127.0.0.1:8787` 的路由在 Cloudflare 控制台里配。令牌只放在令牌文件里（权限 600），不进 plist、不进日志。不用 Cloudflare 隧道时，只装协作服务那一个。

### 安装

```bash
repo=/path/to/cohive                     # 仓库绝对路径（plist 指向这里的脚本与 bin/）
ch="$HOME/.local/share/cohive-server"    # 数据根目录
la=~/Library/LaunchAgents
mkdir -p "$ch/logs" "$la"
cp "$repo/scripts/launchd/cloudflared.yml" "$ch/cloudflared.yml"
for s in server tunnel; do
  sed -e "s#__REPO__#$repo#g" -e "s#__HOME__#$HOME#g" -e "s#__COHIVE_HOME__#$ch#g" \
    "$repo/scripts/launchd/cohive-$s.plist.template" > "$la/com.anormlylee.cohive.$s.plist"
  plutil -lint "$la/com.anormlylee.cohive.$s.plist"
done
```

先装隧道，确认连上再切服务端。**同一条远程托管隧道允许多个连接器同时在线**，所以终端里原来跑着的隧道可以先不停，确认新的注册成功后再停：

```bash
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.anormlylee.cohive.tunnel.plist
grep 'Registered tunnel connection' ~/.local/share/cohive-server/logs/tunnel.log | tail -4   # 应有 4 条
curl -s -o /dev/null -w '%{http_code}\n' https://cohive.example.com/healthz                # 200
```

切服务端：先停掉终端里起的那份，再装载（中断一两秒）：

```bash
env -u COHIVE_DATABASE_URL scripts/cohive-server.sh stop
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.anormlylee.cohive.server.plist
curl -s http://127.0.0.1:8787/api/v1/version
grep 'Telegram 已启用' ~/.local/share/cohive-server/logs/server.log | tail -1
```

装好 plist 之后，`scripts/cohive-server.sh start` 也会自动先停后台进程再装载，效果相同。

### 日常操作

```bash
scripts/cohive-server.sh restart                                   # 换了新构建的程序后：即 kickstart -k
scripts/cohive-server.sh status
launchctl print gui/$(id -u)/com.anormlylee.cohive.server | grep -E 'state|pid =|runs|last exit'
launchctl kickstart -k gui/$(id -u)/com.anormlylee.cohive.tunnel   # 重启隧道
tail -f ~/.local/share/cohive-server/logs/server.log
tail -f ~/.local/share/cohive-server/logs/tunnel.log
```

- 不要用 `kill` 停服务：`KeepAlive` 会立即重新拉起。要停就 `scripts/cohive-server.sh stop`（或 `launchctl bootout`）。
- 日志是追加写，launchd 不轮换。日志太大时先 `scripts/cohive-server.sh stop`，挪走旧日志，再 `start`。
- 同一台机器上另起调试服务时别用同一个端口，也别加载同一个机器人令牌（见第 2 节）。
- 脚本里的变量名后紧跟中文时一律写成 `${名字}`：launchd 下语言环境是 `en_US.UTF-8`，macOS 自带的 bash 3.2 会把紧跟的多字节字符当成变量名的一部分，服务因此起不来（`scripts/scripts_test.go` 有检查）。

### 卸载（回到终端里手动运行）

```bash
launchctl bootout gui/$(id -u)/com.anormlylee.cohive.server
launchctl bootout gui/$(id -u)/com.anormlylee.cohive.tunnel
rm ~/Library/LaunchAgents/com.anormlylee.cohive.server.plist ~/Library/LaunchAgents/com.anormlylee.cohive.tunnel.plist
```

删掉 server 的 plist 后，`scripts/cohive-server.sh start|stop|restart` 回到后台进程方式。

### 关于“开机启动”与睡眠

- 这两个是**用户级服务**（LaunchAgent），在用户登录后才启动；重启电脑后停在登录界面时服务是断的。数据库所在的 Docker Desktop 本来也要登录后才运行，换成开机即起的系统级服务（LaunchDaemon，要管理员权限）并不能让服务更早可用。要做到重启后无人值守恢复，需要在“系统设置 → 用户与群组”里打开自动登录，并让 Docker Desktop 登录时启动；自动登录会降低这台电脑的安全性，自己权衡。
- **睡眠时服务不可用**：Mac 睡着后服务和隧道都不响应，唤醒后隧道会自动重连。要长期对同事开放时：
  - 首选：“系统设置 → 电池（或节能）→ 接通电源时防止在显示器关闭时自动进入睡眠”打开。只在插电时生效，显示器照常关闭，合盖仍会睡眠（笔记本合盖要外接显示器才不睡）。
  - 临时：`caffeinate -s` 在终端里运行期间阻止系统睡眠（`-s` 只在接电源时有效），终端关掉就失效，适合短时间演示。
  - `pmset -g` 可查看当前电源设置。
