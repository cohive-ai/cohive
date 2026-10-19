// 深浅主题：个人中心里选，存在本机浏览器（不跟账号走），默认跟随系统。
// index.html 里有一段同样逻辑的小脚本，在页面绘制前先套上，避免闪一下另一种颜色。

export type ThemePref = "system" | "light" | "dark";

export const THEME_KEY = "cohive-theme";

export function readThemePref(): ThemePref {
  try {
    const v = localStorage.getItem(THEME_KEY);
    return v === "light" || v === "dark" ? v : "system";
  } catch {
    return "system";
  }
}

// applyTheme 把选择写到 <html data-theme>：样式表里的深色块认这个属性；跟随系统时去掉它
export function applyTheme(pref: ThemePref): void {
  const root = document.documentElement;
  if (pref === "system") delete root.dataset.theme;
  else root.dataset.theme = pref;
}

export function setThemePref(pref: ThemePref): void {
  try {
    if (pref === "system") localStorage.removeItem(THEME_KEY);
    else localStorage.setItem(THEME_KEY, pref);
  } catch {
    // 无痕模式等存不了时，只对这次打开生效
  }
  applyTheme(pref);
}
