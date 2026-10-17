#!/usr/bin/env bash
# 保存钉钉企业内部应用的凭据与卡片模板编号到 ~/.config/cohive/dingtalk.env（仅本人可读）。
# Client Secret 输入时不回显，也不会进入命令历史。用 scripts/cohive-server.sh 或 scripts/run-server.sh 启动服务时
# 只在服务进程里加载。Docker 部署不用这个脚本，把同名变量写进 deploy/.env。
set -euo pipefail

dir="$HOME/.config/cohive"
file="${COHIVE_DINGTALK_ENV:-$dir/dingtalk.env}"

printf 'Client ID（钉钉开发者后台“凭证与基础信息”，原 AppKey）：'
IFS= read -r id
id="$(printf '%s' "$id" | tr -d '[:space:]')"
printf 'Client Secret（原 AppSecret，输入不显示）：'
IFS= read -rs secret
echo
secret="$(printf '%s' "$secret" | tr -d '[:space:]')"
printf '通知卡片模板编号（没建可直接回车，群通知将不能 @ 人）：'
IFS= read -r notice
notice="$(printf '%s' "$notice" | tr -d '[:space:]')"
printf '审批卡片模板编号（没建可直接回车，审批消息将不带按钮）：'
IFS= read -r approval
approval="$(printf '%s' "$approval" | tr -d '[:space:]')"

if [[ ! "$id" =~ ^[A-Za-z0-9_-]{6,}$ ]]; then
  echo "Client ID 格式不对，请从钉钉开发者后台重新复制。" >&2
  exit 1
fi
if [[ ! "$secret" =~ ^[A-Za-z0-9_-]{16,}$ ]]; then
  echo "Client Secret 格式不对，请从钉钉开发者后台重新复制。" >&2
  exit 1
fi
for v in "$notice" "$approval"; do
  if [[ -n "$v" && ! "$v" =~ ^[A-Za-z0-9._-]+$ ]]; then
    echo "模板编号格式不对：$v" >&2
    exit 1
  fi
done

mkdir -p "$(dirname "$file")"
chmod 700 "$(dirname "$file")"
umask 077
{
  printf 'export COHIVE_DINGTALK_CLIENT_ID=%s\n' "$id"
  printf 'export COHIVE_DINGTALK_CLIENT_SECRET=%s\n' "$secret"
  printf 'export COHIVE_DINGTALK_NOTICE_TEMPLATE=%s\n' "$notice"
  printf 'export COHIVE_DINGTALK_APPROVAL_TEMPLATE=%s\n' "$approval"
} > "$file"
chmod 600 "$file"
unset secret

echo "已保存到 ${file}（仅你本人可读）"
echo "用 scripts/cohive-server.sh restart 重启服务即可加载；不要在交互终端里 source 这个文件"
