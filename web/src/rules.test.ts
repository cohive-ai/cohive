import { expect, it } from "vitest";
import { ApiError } from "./api";
import {
  adminNeedsPasswordLogin,
  authorizeAsText,
  deviceScopeText,
  inviteLookupFailure,
  isTelegramLink,
  revokeDeviceMessage,
  shouldRetryPoll,
} from "./rules";

it("只有账号密码登录才能进入管理页", () => {
  expect(adminNeedsPasswordLogin({ login_method: "password" })).toBe(false);
  expect(adminNeedsPasswordLogin({ login_method: "device" })).toBe(true);
  expect(adminNeedsPasswordLogin({ login_method: "token" })).toBe(true);
  expect(adminNeedsPasswordLogin({})).toBe(true);
});

it("授权页写明以哪个账号授权", () => {
  expect(authorizeAsText({ name: "ann", display_name: "Ann" })).toBe("将以账号 ann（Ann） 授权这台电脑。");
  expect(authorizeAsText({ name: "lee" })).toBe("将以账号 lee 授权这台电脑。");
  expect(authorizeAsText({ name: "lee", display_name: "lee" })).toBe("将以账号 lee 授权这台电脑。");
});

it("授权页写明批准后这台电脑可用于哪些工作空间", () => {
  expect(deviceScopeText(["pay", "ops"])).toBe("批准后这台电脑可用于：pay、ops。");
  expect(deviceScopeText([])).toContain("不在任何工作空间");
  expect(deviceScopeText(undefined)).toContain("不在任何工作空间");
});

it("吊销提示区分是否为唯一在用的设备", () => {
  const only = revokeDeviceMessage("mac", true);
  const more = revokeDeviceMessage("mac", false);
  expect(only).toContain("唯一在用");
  expect(only).toContain("管理员批准");
  expect(more).not.toContain("唯一在用");
  expect(more).toContain("cohive login");
  expect(revokeDeviceMessage("", false)).toContain("未命名");
});

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

it("查询邀请时 429 是请求过频，不是邀请无效", () => {
  expect(inviteLookupFailure(new ApiError(429, "太频繁"))).toBe("busy");
  expect(inviteLookupFailure(new ApiError(404, "无效"))).toBe("invalid");
  expect(inviteLookupFailure(new ApiError(400, "格式不对"))).toBe("invalid");
  expect(inviteLookupFailure(new ApiError(0, "网络"))).toBe("error");
  expect(inviteLookupFailure(new ApiError(502, "网关"))).toBe("error");
});
