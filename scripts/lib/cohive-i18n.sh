# shellcheck shell=bash
# 运维脚本的中英文案：所有脚本共用（方案 D9，不进社区语言包）。
# 语言按 COHIVE_LANG > LC_ALL > LC_MESSAGES > LANG 判断，取第一个非空的值；
# 以 zh 开头（zh、zh_CN.UTF-8、zh-TW 等）用中文，其余（含 C、POSIX）用英文。都没设置时用英文。
# Messages of the ops scripts, shared by all scripts. Language: COHIVE_LANG > LC_ALL > LC_MESSAGES > LANG,
# first non-empty wins; a value starting with "zh" means Chinese, anything else (including C) English.
#
# 用法：cohive_t "中文" "English"，输出当前语言的那一句（不带换行）。
#   echo "$(cohive_t "已停止" "Stopped")"
# 要兼容 macOS 自带的 bash 3.2 与 POSIX sh：只用 case 与 printf。
# 变量名后紧跟中文标点时必须写成 ${名字}（bash 3.2 在 UTF-8 环境下会把多字节字符当成变量名的一部分）。

cohive_detect_lang() {
  local v
  for v in "${COHIVE_LANG:-}" "${LC_ALL:-}" "${LC_MESSAGES:-}" "${LANG:-}"; do
    case "$v" in
      '') continue ;;
      C|POSIX|C.*) continue ;;
      zh*|ZH*) echo zh; return 0 ;;
      *) echo en; return 0 ;;
    esac
  done
  echo en
}

COHIVE_SCRIPT_LANG="$(cohive_detect_lang)"

cohive_t() {
  if [ "$COHIVE_SCRIPT_LANG" = zh ]; then
    printf '%s' "$1"
  else
    printf '%s' "$2"
  fi
}
