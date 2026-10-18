import { afterEach, expect, it, vi } from "vitest";
import { api, ApiError } from "./api";

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

// 服务一直不响应：15 秒后按网络错误结束，不能一直等。
it("请求 15 秒没有响应就超时", async () => {
  vi.useFakeTimers();
  vi.stubGlobal(
    "fetch",
    (_: string, init: RequestInit) =>
      new Promise((_resolve, reject) => {
        init.signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")));
      }),
  );
  let settled: unknown = "未结束";
  const p = api.me().then(
    () => (settled = "成功"),
    (e) => (settled = e),
  );
  await vi.advanceTimersByTimeAsync(14_999);
  expect(settled).toBe("未结束");
  await vi.advanceTimersByTimeAsync(1);
  await p;
  expect(settled).toBeInstanceOf(ApiError);
  expect((settled as ApiError).status).toBe(0);
  expect((settled as ApiError).message).toContain("超时");
});

it("请求带上界面语言，服务端据此决定报错语言；英文界面的兜底报错也是英文", async () => {
  const i18n = (await import("./i18n")).default;
  await i18n.changeLanguage("en");
  try {
    let lang = "";
    vi.stubGlobal("fetch", async (_: string, init: RequestInit) => {
      lang = (init.headers as Record<string, string>)["Accept-Language"];
      return new Response("{}", { status: 503, headers: { "Content-Type": "application/json" } });
    });
    const err = await api.me().catch((e) => e);
    expect(lang).toBe("en");
    expect((err as ApiError).message).toBe("Request failed (503)");
  } finally {
    await i18n.changeLanguage("zh-CN");
  }
});

it("注册请求带上当前界面语言，服务端写到新账号上", async () => {
  const i18n = (await import("./i18n")).default;
  await i18n.changeLanguage("en");
  try {
    let body: Record<string, unknown> = {};
    vi.stubGlobal("fetch", async (_: string, init: RequestInit) => {
      body = JSON.parse(String(init.body));
      return new Response(JSON.stringify({ status: "pending" }), { status: 200, headers: { "Content-Type": "application/json" } });
    });
    await api.register({ code: "c", name: "newbie", display_name: "", password: "newbie-password-1" });
    expect(body.language).toBe("en");
  } finally {
    await i18n.changeLanguage("zh-CN");
  }
});
