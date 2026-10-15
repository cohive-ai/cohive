#!/usr/bin/env bash
# 保存 Telegram 机器人令牌到 ~/.config/cohive/telegram.env（仅本人可读）。
# 输入时不回显，也不会进入命令历史。协作服务启动前 source 这个文件即可。
set -euo pipefail

dir="$HOME/.config/cohive"
file="$dir/telegram.env"

printf '粘贴 Telegram 机器人令牌后回车（输入不显示）：'
IFS= read -rs token
echo

token="$(printf '%s' "$token" | tr -d '[:space:]')"
if [[ ! "$token" =~ ^[0-9]+:[A-Za-z0-9_-]{20,}$ ]]; then
  echo "格式不对：机器人令牌应形如 123456789:AAH...，请从 @BotFather 重新复制。" >&2
  exit 1
fi

mkdir -p "$dir"
chmod 700 "$dir"
umask 077
printf 'export COHIVE_TELEGRAM_BOT_TOKEN=%s\n' "$token" > "$file"
chmod 600 "$file"
unset token

echo "已保存到 ${file}（仅你本人可读）"
echo "启动服务前执行：source ${file}"
