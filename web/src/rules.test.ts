import { expect, it } from "vitest";
import { ApiError } from "./api";
import { isTelegramLink, shouldRetryPoll } from "./rules";

it("轮询只对网络错误和服务端 5xx 重试", () => {
  expect(shouldRetryPoll(new ApiError(0, "网络"))).toBe(true);
  expect(shouldRetryPoll(new ApiError(500, "内部错误"))).toBe(true);
  expect(shouldRetryPoll(new ApiError(503, "暂不可用"))).toBe(true);
  expect(shouldRetryPoll(new ApiError(404, "不存在"))).toBe(false);
  expect(shouldRetryPoll(new ApiError(403, "无权"))).toBe(false);
  expect(shouldRetryPoll(new ApiError(400, "请求不对"))).toBe(false);
  expect(shouldRetryPoll(new ApiError(429, "太频繁"))).toBe(false);
  expect(shouldRetryPoll(new Error("未知"))).toBe(true);
});

it("只接受 https://t.me/ 开头的机器人连接链接", () => {
  expect(isTelegramLink("https://t.me/cohive_bot?start=ln_abcDEF-12_x")).toBe(true);
  expect(isTelegramLink("http://t.me/cohive_bot?start=ln_abc")).toBe(false);
  expect(isTelegramLink("https://evil.example.com/?start=ln_abc")).toBe(false);
  expect(isTelegramLink("https://t.me.evil.com/cohive_bot?start=ln_abc")).toBe(false);
  expect(isTelegramLink("javascript:alert(1)")).toBe(false);
  expect(isTelegramLink("https://t.me/cohive_bot?start=da_abc")).toBe(false);
  expect(isTelegramLink(undefined)).toBe(false);
});
