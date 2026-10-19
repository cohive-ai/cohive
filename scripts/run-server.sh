#!/usr/bin/env bash
# 启动协作服务。机器人令牌只在本脚本的进程里加载，不进入你的交互终端，
# 因此之后在终端里启动的其他程序（包括 AI 编程工具）读不到它。
#
# 用法：scripts/run-server.sh [serve 的参数，例如 --addr 0.0.0.0:8787]
# 需要的环境变量：COHIVE_DATABASE_URL；对外地址在网页“服务器管理 → 设置”里填，或用 COHIVE_EXTERNAL_URL 锁定。
set -euo pipefail

env_file="$HOME/.config/cohive/telegram.env"
if [ -f "$env_file" ]; then
  # shellcheck disable=SC1090
  source "$env_file"
  export COHIVE_TELEGRAM_BOT_TOKEN
fi

dt_file="${COHIVE_DINGTALK_ENV:-$HOME/.config/cohive/dingtalk.env}"
if [ -f "$dt_file" ]; then
  # shellcheck disable=SC1090
  source "$dt_file"
  export COHIVE_DINGTALK_CLIENT_ID COHIVE_DINGTALK_CLIENT_SECRET COHIVE_DINGTALK_NOTICE_TEMPLATE COHIVE_DINGTALK_APPROVAL_TEMPLATE
fi

sl_file="${COHIVE_SLACK_ENV:-$HOME/.config/cohive/slack.env}"
if [ -f "$sl_file" ]; then
  # shellcheck disable=SC1090
  source "$sl_file"
  export COHIVE_SLACK_BOT_TOKEN COHIVE_SLACK_APP_TOKEN
fi

root="$(cd "$(dirname "$0")/.." && pwd)"
exec "$root/bin/cohive-server" serve "$@"
