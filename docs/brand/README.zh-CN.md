# Cohive 品牌标志

[English](README.md)

Cohive 的标志是一枚缺了右边一条边的六边形：既是蜂巢的一个格子，也是字母 C。内侧的骨白格子是拍板的人，缺口处的金色格子是正在接进来的智能体。标志和字标都由几何图形画成，所有 SVG 都不依赖字体。

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

`site/assets/brand/` 里有一份同样的文件给网站用，改的时候两边一起改。

## 配色

| 名称 | 色值 | 用途 |
|---|---|---|
| 夜黑 | `#0B0B0C` | 主背景；浅色底上的外框和人的格子 |
| 金黄 | `#FFC000` | 唯一的强调色：智能体格子，或一屏里唯一的主按钮 |
| 骨白 | `#F4F3EE` | 深色底上的外框、人的格子和字标 |
| 碳灰 | `#1C1C1F` | 次级表面 |

金色底上，智能体格子改成骨白。只能用一种颜色时，整个标志同色。

## 尺寸与安全距离

- 标志最小 16 像素。
- 字标最小 72 像素宽；组合版缩到字标不够 72 像素时，只用标志。
- 四周至少留出标志宽度的四分之一。

## 不要这样用

- 不要旋转，缺口永远朝右。
- 不要把外框涂成金色，不要交换人和智能体格子的颜色。
- 不要拉伸、斜切、描边或加效果（阴影、渐变、发光）。
- 不要用字体重新打出字标，一律用 SVG。
- 不得使用兰博基尼的公牛标志、盾徽及其专有字体，也不要放在标志旁边。黑金风格只是参考，不借用任何第三方的标志、商标或专有字体。

## 许可

这些 SVG 随本仓库以 [Apache License 2.0](../../LICENSE) 发布。该许可不授予商标权：名称和标志用于指代 Cohive，不得用来暗示对其他产品的背书。
