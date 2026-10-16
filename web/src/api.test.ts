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
