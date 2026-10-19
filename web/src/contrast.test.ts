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

// 文字令牌 × 它会出现的背景（文字至少 4.5:1）
const onPage = ["ink", "muted", "accent", "teal", "wait", "danger"];
const pages = ["bg", "surface", "sunken"];
const pairs: [string, string][] = [
  ...onPage.flatMap((fg) => pages.map((bg) => [fg, bg] as [string, string])),
  // 实色状态标签：待验证与已重开、已接单与修复中、待接单、已关闭与有效、已退回与已停用
  ["accent-ink", "wait-mark"],
  ["surface", "ink"],
  ["on-status", "teal"],
  ["on-status", "danger"],
  // 浅底标签、荧光笔底与提示框
  ["wait", "wait-soft"],
  ["teal", "teal-soft"],
  ["ink", "teal-soft"],
  ["ink", "wait-soft"],
  ["ink", "accent-soft"],
  ["accent", "accent-soft"],
  // 主按钮
  ["accent-ink", "accent-fill"],
];

// 图形（非文字）至少 3:1：输入框与次按钮的边框、工具图标。工具图标放在卡片和信息栏的白底上
const graphics: [string, string][] = [
  ["line-strong", "surface"],
  ["tool-claude", "surface"],
  ["tool-codex", "surface"],
  ["tool-other", "surface"],
];

describe.each([
  ["light", light],
  ["dark", dark],
])("%s theme", (_, vars) => {
  it.each(pairs)("text %s on %s is at least 4.5:1", (fg, bg) => {
    expect(vars[fg], fg).toBeDefined();
    expect(vars[bg], bg).toBeDefined();
    expect(contrast(vars[fg], vars[bg])).toBeGreaterThanOrEqual(4.5);
  });
  it.each(graphics)("graphic %s on %s is at least 3:1", (fg, bg) => {
    expect(vars[fg], fg).toBeDefined();
    expect(vars[bg], bg).toBeDefined();
    expect(contrast(vars[fg], vars[bg])).toBeGreaterThanOrEqual(3);
  });
});

it("the dark theme is the same whether chosen by the system or by data-theme", () => {
  expect(darkAuto).toEqual(dark);
});

it("dark overrides every light color except the fixed brand yellow", () => {
  const { "brand-yellow": yellow, ...rest } = light;
  expect(yellow).toBe("#ffcc00");
  expect(Object.keys(dark).sort()).toEqual(Object.keys(rest).sort());
});
