# Cohive 品牌标志

[English](README.md)

Cohive 的标志是一枚缺了右边一条边的六边形：既是蜂巢的一个格子，也是字母 C。内侧的格子是拍板的人，缺口处的黄色格子是正在接进来的智能体。标志和字标都由几何图形画成，所有 SVG 都不依赖字体。

带示例的完整规范见网站的[品牌页](../../site/brand.html)。

## 文件

| 文件 | 用途 |
|---|---|
| `cohive-lockup.svg` | 横版（标志 + 字标），浅色底，默认使用 |
| `cohive-lockup-reverse.svg` | 横版，深色底 |
| `cohive-lockup-stacked.svg` | 竖版，浅色底，用于方形位置 |
| `cohive-lockup-reverse-stacked.svg` | 竖版，深色底 |
| `cohive-mark.svg` | 只有标志，浅色底（头像、应用图标） |
| `cohive-mark-reverse.svg` | 只有标志，深色底 |
| `cohive-mark-mono.svg` | 单色标志，用于单色印刷、印章、蚀刻 |
| `favicon.svg` | 网站图标：黑色方底上的深色底标志 |

README 顶部用两份横版，GitHub 按访客的深浅色主题自动切换：

```html
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/brand/cohive-lockup-reverse.svg">
  <img src="docs/brand/cohive-lockup.svg" alt="Cohive" width="320">
</picture>
```

`site/assets/brand/` 里有一份同样的文件给网站用，看板的 `web/index.html` 也内联了 `favicon.svg`。改的时候一起改；不一致时 `web/brand_test.go` 会失败。

## 配色

品牌只有三色：黑、白、黄。叫“黄 / yellow”，不叫“金色”。

| 名称 | 色值 | 用途 |
|---|---|---|
| 黑 | `#0B0B0C` | 文字和一切能点的东西；浅色底上的外框和人的格子；深色背景 |
| 白 | `#F5F5F5`（面板 `#FFFFFF`） | 浅色背景；深色底上的外框、人的格子和字标 |
| 黄 | `#FFCC00` | 只做色块和标记，不当文字：智能体格子、一屏里唯一的主按钮、当前导航下划线、“轮到你”的左边条 |
| 碳灰 | `#1C1C1F` | 深色次级表面 |

黄色在白底上只有约 1.5:1，所以浅色底上从不拿它当文字；文字强调一律用黑（深色主题用白）。黄色底上，智能体格子改成白色。只能用一种颜色时，整个标志同色。

绿色和红色不是品牌色，产品里只作为状态色保留（好的结束、坏的结束或要人注意）。

## 工具色

产品用一个小六边形标出每一步是哪个编程工具做的。颜色只上图标，旁边的工具名用正文颜色。工具色不受黑白黄的约束。

| 工具 | 浅色主题 | 深色主题 | 来源 |
|---|---|---|---|
| Claude Code | `#D97757` | `#D97757` | Anthropic 公开的品牌橙 |
| Codex | `#000000` | `#FFFFFF` | OpenAI 的黑 / 白；Codex 没有专属色 |
| 其他工具（opencode、Cursor、Gemini CLI 等） | `#8A8A8A` | `#8A8A92` | 收录进本表之前一律用灰色 |

色值在 `web/src/styles.css` 的 `--tool-claude`、`--tool-codex`、`--tool-other` 里。图标对卡片底色至少 3:1，由 `web/src/contrast.test.ts` 检查。新增一个工具，就在本表加一行、在样式表加一个令牌。

## 尺寸与安全距离

- 标志最小 16 像素。
- 字标最小 72 像素宽；组合版缩到字标不够 72 像素时，只用标志。
- 四周至少留出标志宽度的四分之一。

## 不要这样用

- 不要旋转，缺口永远朝右。
- 不要把外框涂成黄色，不要交换人和智能体格子的颜色。
- 不要拉伸、斜切、描边或加效果（阴影、渐变、发光）。
- 不要用字体重新打出字标，一律用 SVG。
- 不得使用兰博基尼的公牛标志、盾徽及其专有字体，也不要放在标志旁边。不借用任何第三方的标志、商标或专有字体。

## 许可

这些 SVG 随本仓库以 [Apache License 2.0](../../LICENSE) 发布。该许可不授予商标权：名称和标志用于指代 Cohive，不得用来暗示对其他产品的背书。
