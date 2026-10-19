# Cohive brand

[中文](README.zh-CN.md)

The Cohive mark is a hexagon with its right edge missing: a hive cell and the letter C. The bone-white cell inside is the person who decides; the gold cell at the opening is the agent plugging in. The mark and the wordmark are drawn from plain geometry, so none of the SVGs depends on a font.

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

`site/assets/brand/` holds a copy of these files for the site. Change both together.

## Colors

| Name | Hex | Role |
|---|---|---|
| Black | `#0B0B0C` | Primary background; frame and person cell on light backgrounds |
| Gold | `#FFC000` | The only accent. The agent cell, or the single primary button on a screen |
| Bone | `#F4F3EE` | Frame, person cell and wordmark on dark backgrounds |
| Carbon | `#1C1C1F` | Secondary surfaces |

On a gold background the agent cell turns bone white. In one-color use the whole mark is one color.

## Size and clear space

- Mark: at least 16 px.
- Wordmark: at least 72 px wide. Where a lockup would make it narrower, use the mark alone.
- Keep at least ¼ of the mark's width clear on every side.

## Don't

- Rotate the mark. The opening always faces right.
- Make the frame gold, or swap the colors of the person and agent cells.
- Stretch, skew, outline or add effects (shadows, gradients, glows).
- Redraw the wordmark in a typeface; use the SVG.
- Use the Lamborghini bull emblem, its shields, or Lamborghini's proprietary typefaces anywhere near the mark. The black-and-gold style is a reference, not a borrowing: no third-party logos, trademarks or proprietary fonts.

## License

The SVG files are part of this repository under the [Apache License 2.0](../../LICENSE). The license does not grant trademark rights: use the name and the mark to refer to Cohive, not to suggest endorsement of another product.
