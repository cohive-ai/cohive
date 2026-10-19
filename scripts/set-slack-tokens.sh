#!/usr/bin/env bash
# 保存 Slack 应用的两个令牌到 ~/.config/cohive/slack.env（仅本人可读）：机器人令牌（xoxb- 开头）与应用级令牌（xapp- 开头）。
# 两个令牌输入时都不回显，也不会进入命令历史。用 scripts/cohive-server.sh 或 scripts/run-server.sh 启动服务时
# 只在服务进程里加载。Docker 部署不用这个脚本，把同名变量写进 deploy/.env。
set -euo pipefail

# 中英文案（cohive_t），按 COHIVE_LANG > LC_ALL > LC_MESSAGES > LANG 判断
# shellcheck source=lib/cohive-i18n.sh
. "$(cd "$(dirname "$0")" && pwd)/lib/cohive-i18n.sh"

dir="$HOME/.config/cohive"
file="${COHIVE_SLACK_ENV:-$dir/slack.env}"

printf '%s' "$(cohive_t "机器人令牌（应用后台“OAuth & Permissions”，xoxb- 开头，输入不显示）：" "Bot token (app settings, OAuth & Permissions; starts with xoxb-; input is hidden): ")"
IFS= read -rs bot
echo
bot="$(printf '%s' "$bot" | tr -d '[:space:]')"
printf '%s' "$(cohive_t "应用级令牌（应用后台“Basic Information → App-Level Tokens”，xapp- 开头，输入不显示）：" "App-level token (app settings, Basic Information > App-Level Tokens; starts with xapp-; input is hidden): ")"
IFS= read -rs app
echo
app="$(printf '%s' "$app" | tr -d '[:space:]')"

if [[ "$bot" == xapp-* && "$app" == xoxb-* ]]; then
  echo "$(cohive_t "两个令牌填反了：先填 xoxb- 开头的机器人令牌，再填 xapp- 开头的应用级令牌。" "The two tokens are swapped: enter the xoxb- bot token first, then the xapp- app-level token.")" >&2
  exit 1
fi
if [[ ! "$bot" =~ ^xoxb-[A-Za-z0-9-]{10,}$ ]]; then
  echo "$(cohive_t "机器人令牌格式不对（应以 xoxb- 开头），请从 Slack 应用后台重新复制。" "Invalid bot token format (it should start with xoxb-); copy it again from the Slack app settings.")" >&2
  exit 1
fi
if [[ ! "$app" =~ ^xapp-[A-Za-z0-9-]{10,}$ ]]; then
  echo "$(cohive_t "应用级令牌格式不对（应以 xapp- 开头），请从 Slack 应用后台重新复制。" "Invalid app-level token format (it should start with xapp-); copy it again from the Slack app settings.")" >&2
  exit 1
fi

mkdir -p "$(dirname "$file")"
chmod 700 "$(dirname "$file")"
umask 077
{
  printf 'export COHIVE_SLACK_BOT_TOKEN=%s\n' "$bot"
  printf 'export COHIVE_SLACK_APP_TOKEN=%s\n' "$app"
} > "$file"
chmod 600 "$file"
unset bot app

echo "$(cohive_t "已保存到 ${file}（仅你本人可读）" "Saved to ${file} (readable only by you)")"
echo "$(cohive_t "用 scripts/cohive-server.sh restart 重启服务即可加载；不要在交互终端里 source 这个文件" "Restart the server with scripts/cohive-server.sh restart to load it; do not source this file in an interactive terminal")"
