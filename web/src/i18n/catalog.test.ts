import { expect, it } from "vitest";
import { languages, resources } from ".";

// 把嵌套的语言包拍平成 "a.b.c" → 文字
function flatten(obj: unknown, prefix = "", out: Record<string, string> = {}): Record<string, string> {
  if (typeof obj === "string") {
    out[prefix] = obj;
  } else if (obj && typeof obj === "object" && !Array.isArray(obj)) {
    for (const [k, v] of Object.entries(obj)) flatten(v, prefix ? `${prefix}.${k}` : k, out);
  } else {
    throw new Error(`${prefix}: values must be strings or objects`);
  }
  return out;
}

const pluralSuffix = /_(zero|one|two|few|many|other)$/;

// 按“命名空间:键（去掉复数后缀）”汇总每种语言的文案
function catalog(lang: string) {
  const out = new Map<string, { forms: Record<string, string> }>();
  for (const [ns, data] of Object.entries(resources[lang])) {
    for (const [key, text] of Object.entries(flatten(data))) {
      const m = key.match(pluralSuffix);
      const base = `${ns}:${m ? key.slice(0, -m[0].length) : key}`;
      const e = out.get(base) ?? { forms: {} };
      e.forms[m ? m[1] : "other"] = text;
      out.set(base, e);
    }
  }
  return out;
}

const placeholders = (forms: Record<string, string>) =>
  [...new Set(Object.values(forms).flatMap((s) => [...s.matchAll(/\{\{\s*([\w.]+)/g)].map((m) => m[1])))].sort();

const en = catalog("en");

it("en 与 zh-CN 的键完全一致，其他语言不能多出英文里没有的键，占位符一致", () => {
  const problems: string[] = [];
  for (const lang of languages) {
    const cat = catalog(lang);
    for (const [key, e] of cat) {
      const base = en.get(key);
      if (!base) {
        problems.push(`${lang}: ${key} is not in en`);
        continue;
      }
      const got = placeholders(e.forms).join(",");
      const want = placeholders(base.forms).join(",");
      if (got !== want) problems.push(`${lang}: ${key} uses {{${got}}}, en uses {{${want}}}`);
      if (!("other" in e.forms)) problems.push(`${lang}: ${key} needs an _other form`);
    }
    if (lang === "en" || lang === "zh-CN") {
      for (const key of en.keys()) if (!cat.has(key)) problems.push(`${lang}: missing ${key}`);
    }
  }
  for (const [key, e] of en) {
    if (Object.keys(e.forms).length > 1 && !("one" in e.forms)) problems.push(`en: plural ${key} needs _one`);
  }
  expect(problems).toEqual([]);
});

it("en、zh-CN 排在最前，每种语言都有自己的名称", () => {
  expect(languages.slice(0, 2)).toEqual(["en", "zh-CN"]);
  for (const lang of languages) expect(catalog(lang).get("common:language.name")).toBeTruthy();
});
