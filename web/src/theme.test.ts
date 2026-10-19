// @vitest-environment jsdom
import { afterEach, expect, it } from "vitest";
import indexHtml from "../index.html?raw";
import { applyTheme, readThemePref, setThemePref, THEME_KEY } from "./theme";

afterEach(() => {
  localStorage.clear();
  delete document.documentElement.dataset.theme;
});

it("follows the system until the person picks light or dark", () => {
  expect(readThemePref()).toBe("system");
  localStorage.setItem(THEME_KEY, "purple");
  expect(readThemePref()).toBe("system");
});

it("saves the choice in this browser and sets data-theme", () => {
  setThemePref("dark");
  expect(localStorage.getItem(THEME_KEY)).toBe("dark");
  expect(document.documentElement.dataset.theme).toBe("dark");
  expect(readThemePref()).toBe("dark");

  setThemePref("light");
  expect(document.documentElement.dataset.theme).toBe("light");

  setThemePref("system");
  expect(localStorage.getItem(THEME_KEY)).toBeNull();
  expect(document.documentElement.dataset.theme).toBeUndefined();
});

it("applyTheme only touches data-theme", () => {
  applyTheme("dark");
  expect(document.documentElement.dataset.theme).toBe("dark");
  applyTheme("system");
  expect(document.documentElement.dataset.theme).toBeUndefined();
});

// index.html 里绘制前的那段脚本必须读同一个键、只认 light 和 dark
it("the pre-paint script in index.html matches theme.ts", () => {
  const script = indexHtml.slice(indexHtml.indexOf("<script>") + 8, indexHtml.indexOf("</script>"));
  expect(script).toContain(`localStorage.getItem("${THEME_KEY}")`);
  for (const [stored, want] of [
    ["dark", "dark"],
    ["light", "light"],
    ["purple", undefined],
  ] as const) {
    localStorage.setItem(THEME_KEY, stored);
    delete document.documentElement.dataset.theme;
    new Function(script)();
    expect(document.documentElement.dataset.theme).toBe(want);
  }
});
