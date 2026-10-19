import { expect, it } from "vitest";

// 主题守卫：组件里的颜色、圆角、阴影一律用 styles.css 的令牌，换主题只改样式表一处

const sources = import.meta.glob<string>("./**/*.{ts,tsx}", { query: "?raw", import: "default", eager: true });

const rules: [string, RegExp][] = [
  ["hex color", /#[0-9a-fA-F]{3,8}\b/],
  ["rgb()/hsl() color", /\b(rgba?|hsla?|oklch)\(/],
  [
    "Tailwind palette color",
    /\b(bg|text|border|ring|outline|fill|stroke|from|via|to|decoration|divide|accent|caret|shadow)-(white|black|slate|gray|zinc|neutral|stone|red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose)-[0-9]{2,3}\b/,
  ],
  ["literal white or black", /\b(bg|text|border|ring|fill|stroke)-(white|black)\b/],
  ["arbitrary shadow or radius", /\b(shadow|rounded)-\[/],
  ["fixed-size Tailwind shadow", /\bshadow-(2xs|xs|sm|md|lg|xl|2xl)\b/],
];

it("components take colors, radii and shadows from theme tokens only", () => {
  const hits: string[] = [];
  for (const [path, src] of Object.entries(sources)) {
    if (path.includes(".test.")) continue;
    src.split("\n").forEach((line, i) => {
      for (const [what, re] of rules) if (re.test(line)) hits.push(`${path}:${i + 1} ${what}: ${line.trim()}`);
    });
  }
  expect(hits).toEqual([]);
});
