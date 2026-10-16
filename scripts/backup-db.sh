#!/usr/bin/env bash
# 备份协作服务的数据库：pg_dump 自定义格式，按日期命名，保留最近 N 份。
#
# 用法：scripts/backup-db.sh
# 配置（server.env 或环境变量，见 scripts/server.env.example）：
#   COHIVE_DATABASE_URL   要备份的库（必填）
#   COHIVE_BACKUP_DIR     备份目录，缺省 $COHIVE_HOME/backups
#   COHIVE_BACKUP_KEEP    保留份数，缺省 14
#   COHIVE_PG_CONTAINER   数据库跑在 Docker 容器里时填容器名（例如 cohive-dev-postgres），
#                         用容器里同版本的 pg_dump；本机 pg_dump 版本比服务器旧时必须设置
#   COHIVE_PG_CONTAINER_PORT  容器内的数据库端口，缺省 5432
#
# 文件名：cohive-<库名>-YYYYmmdd-HHMMSS.dump。先写 .partial，校验能读出目录后再改名；
# 只在本次备份成功后删除更旧的，且只删同一个库、同一命名规则的文件。
# 密码只经 PGPASSWORD 环境变量传给 pg_dump，不出现在命令参数里，也不打印。
# 恢复：一律恢复到新库，核对后再切换，见 docs/guides/test-server.md。
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=lib/cohive-env.sh
. "$root/scripts/lib/cohive-env.sh"
cohive_load_env

fail() { echo "错误：$*" >&2; exit 1; }

url="${COHIVE_DATABASE_URL:-}"
[ -n "$url" ] || fail "没有设置 COHIVE_DATABASE_URL"
dir="${COHIVE_BACKUP_DIR:-$COHIVE_HOME/backups}"
keep="${COHIVE_BACKUP_KEEP:-14}"
[[ "$keep" =~ ^[1-9][0-9]*$ ]] || fail "COHIVE_BACKUP_KEEP 应为正整数，收到 $keep"
container="${COHIVE_PG_CONTAINER:-}"

# 解析 postgres://用户:密码@主机:端口/库名?参数（用户名、密码里有特殊字符时需要按网址编码，这里不解码）
re='^postgres(ql)?://([^:@/]+)(:([^@/]*))?@([^:/?]+)(:([0-9]+))?/([^?/]+)(\?(.*))?$'
[[ "$url" =~ $re ]] || fail "COHIVE_DATABASE_URL 格式看不懂，应形如 postgres://用户:密码@主机:端口/库名"
user="${BASH_REMATCH[2]}"
pass="${BASH_REMATCH[4]}"
host="${BASH_REMATCH[5]}"
port="${BASH_REMATCH[7]:-5432}"
db="${BASH_REMATCH[8]}"
params="${BASH_REMATCH[10]:-}"
[[ "$db" =~ ^[A-Za-z0-9_-]+$ ]] || fail "库名 $db 含有备份脚本不支持的字符"
sslmode=""
if [[ "$params" =~ (^|&)sslmode=([a-z-]+) ]]; then sslmode="${BASH_REMATCH[2]}"; fi

umask 077
mkdir -p "$dir"
chmod 700 "$dir" 2>/dev/null || true
out="$dir/cohive-$db-$(date +%Y%m%d-%H%M%S).dump"
[ -e "$out" ] && fail "$out 已存在（一秒内重复备份），稍后再试"
part="$out.partial"
trap 'rm -f "$part"' EXIT

if [ -n "$container" ]; then
  command -v docker >/dev/null 2>&1 || fail "设置了 COHIVE_PG_CONTAINER，但找不到 docker"
  cport="${COHIVE_PG_CONTAINER_PORT:-5432}"
  # -e PGPASSWORD 不带值：从本进程的环境变量传入容器，密码不出现在命令参数里
  PGPASSWORD="$pass" docker exec -e PGPASSWORD "$container" \
    pg_dump -h 127.0.0.1 -p "$cport" -U "$user" -Fc "$db" > "$part" \
    || fail "pg_dump 失败（容器 $container，库 $db）"
  # 用容器里同版本的 pg_restore 校验：旧版 pg_restore 读不了新版的备份格式
  docker exec -i "$container" pg_restore --list < "$part" > /dev/null \
    || fail "备份文件校验失败，已丢弃"
else
  command -v pg_dump >/dev/null 2>&1 || fail "找不到 pg_dump：安装与服务器同版本的 PostgreSQL 客户端，或设置 COHIVE_PG_CONTAINER 用容器里的"
  local_major="$(pg_dump --version | sed -E 's/[^0-9]*([0-9]+).*/\1/')"
  if command -v psql >/dev/null 2>&1; then
    server_num="$(PGPASSWORD="$pass" PGSSLMODE="$sslmode" psql -h "$host" -p "$port" -U "$user" -d "$db" -tAc 'SHOW server_version_num' 2>/dev/null || true)"
    if [[ "$server_num" =~ ^[0-9]+$ ]] && [ "$((server_num / 10000))" -gt "$local_major" ]; then
      fail "本机 pg_dump 是 $local_major，数据库服务器是 $((server_num / 10000))，pg_dump 不能备份更新版本的服务器。" \
        "请安装同版本客户端（macOS：brew install postgresql@$((server_num / 10000))），或在 server.env 里设置 COHIVE_PG_CONTAINER=<数据库容器名>"
    fi
  fi
  PGPASSWORD="$pass" PGSSLMODE="$sslmode" pg_dump -h "$host" -p "$port" -U "$user" -Fc -f "$part" "$db" \
    || fail "pg_dump 失败（库 $db）"
  pg_restore --list "$part" > /dev/null || fail "备份文件校验失败，已丢弃"
fi

mv "$part" "$out"
trap - EXIT
size="$(du -h "$out" | cut -f1 | tr -d ' ')"

# 轮换：文件名里的时间可按字典序排序，最新的在前，保留前 keep 份
shopt -s nullglob
backups=( "$dir"/cohive-"$db"-[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]-[0-9][0-9][0-9][0-9][0-9][0-9].dump )
removed=0
if [ "${#backups[@]}" -gt "$keep" ]; then
  while IFS= read -r f; do
    rm -f -- "$f"
    removed=$((removed + 1))
  done < <(printf '%s\n' "${backups[@]}" | sort -r | tail -n +"$((keep + 1))")
fi
echo "$(date '+%Y-%m-%d %H:%M:%S') 已备份库 $db → $out（$size）；保留最近 $keep 份，删除旧备份 $removed 份"
