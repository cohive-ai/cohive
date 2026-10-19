# 自定义主题

[English](theming.en.md)

看板的外观（颜色、圆角、阴影）全部由一组 CSS 变量决定，默认值在 [`web/src/theme.css`](../../web/src/theme.css)，组件里不写死任何颜色。服务器管理员可以放一份**自定义主题文件**，覆盖其中一部分变量，例如把品牌黄换成公司的蓝色。

- 没有自定义主题文件时，看板和默认完全一样。
- 自定义主题只能改变量，不能改布局、字号，也不能加任何别的样式。
- 每个人还可以在“个人中心 → 外观”里选浅色、深色或跟随系统，这一项和自定义主题无关，见文末。

## 启用

主题文件只能由能改服务器配置或数据目录的人放，网页上没有上传入口。两种放法，二选一：

1. 放在数据目录下，命名为 `theme.css`（Docker 部署时是数据卷里的 `/data/theme.css`）。有这个文件就加载，没有就用默认主题。
2. 用环境变量 `COHIVE_THEME_FILE` 指定路径。指定了却读不到时，服务**拒绝启动**，免得以为生效了其实没有。

改完主题文件要**重启服务**才生效。启动日志里会写明加载了哪份主题、几个变量。

上线前先自查，见下文“自查”：

```bash
cohive-server theme check /path/to/theme.css
```

## 写法

```css
/* 浅色主题，也是深色块没写到的变量的底 */
:root {
  --accent-fill: #1f6feb;
  --accent-ink: #ffffff;
}

/* 深色主题：写一次即可，“跟随系统”和“手动选深色”两种情况都会生效 */
:root[data-theme="dark"] {
  --accent-fill: #4c8dff;
  --accent-ink: #0b0b0c;
}
```

规则：

- 只允许两种块：`:root`（浅色，也可以写 `[data-theme="light"]`）和 `:root[data-theme="dark"]`（深色，也可以写 `[data-theme="dark"]`）。
- 块里只能写 CSS 变量声明 `--名字: 值;`，名字必须是下表里的变量。
- 圆角、阴影这类默认深色主题没有单独定义的变量，写在 `:root` 里两种主题共用。
- 取值按类型检查：
  - 颜色：`#rgb` 或 `#rrggbb`；
  - 可半透明的颜色：另外允许 `#rrggbbaa` 和 `rgb()`、`rgba()`；
  - 长度：`0` 或带 `px`、`rem`、`em` 的数；
  - 阴影：`none`，或最多两组“2 到 4 个长度 + 一个颜色”，颜色可以是十六进制或 `var(--颜色变量)`。
- 可以写注释 `/* … */`，加载时注释会被丢掉。
- 文件不超过 64 KB。

**一律拒绝**的写法：`url()`、`image-set()`、`@import`、`@media` 等任何 @ 规则、`expression()`、`attr()`、`env()`、`javascript:`、引号、反斜杠转义、`!important`、尖括号、其他选择器、普通属性（例如 `color: red`），以及块里再套块。只要出现一处，**整份文件都不加载**，看板继续用默认主题，启动日志会提示。

拼错或不认识的变量会被忽略，检查命令会列出来。

**安全上的做法**：服务器不会把文件原文发给浏览器，而是逐条解析、只留下合法的变量取值，再重新生成一份样式表，以 `/theme.css` 提供，排在默认样式之后加载。

## 变量

完整清单以 [`web/theme-spec.json`](../../web/theme-spec.json) 为准，网页测试和检查命令读的都是它。

| 变量 | 用途 | 类型 | 浅色默认 | 深色默认 |
|---|---|---|---|---|
| `--bg` | 页面底色 | 颜色 | `#f5f5f5` | `#0b0b0c` |
| `--surface` | 卡片、面板、页头、输入框的底色 | 颜色 | `#ffffff` | `#141416` |
| `--sunken` | 看板列底、表头、中性标签底 | 颜色 | `#ebebeb` | `#070708` |
| `--ink` | 正文与标题 | 颜色 | `#0b0b0c` | `#f5f5f5` |
| `--muted` | 次要文字 | 颜色 | `#555555` | `#9c9ca3` |
| `--line` | 分隔线、卡片边框 | 颜色 | `#dcdcdc` | `#2b2b30` |
| `--line-strong` | 输入框、次按钮的边框 | 颜色 | `#8a8a8a` | `#6a6a72` |
| `--accent` | 链接、当前项、焦点框、选中边框 | 颜色 | `#0b0b0c` | `#f5f5f5` |
| `--accent-soft` | 选中卡片、提示框的浅底 | 颜色 | `#fff5cc` | `#2a2410` |
| `--accent-fill` | 主按钮底、当前导航下划线、输入框聚焦光圈 | 颜色 | `#ffcc00` | `#ffcc00` |
| `--accent-ink` | 主按钮、待验证标签上的字 | 颜色 | `#0b0b0c` | `#0b0b0c` |
| `--wait` | “轮到你”的文字 | 颜色 | `#0b0b0c` | `#ffcc00` |
| `--wait-soft` | “等你处理”的荧光笔底、提醒类标签底 | 颜色 | `#ffe680` | `#3a3000` |
| `--wait-mark` | “轮到你”左边条、待验证标签底、交接高亮 | 颜色 | `#ffcc00` | `#ffcc00` |
| `--teal` | 好的结束：已关闭、有效、在线、已绑定 | 颜色 | `#0e7436` | `#4cc47f` |
| `--teal-soft` | 成功提示、成功标签的浅底 | 颜色 | `#e1f3e7` | `#12301e` |
| `--danger` | 出错、已退回、已停用、危险按钮 | 颜色 | `#c2261a` | `#ff6b5c` |
| `--on-status` | 绿、红实色底上的字 | 颜色 | `#ffffff` | `#0b0b0c` |
| `--tool-claude` | Claude Code 图标 | 颜色 | `#d97757` | `#d97757` |
| `--tool-codex` | Codex 图标 | 颜色 | `#000000` | `#ffffff` |
| `--tool-other` | 其他编程工具的图标 | 颜色 | `#8a8a8a` | `#8a8a92` |
| `--brand-yellow` | 标志里的智能体格子（两种主题相同） | 颜色 | `#ffcc00` | 同浅色 |
| `--backdrop` | 对话框背后的遮罩 | 颜色（可半透明） | `rgb(0 0 0 / 0.4)` | `rgb(0 0 0 / 0.6)` |
| `--radius-control` | 按钮、输入框、标签、卡片的圆角 | 长度 | `2px` | 同浅色 |
| `--radius-panel` | 面板、列表的圆角 | 长度 | `4px` | 同浅色 |
| `--hard-shadow` | 主按钮与卡片悬停的阴影 | 阴影 | `2px 2px 0 var(--ink)` | 同浅色 |
| `--focus-ring` | 输入框聚焦时的光圈 | 阴影 | `0 0 0 3px var(--accent-fill)` | 同浅色 |

**文字，至少 4.5:1**（前景 / 背景）：

- `--ink` / `--bg`
- `--ink` / `--surface`
- `--ink` / `--sunken`
- `--muted` / `--bg`
- `--muted` / `--surface`
- `--muted` / `--sunken`
- `--accent` / `--bg`
- `--accent` / `--surface`
- `--accent` / `--sunken`
- `--teal` / `--bg`
- `--teal` / `--surface`
- `--teal` / `--sunken`
- `--wait` / `--bg`
- `--wait` / `--surface`
- `--wait` / `--sunken`
- `--danger` / `--bg`
- `--danger` / `--surface`
- `--danger` / `--sunken`
- `--accent-ink` / `--wait-mark`（待验证、已重开标签）
- `--surface` / `--ink`（已接单、修复中、管理员标签）
- `--on-status` / `--teal`（已关闭、有效标签）
- `--on-status` / `--danger`（已退回、已停用标签，危险按钮）
- `--wait` / `--wait-soft`（“等你处理”荧光笔底）
- `--teal` / `--teal-soft`
- `--ink` / `--teal-soft`
- `--ink` / `--wait-soft`
- `--ink` / `--accent-soft`
- `--accent` / `--accent-soft`
- `--accent-ink` / `--accent-fill`（主按钮）

**图形，至少 3:1**（前景 / 背景）：

- `--line-strong` / `--surface`（输入框、次按钮边框）
- `--tool-claude` / `--surface`（卡片上的 Claude Code 图标）
- `--tool-codex` / `--surface`（卡片上的 Codex 图标）
- `--tool-other` / `--surface`（卡片上的其他工具图标）

## 自查

```bash
cohive-server theme check /path/to/theme.css
```

Docker 部署时：

```bash
docker compose exec cohive cohive-server theme check /data/theme.css
```

它和网页的对比度测试用同一份规则（`web/theme-spec.json`），会报告：

- **被拒绝的写法**：第几行、哪个变量、为什么。有任何一处，服务器都不会加载这份文件。
- **未知变量**：会被忽略的声明，多半是拼错了。
- **对比度**：默认主题叠加你的文件后，浅色、深色两种主题下上面每一组的实际对比度，不达标的标 `FAIL`。

不给文件时检查内置的默认主题。

退出码可以直接用在脚本里：

| 退出码 | 含义 |
|---|---|
| 0 | 全部通过 |
| 1 | 有被拒绝的写法、未知变量或对比度不达标 |
| 2 | 用法不对，或文件读不到 |

对比度不达标的主题仍然会被加载，启动日志会提醒。是否上线由管理员决定，但请尽量修到全部通过。

## 完整例子

[`examples/themes/harbor.css`](../../examples/themes/harbor.css)：把品牌黄换成蓝色，圆角放大一点，去掉硬阴影，两种主题都通过检查。

```css
/*
 * Harbor: an example custom theme for Cohive (a blue accent instead of the brand yellow).
 * Only CSS variables of the default theme are allowed; see docs/guides/theming.en.md.
 *
 *   cohive-server theme check examples/themes/harbor.css
 *   COHIVE_THEME_FILE=/path/to/harbor.css cohive-server serve
 */

/* Light theme (also the base for anything the dark block does not set) */
:root {
  --bg: #f3f5f8;
  --surface: #ffffff;
  --sunken: #e6ebf1;
  --ink: #0f1b2d;
  --muted: #4d5b6e;
  --line: #d5dce6;
  --line-strong: #7d8a9c;

  /* Interactive things: links and current items in navy, the primary button in blue */
  --accent: #0b4f8a;
  --accent-soft: #e3eefb;
  --accent-fill: #1f6feb;
  --accent-ink: #ffffff;

  /* "Your turn": a blue bar and tag, navy text on a pale blue highlighter */
  --wait: #0b2e59;
  --wait-soft: #dbeafe;
  --wait-mark: #1f6feb;

  /* Softer corners and no hard shadow; the focus ring keeps its default (accent-fill) */
  --radius-control: 4px;
  --radius-panel: 8px;
  --hard-shadow: none;
}

/* Dark theme */
:root[data-theme="dark"] {
  --bg: #0d1420;
  --surface: #152031;
  --sunken: #0a101a;
  --ink: #e8eef7;
  --muted: #9aa8bb;
  --line: #24324a;
  --line-strong: #5d6f8a;
  --accent: #9cc8ff;
  --accent-soft: #14294a;
  --accent-fill: #4c8dff;
  --accent-ink: #0b0b0c;
  --wait: #9cc8ff;
  --wait-soft: #10294d;
  --wait-mark: #4c8dff;
}
```

## 个人的浅色、深色

“个人中心 → 外观”里可以选跟随系统（默认）、浅色或深色。这个选择存在当前浏览器里，不跟账号走，所以：

- 每台电脑可以各选各的（比如白天的办公室显示器和晚上的笔记本）；
- 不用登录就能生效，登录页也不会先闪一下另一种颜色；
- 服务器上没有任何地方需要用到它，不必为它改账号数据。

它只决定用浅色块还是深色块，两块都可以被自定义主题覆盖。
