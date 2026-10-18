// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import i18n, { applyAccountLanguage, STORAGE_KEY } from ".";
import { MePage } from "../pages/Me";

beforeEach(async () => {
  localStorage.clear();
  await i18n.changeLanguage("zh-CN");
});

afterEach(async () => {
  cleanup();
  vi.unstubAllGlobals();
  localStorage.clear();
  await i18n.changeLanguage("zh-CN");
});

it("账号设了语言且与界面不同才切换并写本机；没设或相同时不动", async () => {
  applyAccountLanguage(undefined);
  applyAccountLanguage("");
  applyAccountLanguage("xx");
  expect(i18n.resolvedLanguage).toBe("zh-CN");
  expect(localStorage.getItem(STORAGE_KEY)).toBeNull();
  applyAccountLanguage("zh-CN");
  expect(localStorage.getItem(STORAGE_KEY)).toBeNull();
  applyAccountLanguage("en");
  await waitFor(() => expect(i18n.resolvedLanguage).toBe("en"));
  expect(localStorage.getItem(STORAGE_KEY)).toBe("en");
});

const me = (language?: string) => ({
  account: { id: 1, name: "ann", server_admin: false, disabled: false, has_password: true, language },
  must_change_password: false,
  tool: "web",
  workspaces: [],
  default_language: "zh-CN",
  languages: [
    { code: "en", name: "English" },
    { code: "zh-CN", name: "简体中文" },
  ],
});

it("个人中心：账号语言选项，改动时 POST /me/language 并切换界面、记到本机", async () => {
  const posts: unknown[] = [];
  let current = me();
  vi.stubGlobal("fetch", async (url: string, init: RequestInit) => {
    const json = (body: unknown) => new Response(JSON.stringify(body), { status: 200 });
    if (url === "/api/v1/me/language") {
      const body = JSON.parse(String(init.body)) as { language: string };
      posts.push(body);
      current = me(body.language || undefined);
      return new Response(null, { status: 204 });
    }
    if (url === "/api/v1/me") return json(current);
    if (url === "/api/v1/version") return json({ cli: null });
    if (url === "/api/v1/me/devices") return json({ items: [] });
    return json({});
  });
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <MePage />
    </QueryClientProvider>,
  );
  const select = (await screen.findByLabelText("账号语言")) as HTMLSelectElement;
  expect(select.value).toBe("");
  expect(screen.getByText(/未设置（网页跟随浏览器；编程工具、命令行用服务器缺省语言：简体中文）/)).toBeTruthy();
  fireEvent.change(select, { target: { value: "en" } });
  await waitFor(() => expect(i18n.resolvedLanguage).toBe("en"));
  expect(posts).toEqual([{ language: "en" }]);
  expect(localStorage.getItem(STORAGE_KEY)).toBe("en");
  expect(await screen.findByLabelText("Account language")).toBeTruthy();
  expect((screen.getByLabelText("Account language") as HTMLSelectElement).value).toBe("en");
  // 改回未设置：清掉本机记住的选择
  fireEvent.change(screen.getByLabelText("Account language"), { target: { value: "" } });
  await waitFor(() => expect(posts).toHaveLength(2));
  expect(posts[1]).toEqual({ language: "" });
  expect(localStorage.getItem(STORAGE_KEY)).toBeNull();
});
