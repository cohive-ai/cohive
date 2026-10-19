# Cohive brand

[中文](README.zh-CN.md)

The Cohive mark is a hexagon with its right edge missing: a hive cell and the letter C. The cell inside is the person who decides; the yellow cell at the opening is the agent plugging in. The mark and the wordmark are drawn from plain geometry, so none of the SVGs depends on a font.

The full guide with examples is the [brand page](../../site/brand.html) of the site.

## Files

| File | Use |
|---|---|
| `cohive-lockup.svg` | Horizontal lockup (mark + wordmark) for light backgrounds. The default |
| `cohive-lockup-reverse.svg` | Horizontal lockup for dark backgrounds |
| `cohive-lockup-stacked.svg` | Stacked lockup for light backgrounds, for square spaces |
| `cohive-lockup-reverse-stacked.svg` | Stacked lockup for dark backgrounds |
| `cohive-mark.svg` | Mark only, for light backgrounds (avatars, app icons) |
| `cohive-mark-reverse.svg` | Mark only, for dark backgrounds |
| `cohive-mark-mono.svg` | Mark in one color, for single-color print, stamps and etching |
| `favicon.svg` | Browser icon: the reverse mark on a black square |

The README header uses the two horizontal lockups and lets GitHub pick by the viewer's theme:

```html
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/brand/cohive-lockup-reverse.svg">
  <img src="docs/brand/cohive-lockup.svg" alt="Cohive" width="320">
</picture>
```

`site/assets/brand/` holds a copy of these files for the site, and the board inlines `favicon.svg` in `web/index.html`. Change them together; `web/brand_test.go` fails when they drift apart.

## Colors

The brand has three colors: black, white and yellow. Call it yellow, never gold.

| Name | Hex | Role |
|---|---|---|
| Black | `#0B0B0C` | Text and anything you can act on; frame and person cell on light backgrounds; dark background |
| White | `#F5F5F5` (panels `#FFFFFF`) | Light background; frame, person cell and wordmark on dark backgrounds |
| Yellow | `#FFCC00` | Fills and marks only, never text: the agent cell, the single primary button, the current-tab underline, the "your turn" bar |
| Carbon | `#1C1C1F` | Secondary dark surfaces |

Yellow on a white background is only about 1.5:1, so it is never used as text there; emphasis in text is black (white in dark themes). On a yellow background the agent cell turns white. In one-color use the whole mark is one color.

Green and red are not brand colors. The product keeps them only as status colors (finished well, finished badly or needs attention).

## Tool colors

The product marks which coding tool did each step with a small hexagon. The color goes on the icon only; the tool name next to it is plain text color. These colors sit outside the black, white and yellow rule.

| Tool | Light theme | Dark theme | Source |
|---|---|---|---|
| Claude Code | `#D97757` | `#D97757` | Anthropic's public brand orange |
| Codex | `#000000` | `#FFFFFF` | OpenAI black / white; Codex has no color of its own |
| Any other tool (opencode, Cursor, Gemini CLI, …) | `#8A8A8A` | `#8A8A92` | Neutral gray until it is added here |

The values live in `web/src/styles.css` as `--tool-claude`, `--tool-codex` and `--tool-other`. Icons must reach 3:1 against the card background (`web/src/contrast.test.ts` checks it). To add a tool, add a row here and a token there.

## Size and clear space

- Mark: at least 16 px.
- Wordmark: at least 72 px wide. Where a lockup would make it narrower, use the mark alone.
- Keep at least ¼ of the mark's width clear on every side.

## Don't

- Rotate the mark. The opening always faces right.
- Make the frame yellow, or swap the colors of the person and agent cells.
- Stretch, skew, outline or add effects (shadows, gradients, glows).
- Redraw the wordmark in a typeface; use the SVG.
- Use the Lamborghini bull emblem, its shields, or Lamborghini's proprietary typefaces anywhere near the mark. No third-party logos, trademarks or proprietary fonts.

## License

The SVG files are part of this repository under the [Apache License 2.0](../../LICENSE). The license does not grant trademark rights: use the name and the mark to refer to Cohive, not to suggest endorsement of another product.
