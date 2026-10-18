#!/usr/bin/env bash
# 保存 Telegram 机器人令牌到 ~/.config/cohive/telegram.env（仅本人可读）。
# 输入时不回显，也不会进入命令历史。用 scripts/run-server.sh 启动服务时会自动加载。
set -euo pipefail

# 中英文案（cohive_t），按 COHIVE_LANG > LC_ALL > LC_MESSAGES > LANG 判断
# shellcheck source=lib/cohive-i18n.sh
. "$(cd "$(dirname "$0")" && pwd)/lib/cohive-i18n.sh"

dir="$HOME/.config/cohive"
file="$dir/telegram.env"

printf '%s' "$(cohive_t "粘贴 Telegram 机器人令牌后回车（输入不显示）：" "Paste the Telegram bot token and press Enter (input is hidden): ")"
IFS= read -rs token
echo

token="$(printf '%s' "$token" | tr -d '[:space:]')"
if [[ ! "$token" =~ ^[0-9]+:[A-Za-z0-9_-]{20,}$ ]]; then
  echo "$(cohive_t "格式不对：机器人令牌应形如 123456789:AAH...，请从 @BotFather 重新复制。" "Invalid format: a bot token looks like 123456789:AAH...; copy it again from @BotFather.")" >&2
  exit 1
fi

mkdir -p "$dir"
chmod 700 "$dir"
umask 077
printf 'export COHIVE_TELEGRAM_BOT_TOKEN=%s\n' "$token" > "$file"
chmod 600 "$file"
unset token

echo "$(cohive_t "已保存到 ${file}（仅你本人可读）" "Saved to ${file} (readable only by you)")"
echo "$(cohive_t "用 scripts/run-server.sh 启动服务即可加载它；不要在交互终端里 source 这个文件" "Start the server with scripts/run-server.sh to load it; do not source this file in an interactive terminal")"
