import { describe, expect, it } from "vitest";
import css from "./theme.css?raw";
import spec from "../theme-spec.json";
import vectors from "../theme-cases.json";

// 默认主题的对比度检查。变量清单与对比度规则在 web/theme-spec.json，
// cohive-server theme check（internal/theme）读同一份；web/theme-cases.json 是两边都要跑出相同结论的用例。

type Vars = Record<string, string>;

function block(selector: string): Vars {
  const start = css.indexOf(selector + " {");
  if (start < 0) throw new Error(`missing block ${selector}`);
  const body = css.slice(css.indexOf("{", start) + 1, css.indexOf("}", start));
  const vars: Vars = {};
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

type Pair = { fg: string; bg: string };
const lists: [string, Pair[], number][] = [
  ["text", spec.text, spec.minText],
  ["graphics", spec.graphics, spec.minGraphic],
];

// 按 CSS 层叠算出某一模式下的变量：浅色块的覆盖也作用于默认深色块没定义的变量
function resolve(mode: "light" | "dark", lightOver: Vars, darkOver: Vars): Vars {
  if (mode === "light") return { ...light, ...lightOver };
  const fromLight = Object.fromEntries(Object.entries(lightOver).filter(([k]) => !(k in dark)));
  return { ...light, ...fromLight, ...dark, ...darkOver };
}

function failing(lightOver: Vars, darkOver: Vars): string[] {
  const out: string[] = [];
  for (const mode of ["light", "dark"] as const) {
    const vars = resolve(mode, lightOver, darkOver);
    for (const [name, pairs, min] of lists) {
      for (const p of pairs) {
        if (contrast(vars[p.fg], vars[p.bg]) < min) out.push(`${mode}:${name}:${p.fg}/${p.bg}`);
      }
    }
  }
  return out;
}

describe.each([
  ["light", light],
  ["dark", dark],
])("default %s theme", (_, vars) => {
  for (const [name, pairs, min] of lists) {
    it.each(pairs.map((p) => [p.fg, p.bg]))(`${name} %s on %s reaches the minimum`, (fg, bg) => {
      expect(vars[fg], fg).toBeDefined();
      expect(vars[bg], bg).toBeDefined();
      expect(contrast(vars[fg], vars[bg])).toBeGreaterThanOrEqual(min);
    });
  }
});

it.each(vectors.cases.map((c) => [c.name, c] as const))("shared case: %s", (_, c) => {
  expect(failing(c.light as Vars, c.dark as Vars)).toEqual(c.failing);
});

it("the dark theme is the same whether chosen by the system or by data-theme", () => {
  expect(darkAuto).toEqual(dark);
});

it("dark overrides every light color except the fixed brand yellow", () => {
  const { "brand-yellow": yellow, ...rest } = light;
  expect(yellow).toBe("#ffcc00");
  expect(Object.keys(dark).sort()).toEqual(Object.keys(rest).sort());
});

it("the spec lists exactly the variables the default theme defines", () => {
  const all = [...css.matchAll(/^\s*--([a-z-]+):/gm)].map((m) => m[1]);
  expect(spec.tokens.map((t) => t.name).sort()).toEqual([...new Set(all)].sort());
  const colors = spec.tokens.filter((t) => t.kind === "color").map((t) => t.name);
  expect(colors.sort()).toEqual(Object.keys(light).sort());
});
