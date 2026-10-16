#!/usr/bin/env bash
# 单机运行协作服务（测试服用）：启动、停止、查看状态。
#
# 用法：scripts/cohive-server.sh start|stop|restart|status|run
#   start    后台启动，写进程号文件，等健康检查通过
#   stop     只停进程号文件里记录、且确实是 cohive-server 的进程
#   restart  stop 后 start
#   status   是否在运行、版本、数据目录与日志位置
#   run      前台运行（交给 launchd、systemd 等托管时用）
#
# 目录（可用环境变量覆盖）：
#   COHIVE_HOME      缺省 ~/.local/share/cohive-server
#     server.env     非秘密配置，见 scripts/server.env.example
#     data/          COHIVE_DATA_DIR，初始管理员密码文件等
#     logs/          服务输出 server.log
#     run/           进程号文件 server.pid
# 机器人令牌：沿用 ~/.config/cohive/telegram.env（scripts/set-telegram-token.sh 写入），
#   只在服务进程里加载，不打印、不写日志、不进 server.env；可用 COHIVE_TELEGRAM_ENV 指定别的文件。
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

fail() { echo "错误：$*" >&2; exit 1; }

# 健康检查用的地址：“:8787”这类只写端口的监听地址按本机访问
probe="$addr"
case "$probe" in :*) probe="127.0.0.1$probe" ;; 0.0.0.0:*) probe="127.0.0.1:${probe#0.0.0.0:}" ;; esac
port="${addr##*:}"

need_config() {
  [ -n "${COHIVE_DATABASE_URL:-}" ] || fail "没有设置 COHIVE_DATABASE_URL：写进 $COHIVE_SERVER_ENV（参照 scripts/server.env.example）或在环境变量里设置"
  [ -x "$bin" ] || fail "找不到服务端程序 $bin：先在仓库根目录执行 make build，或用 COHIVE_BIN 指定"
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
    *) echo "进程号文件 $pid_file 记录的 $pid 是别的程序（$comm），不会动它" >&2; return 2 ;;
  esac
}

# 只在子进程里加载机器人令牌，然后执行服务端；令牌不经过本脚本的任何输出
exec_server() {
  if [ -f "$tg_env" ]; then
    # shellcheck disable=SC1090
    . "$tg_env"
    export COHIVE_TELEGRAM_BOT_TOKEN
  fi
  exec "$bin" serve --addr "$addr"
}

port_owner() {
  command -v lsof >/dev/null 2>&1 || return 1
  local p
  p="$(lsof -nP -iTCP:"$port" -sTCP:LISTEN -t 2>/dev/null | head -1)"
  [ -n "$p" ] || return 1
  printf '%s（%s）' "$p" "$(ps -p "$p" -o comm= 2>/dev/null || echo 未知)"
}

start() {
  need_config
  local pid rc=0
  pid="$(running)" || rc=$?
  if [ "$rc" -eq 0 ]; then
    echo "已在运行，进程号 $pid（$addr）"
    return 0
  fi
  [ "$rc" -eq 2 ] && fail "进程号文件指向别的程序，请确认后删除 $pid_file 再启动"
  if owner="$(port_owner)"; then
    fail "端口 $port 已被进程 $owner 占用，不会自动结束它；请先确认是什么程序"
  fi
  mkdir -p "$COHIVE_DATA_DIR" "$log_dir" "$run_dir"
  chmod 700 "$COHIVE_HOME" "$COHIVE_DATA_DIR" "$run_dir" 2>/dev/null || true
  {
    echo
    echo "==== $(date '+%Y-%m-%d %H:%M:%S') 启动（库 $(cohive_db_name "$COHIVE_DATABASE_URL")，监听 $addr）===="
  } >> "$log_file"
  ( exec_server ) >> "$log_file" 2>&1 < /dev/null &
  pid=$!
  echo "$pid" > "$pid_file"
  for _ in $(seq 1 30); do
    if curl -fsS "http://$probe/healthz" >/dev/null 2>&1; then
      echo "已启动，进程号 $pid，监听 $addr"
      echo "  数据目录：$COHIVE_DATA_DIR"
      echo "  日志：$log_file"
      return 0
    fi
    if ! kill -0 "$pid" 2>/dev/null; then
      rm -f "$pid_file"
      echo "启动失败，日志最后 20 行：" >&2
      tail -n 20 "$log_file" >&2
      exit 1
    fi
    sleep 0.5
  done
  echo "15 秒内健康检查没有通过（进程 $pid 仍在运行），日志最后 20 行：" >&2
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
    echo "没有在运行"
    return 0
  fi
  kill -TERM "$pid"
  for _ in $(seq 1 20); do
    if ! kill -0 "$pid" 2>/dev/null; then
      rm -f "$pid_file"
      echo "已停止（进程号 $pid）"
      return 0
    fi
    sleep 0.5
  done
  fail "进程 $pid 10 秒内没有退出；不会强制结束，请查看日志 $log_file 后自行处理"
}

status() {
  local pid rc=0
  pid="$(running)" || rc=$?
  if [ "$rc" -eq 0 ]; then
    echo "运行中，进程号 $pid，监听 $addr"
    local v
    v="$(curl -fsS "http://$probe/api/v1/version" 2>/dev/null || true)"
    [ -n "$v" ] && echo "  版本：$v"
  else
    echo "没有在运行"
  fi
  echo "  数据库：$(cohive_db_name "${COHIVE_DATABASE_URL:-（未设置）}")"
  echo "  数据目录：$COHIVE_DATA_DIR"
  echo "  日志：$log_file"
  echo "  配置：$COHIVE_SERVER_ENV"
  [ "$rc" -eq 0 ]
}

case "${1:-}" in
  start) start ;;
  stop) stop ;;
  restart) stop && start ;;
  status) status ;;
  run)
    need_config
    mkdir -p "$COHIVE_DATA_DIR"
    exec_server
    ;;
  *)
    sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'
    exit 2
    ;;
esac
