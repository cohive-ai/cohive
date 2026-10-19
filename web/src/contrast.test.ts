import { describe, expect, it } from "vitest";
import css from "./styles.css?raw";

// 颜色令牌的对比度检查：文字与它所在的背景至少达到 WCAG AA（正文 4.5:1）

function block(selector: string): Record<string, string> {
  const start = css.indexOf(selector + " {");
  if (start < 0) throw new Error(`missing block ${selector}`);
  const body = css.slice(css.indexOf("{", start) + 1, css.indexOf("}", start));
  const vars: Record<string, string> = {};
  for (const m of body.matchAll(/--([a-z-]+):\s*(#[0-9a-f]{6});/gi)) vars[m[1]] = m[2].toLowerCase();
  return vars;
}

function luminance(hex: string): number {
  const [r, g, b] = [1, 3, 5].map((i) => {
    const c = parseInt(hex.slice(i, i + 2), 16) / 255;
    return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

export function contrast(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

const light = block(":root");
const dark = block(':root[data-theme="dark"]');
const darkAuto = block(':root:not([data-theme="light"])');

// 文字令牌 × 它会出现的背景
const onPage = ["ink", "muted", "accent", "teal", "wait", "danger", "tool-claude", "tool-codex"];
const pages = ["bg", "surface", "sunken"];
const pairs: [string, string][] = [
  ...onPage.flatMap((fg) => pages.map((bg) => [fg, bg] as [string, string])),
  ["teal", "teal-soft"],
  ["wait", "wait-soft"],
  ["ink", "teal-soft"],
  ["ink", "wait-soft"],
  ["ink", "accent-soft"],
  ["accent", "accent-soft"],
  ["accent-ink", "accent-fill"],
  ["surface", "danger"],
];

describe.each([
  ["light", light],
  ["dark", dark],
])("%s theme", (_, vars) => {
  it.each(pairs)("%s on %s is at least 4.5:1", (fg, bg) => {
    expect(vars[fg], fg).toBeDefined();
    expect(vars[bg], bg).toBeDefined();
    expect(contrast(vars[fg], vars[bg])).toBeGreaterThanOrEqual(4.5);
  });
});

it("the dark theme is the same whether chosen by the system or by data-theme", () => {
  expect(darkAuto).toEqual(dark);
});

it("dark overrides every light token except the fixed brand gold", () => {
  const { "brand-gold": gold, ...rest } = light;
  expect(gold).toBe("#ffc000");
  expect(Object.keys(dark).sort()).toEqual(Object.keys(rest).sort());
});

it("the waiting color stays clearly apart from the brand gold", () => {
  // 色相相差至少 90 度：等你处理不能被看成“可点的金色”
  const hue = (hex: string) => {
    const [r, g, b] = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255);
    const max = Math.max(r, g, b);
    const min = Math.min(r, g, b);
    const d = max - min;
    const h = max === r ? ((g - b) / d) % 6 : max === g ? (b - r) / d + 2 : (r - g) / d + 4;
    return (h * 60 + 360) % 360;
  };
  for (const vars of [light, dark]) {
    const diff = Math.abs(hue(vars.wait) - hue(vars.accent));
    expect(Math.min(diff, 360 - diff)).toBeGreaterThanOrEqual(90);
  }
});
