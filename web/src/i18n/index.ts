// 网页多语言：语言包在 src/locales/<语言代码>/<命名空间>.json（i18next JSON v4），英文是翻译基准。
// 语言的决定：本机设置（localStorage）> 浏览器语言 > 英文。方案见 docs/superpowers/plans/2026-10-18-i18n-community.md。
import i18n from "i18next";
import { initReactI18next } from "react-i18next";

type Catalog = Record<string, unknown>;

const files = import.meta.glob<Catalog>("../locales/*/*.json", { eager: true, import: "default" });

export const resources: Record<string, Record<string, Catalog>> = {};
for (const [path, data] of Object.entries(files)) {
  const m = path.match(/locales\/([^/]+)\/([^/]+)\.json$/);
  if (m) (resources[m[1]] ??= {})[m[2]] = data;
}

// 英文排第一（匹配不到时的回退），简体中文第二，其余按代码排序
const rank = (l: string) => (l === "en" ? "0" : l === "zh-CN" ? "1" : "2" + l);
export const languages = Object.keys(resources).sort((a, b) => rank(a).localeCompare(rank(b)));
export const namespaces = [...new Set(Object.values(resources).flatMap((r) => Object.keys(r)))].sort();

export const STORAGE_KEY = "cohive.lang";

// matchLanguage 把浏览器的语言偏好匹配到已有的语言：先比完整代码，再比主语言（zh-TW、zh-Hans 都归到 zh-CN）。
export function matchLanguage(prefs: readonly string[]): string | undefined {
  const lower = languages.map((l) => l.toLowerCase());
  for (const p of prefs) {
    const i = lower.indexOf(p.toLowerCase());
    if (i >= 0) return languages[i];
    const base = p.toLowerCase().split(/[-_]/)[0];
    const j = lower.findIndex((l) => l.split("-")[0] === base);
    if (j >= 0) return languages[j];
  }
  return undefined;
}

function stored(): string | null {
  try {
    return localStorage.getItem(STORAGE_KEY);
  } catch {
    return null;
  }
}

export function detectLanguage(): string {
  const saved = stored();
  if (saved && languages.includes(saved)) return saved;
  const prefs = typeof navigator === "undefined" ? [] : navigator.languages ?? [navigator.language];
  return matchLanguage(prefs) ?? "en";
}

// setLanguage 切换界面语言并记在本机
export async function setLanguage(lang: string) {
  try {
    localStorage.setItem(STORAGE_KEY, lang);
  } catch {
    // 隐私模式下存不了，只影响本次
  }
  await i18n.changeLanguage(lang);
}

// followBrowserLanguage 去掉本机记住的选择，界面语言回到浏览器语言（账号没设语言时用）
export async function followBrowserLanguage() {
  try {
    localStorage.removeItem(STORAGE_KEY);
  } catch {
    // 隐私模式下读写不了，只影响本次
  }
  await i18n.changeLanguage(detectLanguage());
}

// useAccountLanguage 在读到账号信息后，账号设了语言且与当前界面语言不同，就切换界面语言（写本机）。
// 只在账号语言本身变化时才比对，所以本机上的其他改动（登录页的语言选择）不会被反复覆盖，也不会循环。
export function applyAccountLanguage(accountLanguage: string | undefined) {
  if (!accountLanguage || !languages.includes(accountLanguage)) return;
  if (i18n.resolvedLanguage === accountLanguage) return;
  void setLanguage(accountLanguage);
}

i18n.on("languageChanged", (lang) => {
  if (typeof document === "undefined") return;
  document.documentElement.lang = lang;
  document.title = i18n.t("app.title");
});

void i18n.use(initReactI18next).init({
  resources,
  lng: detectLanguage(),
  fallbackLng: "en",
  supportedLngs: languages,
  defaultNS: "common",
  ns: namespaces,
  interpolation: { escapeValue: false }, // React 自己会转义
  returnEmptyString: false,
});

export default i18n;
