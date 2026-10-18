import { expect, it } from "vitest";

// 非测试源码里注释之外不许直接写中文，文案一律进语言包。确有必要的行在同一行写注释 i18n-ignore。
const sources = import.meta.glob<string>("../**/*.{ts,tsx}", { query: "?raw", import: "default", eager: true });

// 第二段才改的文件（在另一条分支上），第二段清空
const pending = ["../pages/Admin.tsx"];

const cjk = /[　-〿一-鿿＀-￯]/;

// stripComments 去掉 // 与 /* */ 注释，保留字符串、模板字符串和 JSX 文本；带 i18n-ignore 的行整行去掉
export function stripComments(src: string): string {
  let out = "";
  let i = 0;
  let quote = "";
  while (i < src.length) {
    const c = src[i];
    const next = src[i + 1];
    if (quote) {
      out += c;
      if (c === "\\") {
        out += next ?? "";
        i += 2;
        continue;
      }
      if (c === quote) quote = "";
      i++;
    } else if (c === "/" && next === "/") {
      const end = src.indexOf("\n", i);
      i = end < 0 ? src.length : end;
    } else if (c === "/" && next === "*") {
      const end = src.indexOf("*/", i + 2);
      const body = src.slice(i, end < 0 ? src.length : end + 2);
      out += body.replace(/[^\n]/g, ""); // 保留换行，行号不变
      i += body.length;
    } else {
      if (c === '"' || c === "'" || c === "`") quote = c;
      out += c;
      i++;
    }
  }
  return out;
}

it("stripComments 去掉注释、保留字符串", () => {
  expect(stripComments('const a = "中文"; // 注释\n/* 块\n注释 */ b')).toBe('const a = "中文"; \n\n b');
  expect(stripComments("const u = 'http://x'; // 说明")).toBe("const u = 'http://x'; ");
});

it("非测试源码不硬编码中文", () => {
  const hits: string[] = [];
  for (const [path, src] of Object.entries(sources)) {
    if (/\.test\.tsx?$/.test(path) || /\.d\.ts$/.test(path) || pending.includes(path)) continue;
    const ignored = new Set(src.split("\n").flatMap((l, n) => (l.includes("i18n-ignore") ? [n] : [])));
    stripComments(src)
      .split("\n")
      .forEach((line, n) => {
        if (!ignored.has(n) && cjk.test(line)) hits.push(`${path.slice(3)}:${n + 1}: ${line.trim().slice(0, 80)}`);
      });
  }
  expect(hits).toEqual([]);
});
