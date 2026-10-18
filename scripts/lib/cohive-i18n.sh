# shellcheck shell=bash
# 运维脚本的中英文案：所有脚本共用（方案 D9，不进社区语言包）。
# 语言按 COHIVE_LANG > LC_ALL > LC_MESSAGES > LANG 判断，取第一个非空的值；
# 以 zh 开头（zh、zh_CN.UTF-8、zh-TW 等）用中文，其余用英文。
# 这几个变量都没设置（或只有 C、POSIX）时，在 macOS 上读系统界面语言
# （defaults read -g AppleLanguages 的第一项），以 zh 开头用中文，否则英文；读不到、不是 macOS 一律英文。
# 与 Go 端 internal/i18n 的 Local() 行为一致：环境变量设置了但不是中文（例如 en_US.UTF-8、ja_JP）时按英文，
# 不再看系统界面语言。
# Messages of the ops scripts, shared by all scripts. Language: COHIVE_LANG > LC_ALL > LC_MESSAGES > LANG,
# first non-empty wins; a value starting with "zh" means Chinese, anything else English. If none is set
# (or only C/POSIX), on macOS the first entry of `defaults read -g AppleLanguages` decides (zh* = Chinese);
# unreadable or not macOS means English. Same as Local() in internal/i18n on the Go side.
#
# 用法：cohive_t "中文" "English"，输出当前语言的那一句（不带换行）。
#   echo "$(cohive_t "已停止" "Stopped")"
# 要兼容 macOS 自带的 bash 3.2 与 POSIX sh：只用 case 与 printf。
# 变量名后紧跟中文标点时必须写成 ${名字}（bash 3.2 在 UTF-8 环境下会把多字节字符当成变量名的一部分）。

# macOS 系统界面语言的第一项，如 zh-Hans-CN；读不到（命令失败、不是 macOS）时输出空。
# defaults 的输出形如 "(\n    "zh-Hans-CN",\n    "en-CN"\n)"，简单的代码可能不带引号。
cohive_system_lang() {
  [ "$(uname -s 2>/dev/null)" = Darwin ] || return 0
  local out line
  out="$(defaults read -g AppleLanguages 2>/dev/null)" || return 0
  while IFS= read -r line; do
    line="$(printf '%s' "$line" | tr -d '(), "\t')"
    if [ -n "$line" ]; then
      printf '%s' "$line"
      return 0
    fi
  done <<EOF
$out
EOF
}

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
  # 环境变量都没给出语言：看 macOS 的系统界面语言
  case "$(cohive_system_lang)" in
    zh*|ZH*) echo zh ;;
    *) echo en ;;
  esac
}

COHIVE_SCRIPT_LANG="$(cohive_detect_lang)"

cohive_t() {
  if [ "$COHIVE_SCRIPT_LANG" = zh ]; then
    printf '%s' "$1"
  else
    printf '%s' "$2"
  fi
}
