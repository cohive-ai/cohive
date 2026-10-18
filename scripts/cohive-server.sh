#!/usr/bin/env bash
# 单机运行协作服务（不用 Docker 时）：启动、停止、查看状态。
#
# 用法：scripts/cohive-server.sh start|stop|restart|status|run
#   start    后台启动，写进程号文件，等健康检查通过
#   stop     只停进程号文件里记录、且确实是 cohive-server 的进程
#   restart  stop 后 start
#   status   是否在运行、版本、数据目录与日志位置
#   run      前台运行（交给 launchd、systemd 等托管时用）
# 装了 launchd 常驻服务（~/Library/LaunchAgents/com.anormlylee.cohive.server.plist）后，
#   start/stop/restart 改用 launchctl 操作那个服务，不再自己后台拉起，免得同时跑两份：
#   start 装载或拉起，stop 从 launchd 卸下（下次登录自动装回），restart 即 kickstart -k。
#
# 目录（可用环境变量覆盖）：
#   COHIVE_HOME      缺省 ~/.local/share/cohive-server
#     server.env     非秘密配置，见 scripts/server.env.example
#     data/          COHIVE_DATA_DIR，初始管理员密码文件等
#     logs/          服务输出 server.log
#     run/           进程号文件 server.pid
# 机器人令牌：沿用 ~/.config/cohive/telegram.env（scripts/set-telegram-token.sh 写入），
#   只在服务进程里加载，不打印、不写日志、不进 server.env；可用 COHIVE_TELEGRAM_ENV 指定别的文件。
# 钉钉应用凭据：同样处理 ~/.config/cohive/dingtalk.env（scripts/set-dingtalk-credentials.sh 写入），
#   可用 COHIVE_DINGTALK_ENV 指定别的文件。
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=lib/cohive-env.sh
. "$root/scripts/lib/cohive-env.sh"
cohive_load_env

bin="${COHIVE_BIN:-$root/bin/cohive-server}"
addr="${COHIVE_ADDR:-127.0.0.1:8787}"
export COHIVE_DATA_DIR="${COHIVE_DATA_DIR:-$COHIVE_HOME/data}"
log_dir="${COHIVE_LOG_DIR:-$COHIVE_HOME/logs}"
run_dir="${COHIVE_RUN_DIR:-$COHIVE_HOME/run}"
pid_file="$run_dir/server.pid"
log_file="$log_dir/server.log"
tg_env="${COHIVE_TELEGRAM_ENV:-$HOME/.config/cohive/telegram.env}"
dt_env="${COHIVE_DINGTALK_ENV:-$HOME/.config/cohive/dingtalk.env}"

label="${COHIVE_LAUNCHD_LABEL:-com.anormlylee.cohive.server}"
plist="${COHIVE_LAUNCHD_PLIST:-$HOME/Library/LaunchAgents/$label.plist}"
domain="gui/$(id -u)"

# fail "中文" "English"：按语言输出一句错误并退出
fail() { echo "$(cohive_t "错误：$1" "Error: $2")" >&2; exit 1; }

# 用法：未给或给错子命令时打印
usage() {
  if [ "$COHIVE_SCRIPT_LANG" = zh ]; then
    cat <<'EOF_USAGE_ZH'
单机运行协作服务（不用 Docker 时）：启动、停止、查看状态。

用法：scripts/cohive-server.sh start|stop|restart|status|run
  start    后台启动，写进程号文件，等健康检查通过
  stop     只停进程号文件里记录、且确实是 cohive-server 的进程
  restart  stop 后 start
  status   是否在运行、版本、数据目录与日志位置
  run      前台运行（交给 launchd、systemd 等托管时用）
装了 launchd 常驻服务（~/Library/LaunchAgents/com.anormlylee.cohive.server.plist）后，
  start/stop/restart 改用 launchctl 操作那个服务，不再自己后台拉起，免得同时跑两份：
  start 装载或拉起，stop 从 launchd 卸下（下次登录自动装回），restart 即 kickstart -k。

目录（可用环境变量覆盖）：
  COHIVE_HOME      缺省 ~/.local/share/cohive-server
    server.env     非秘密配置，见 scripts/server.env.example
    data/          COHIVE_DATA_DIR，初始管理员密码文件等
    logs/          服务输出 server.log
    run/           进程号文件 server.pid
机器人令牌：沿用 ~/.config/cohive/telegram.env（scripts/set-telegram-token.sh 写入），
  只在服务进程里加载，不打印、不写日志、不进 server.env；可用 COHIVE_TELEGRAM_ENV 指定别的文件。
钉钉应用凭据：同样处理 ~/.config/cohive/dingtalk.env（scripts/set-dingtalk-credentials.sh 写入），
  可用 COHIVE_DINGTALK_ENV 指定别的文件。
本脚本提示语的语言：COHIVE_LANG，其次 LC_ALL、LC_MESSAGES、LANG（zh 开头为中文，其余为英文）。
EOF_USAGE_ZH
  else
    cat <<'EOF_USAGE_EN'
Run the Cohive server on a single machine (without Docker): start, stop, check status.

Usage: scripts/cohive-server.sh start|stop|restart|status|run
  start    Start in the background, write the pid file, wait for the health check
  stop     Stop only the process recorded in the pid file, and only if it really is cohive-server
  restart  stop, then start
  status   Whether it is running, version, data directory and log location
  run      Run in the foreground (for launchd, systemd and the like)
Once the launchd service (~/Library/LaunchAgents/com.anormlylee.cohive.server.plist) is installed,
  start/stop/restart operate that service through launchctl instead of starting a background process,
  so two copies never run at once: start loads or kicks it, stop unloads it from launchd
  (it comes back at next login), restart is kickstart -k.

Directories (override with environment variables):
  COHIVE_HOME      default ~/.local/share/cohive-server
    server.env     non-secret settings, see scripts/server.env.example
    data/          COHIVE_DATA_DIR, initial admin password file and so on
    logs/          server output, server.log
    run/           pid file, server.pid
Telegram bot token: read from ~/.config/cohive/telegram.env (written by scripts/set-telegram-token.sh),
  loaded only inside the server process; never printed, logged or put in server.env; set COHIVE_TELEGRAM_ENV to use another file.
DingTalk app credentials: handled the same way, from ~/.config/cohive/dingtalk.env (written by scripts/set-dingtalk-credentials.sh);
  set COHIVE_DINGTALK_ENV to use another file.
Language of these messages: COHIVE_LANG, otherwise LC_ALL, LC_MESSAGES, LANG (zh... means Chinese, anything else English).
EOF_USAGE_EN
  fi
}

# 装了 launchd 常驻服务就交给它管：以 plist 文件是否存在为准（launchd 只在 macOS 上有）
launchd_managed() { [ -f "$plist" ] && command -v launchctl >/dev/null 2>&1; }
launchd_loaded() { launchctl print "$domain/$label" >/dev/null 2>&1; }
# launchd_pid 输出 launchd 记录的服务进程号；没装载或没有进程时输出空
launchd_pid() {
  local out
  out="$(launchctl print "$domain/$label" 2>/dev/null)" || return 0
  printf '%s\n' "$out" | awk '$1 == "pid" && $2 == "=" && !n++ { print $3 }'
}

# 健康检查用的地址：“:8787”这类只写端口的监听地址按本机访问
probe="$addr"
case "$probe" in :*) probe="127.0.0.1$probe" ;; 0.0.0.0:*) probe="127.0.0.1:${probe#0.0.0.0:}" ;; esac
port="${addr##*:}"

need_config() {
  [ -n "${COHIVE_DATABASE_URL:-}" ] || fail "没有设置 COHIVE_DATABASE_URL：写进 ${COHIVE_SERVER_ENV}（参照 scripts/server.env.example）或在环境变量里设置" \
    "COHIVE_DATABASE_URL is not set: put it in ${COHIVE_SERVER_ENV} (see scripts/server.env.example) or set it in the environment"
  [ -x "$bin" ] || fail "找不到服务端程序 ${bin}：先在仓库根目录执行 make build，或用 COHIVE_BIN 指定" \
    "server binary ${bin} not found: run make build in the repository root, or point COHIVE_BIN at it"
}

# running 判断进程号文件里的进程是否在运行且确是 cohive-server；是则输出进程号。
running() {
  local pid comm
  [ -f "$pid_file" ] || return 1
  pid="$(tr -cd '0-9' < "$pid_file")"
  [ -n "$pid" ] || return 1
  comm="$(ps -p "$pid" -o comm= 2>/dev/null || true)"
  case "$comm" in
    *cohive-server*) printf '%s' "$pid" ;;
    "") return 1 ;;
    *) echo "$(cohive_t "进程号文件 ${pid_file} 记录的 ${pid} 是别的程序（${comm}），不会动它" \
         "The pid file ${pid_file} records ${pid}, which is another program (${comm}); leaving it alone")" >&2; return 2 ;;
  esac
}

# 只在子进程里加载机器人令牌，然后执行服务端；令牌不经过本脚本的任何输出
exec_server() {
  if [ -f "$tg_env" ]; then
    # shellcheck disable=SC1090
    . "$tg_env"
    export COHIVE_TELEGRAM_BOT_TOKEN
  fi
  if [ -f "$dt_env" ]; then
    # shellcheck disable=SC1090
    . "$dt_env"
    export COHIVE_DINGTALK_CLIENT_ID COHIVE_DINGTALK_CLIENT_SECRET COHIVE_DINGTALK_NOTICE_TEMPLATE COHIVE_DINGTALK_APPROVAL_TEMPLATE
  fi
  exec "$bin" serve --addr "$addr"
}

port_owner() {
  command -v lsof >/dev/null 2>&1 || return 1
  local p
  p="$(lsof -nP -iTCP:"$port" -sTCP:LISTEN -t 2>/dev/null | head -1)"
  [ -n "$p" ] || return 1
  printf '%s (%s)' "$p" "$(ps -p "$p" -o comm= 2>/dev/null || cohive_t 未知 unknown)"
}

# wait_healthy 等健康检查通过（最多 15 秒）；进程号变成 old 以外的值才算新进程起来了
wait_healthy() {
  local old="${1:-}" pid
  for _ in $(seq 1 30); do
    pid="$(launchd_pid)"
    if [ -n "$pid" ] && [ "$pid" != "$old" ] && curl -fsS "http://$probe/healthz" >/dev/null 2>&1; then
      echo "$(cohive_t "已由 launchd 启动，进程号 ${pid}，监听 ${addr}" "Started by launchd, pid ${pid}, listening on ${addr}")"
      echo "  $(cohive_t "日志：${log_file}" "Log: ${log_file}")"
      return 0
    fi
    sleep 0.5
  done
  echo "$(cohive_t "15 秒内健康检查没有通过，launchd 会每 10 秒重试；日志最后 20 行：" \
    "The health check did not pass within 15 seconds; launchd retries every 10 seconds. Last 20 log lines:")" >&2
  tail -n 20 "$log_file" >&2
  exit 1
}

launchd_start() {
  if launchd_loaded; then
    launchctl kickstart "$domain/$label" >/dev/null
  else
    launchctl bootstrap "$domain" "$plist"
  fi
  wait_healthy
}

launchd_stop() {
  if ! launchd_loaded; then
    echo "$(cohive_t "没有在运行（launchd 服务 ${label} 未装载）" "Not running (launchd service ${label} is not loaded)")"
    return 0
  fi
  launchctl bootout "$domain/$label"
  rm -f "$pid_file"
  echo "$(cohive_t "已停止并从 launchd 卸下 ${label}；下次登录会自动装回，要现在恢复执行 $0 start" \
    "Stopped and unloaded ${label} from launchd; it comes back at next login, run $0 start to restore it now")"
}

launchd_restart() {
  launchd_loaded || { launchd_start; return; }
  local old
  old="$(launchd_pid)"
  launchctl kickstart -k "$domain/$label"
  wait_healthy "$old"
}

start() {
  need_config
  local pid rc=0
  pid="$(running)" || rc=$?
  if [ "$rc" -eq 0 ]; then
    echo "$(cohive_t "已在运行，进程号 ${pid}（${addr}）" "Already running, pid ${pid} (${addr})")"
    return 0
  fi
  [ "$rc" -eq 2 ] && fail "进程号文件指向别的程序，请确认后删除 ${pid_file} 再启动" \
    "the pid file points at another program; check it, then delete ${pid_file} and start again"
  if owner="$(port_owner)"; then
    fail "端口 ${port} 已被进程 ${owner} 占用，不会自动结束它；请先确认是什么程序" \
      "port ${port} is already in use by process ${owner}; it will not be killed automatically, check what it is first"
  fi
  mkdir -p "$COHIVE_DATA_DIR" "$log_dir" "$run_dir"
  chmod 700 "$COHIVE_HOME" "$COHIVE_DATA_DIR" "$run_dir" 2>/dev/null || true
  {
    echo
    echo "==== $(date '+%Y-%m-%d %H:%M:%S') $(cohive_t "启动（库 $(cohive_db_name "$COHIVE_DATABASE_URL")，监听 ${addr}）" \
      "starting (database $(cohive_db_name "$COHIVE_DATABASE_URL"), listening on ${addr})") ===="
  } >> "$log_file"
  ( exec_server ) >> "$log_file" 2>&1 < /dev/null &
  pid=$!
  echo "$pid" > "$pid_file"
  for _ in $(seq 1 30); do
    if curl -fsS "http://$probe/healthz" >/dev/null 2>&1; then
      echo "$(cohive_t "已启动，进程号 ${pid}，监听 ${addr}" "Started, pid ${pid}, listening on ${addr}")"
      echo "  $(cohive_t "数据目录：${COHIVE_DATA_DIR}" "Data directory: ${COHIVE_DATA_DIR}")"
      echo "  $(cohive_t "日志：${log_file}" "Log: ${log_file}")"
      return 0
    fi
    if ! kill -0 "$pid" 2>/dev/null; then
      rm -f "$pid_file"
      echo "$(cohive_t "启动失败，日志最后 20 行：" "Failed to start. Last 20 log lines:")" >&2
      tail -n 20 "$log_file" >&2
      exit 1
    fi
    sleep 0.5
  done
  echo "$(cohive_t "15 秒内健康检查没有通过（进程 ${pid} 仍在运行），日志最后 20 行：" \
    "The health check did not pass within 15 seconds (process ${pid} is still running). Last 20 log lines:")" >&2
  tail -n 20 "$log_file" >&2
  exit 1
}

stop() {
  local pid rc=0
  pid="$(running)" || rc=$?
  if [ "$rc" -eq 2 ]; then
    exit 1
  elif [ "$rc" -ne 0 ]; then
    rm -f "$pid_file"
    echo "$(cohive_t "没有在运行" "Not running")"
    return 0
  fi
  kill -TERM "$pid"
  for _ in $(seq 1 20); do
    if ! kill -0 "$pid" 2>/dev/null; then
      rm -f "$pid_file"
      echo "$(cohive_t "已停止（进程号 ${pid}）" "Stopped (pid ${pid})")"
      return 0
    fi
    sleep 0.5
  done
  fail "进程 ${pid} 10 秒内没有退出；不会强制结束，请查看日志 ${log_file} 后自行处理" \
    "process ${pid} did not exit within 10 seconds; it will not be force-killed, check the log ${log_file} and deal with it yourself"
}

status() {
  local pid rc=0
  pid="$(running)" || rc=$?
  if launchd_managed; then
    if launchd_loaded; then
      # 以 launchd 记录的进程号为准（进程号文件可能是旧版脚本留下的或缺失）
      pid="$(launchd_pid)"
      if [ -n "$pid" ]; then
        rc=0
        echo "$(cohive_t "由 launchd 托管：${label}" "Managed by launchd: ${label}")"
      else
        rc=1
        echo "$(cohive_t "由 launchd 托管：${label}，当前没有进程（launchd 会每 10 秒重试，看日志 ${log_file}）" \
          "Managed by launchd: ${label}, no process right now (launchd retries every 10 seconds, see the log ${log_file})")"
      fi
    else
      echo "$(cohive_t "装了 launchd 服务 ${label} 但未装载（执行 $0 start 装载）" "launchd service ${label} is installed but not loaded (run $0 start to load it)")"
    fi
  fi
  if [ "$rc" -eq 0 ]; then
    echo "$(cohive_t "运行中，进程号 ${pid}，监听 ${addr}" "Running, pid ${pid}, listening on ${addr}")"
    local v
    v="$(curl -fsS "http://$probe/api/v1/version" 2>/dev/null | sed -nE 's/.*"version":"([^"]*)"[^"]*$/\1/p' || true)"
    [ -n "$v" ] && echo "  $(cohive_t "版本：${v}" "Version: ${v}")"
  else
    echo "$(cohive_t "没有在运行" "Not running")"
  fi
  echo "  $(cohive_t "数据库：" "Database: ")$(cohive_db_name "${COHIVE_DATABASE_URL:-$(cohive_t "（未设置）" "(not set)")}")"
  echo "  $(cohive_t "数据目录：${COHIVE_DATA_DIR}" "Data directory: ${COHIVE_DATA_DIR}")"
  echo "  $(cohive_t "日志：${log_file}" "Log: ${log_file}")"
  echo "  $(cohive_t "配置：${COHIVE_SERVER_ENV}" "Config: ${COHIVE_SERVER_ENV}")"
  [ "$rc" -eq 0 ]
}

# 交给 launchd 时 start/stop/restart 只用 launchctl。之前用 start 后台起的那份要先按进程号停掉，
# 否则 launchd 拉起的新进程会因端口被占而反复退出。
if launchd_managed; then
  case "${1:-}" in
    start|stop|restart)
      old_pid="$(running 2>/dev/null)" || old_pid=""
      if [ -n "$old_pid" ] && [ "$old_pid" != "$(launchd_pid)" ]; then
        echo "$(cohive_t "进程号 ${old_pid} 不是 launchd 拉起的（多半是之前用 start 后台起的），先停掉它" \
          "Pid ${old_pid} was not started by launchd (probably an earlier background start); stopping it first")"
        stop
      fi
      "launchd_$1"
      exit
      ;;
  esac
fi

case "${1:-}" in
  start) start ;;
  stop) stop ;;
  restart) stop && start ;;
  status) status ;;
  run)
    need_config
    mkdir -p "$COHIVE_DATA_DIR" "$run_dir"
    echo
    echo "==== $(date '+%Y-%m-%d %H:%M:%S') $(cohive_t "前台启动（库 $(cohive_db_name "$COHIVE_DATABASE_URL")，监听 ${addr}）" \
      "starting in the foreground (database $(cohive_db_name "$COHIVE_DATABASE_URL"), listening on ${addr})") ===="
    # exec 不换进程号：记下来，status 与 stop 照样能认出它
    echo "$$" > "$pid_file"
    exec_server
    ;;
  *)
    usage
    exit 2
    ;;
esac
