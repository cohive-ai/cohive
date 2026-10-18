# shellcheck shell=bash
# cohive-server.sh 与 backup-db.sh 共用：确定目录、读取 server.env。
# server.env 只放非秘密配置（数据库地址、监听地址等），一行一个 KEY=VALUE；
# 已经在环境变量里设置的值优先，文件里的不覆盖。机器人令牌不放这里（见 cohive-server.sh）。

# 中英文案（cohive_t）
# shellcheck source=cohive-i18n.sh
. "$(dirname "${BASH_SOURCE[0]}")/cohive-i18n.sh"

COHIVE_HOME="${COHIVE_HOME:-$HOME/.local/share/cohive-server}"
COHIVE_SERVER_ENV="${COHIVE_SERVER_ENV:-$COHIVE_HOME/server.env}"

cohive_load_env() {
  local line key val
  [ -f "$COHIVE_SERVER_ENV" ] || return 0
  while IFS= read -r line || [ -n "$line" ]; do
    line="${line#"${line%%[![:space:]]*}"}"
    case "$line" in ''|'#'*) continue ;; esac
    line="${line#export }"
    if [[ ! "$line" =~ ^([A-Z_][A-Z0-9_]*)=(.*)$ ]]; then
      echo "$(cohive_t "错误：${COHIVE_SERVER_ENV} 里这一行看不懂（应为 KEY=VALUE）：${line%%=*}=…" \
        "Error: cannot parse this line in ${COHIVE_SERVER_ENV} (expected KEY=VALUE): ${line%%=*}=...")" >&2
      return 1
    fi
    key="${BASH_REMATCH[1]}"
    val="${BASH_REMATCH[2]}"
    # 去掉成对的引号
    if [[ "$val" =~ ^\"(.*)\"$ ]] || [[ "$val" =~ ^\'(.*)\'$ ]]; then
      val="${BASH_REMATCH[1]}"
    fi
    if [ -z "${!key+x}" ]; then
      export "$key=$val"
    fi
  done < "$COHIVE_SERVER_ENV"
}

# cohive_db_name 从数据库地址里取出库名（不含密码），用于提示与备份文件名。
cohive_db_name() {
  local u="${1%%\?*}"
  printf '%s' "${u##*/}"
}
