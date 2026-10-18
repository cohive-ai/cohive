import { afterEach, expect, it } from "vitest";
import i18n from "./i18n";
import {
  absoluteTime,
  actionLabel,
  deviceAuthStatusLabel,
  memberNamePattern,
  relativeTime,
  roleLabel,
  safeRedirect,
  severityLabel,
  statusLabel,
  statusOptions,
  toolsText,
  tokensByWorkspace,
  workspaceLabel,
  workspaceListText,
  workspaceName,
} from "./format";

afterEach(async () => {
  await i18n.changeLanguage("zh-CN");
});

it("工作空间名：显示名优先；与路径不同时一并写出路径", () => {
  expect(workspaceName({ name: "pay", display_name: "支付组" })).toBe("支付组");
  expect(workspaceName({ name: "pay", display_name: "" })).toBe("pay");
  expect(workspaceName({ name: "pay" })).toBe("pay");
  expect(workspaceLabel({ name: "pay", display_name: "支付组" })).toBe("支付组（pay）");
  expect(workspaceLabel({ name: "pay", display_name: "pay" })).toBe("pay");
  expect(workspaceLabel({ name: "pay" })).toBe("pay");
});

// 与服务端规则一致：小写字母、数字、下划线、横线，1 到 32 位，首字符为字母或数字。
it("登录名规则与服务端一致", () => {
  for (const ok of ["a", "lee", "lee-codex", "ann_dev", "9lives", "a".repeat(32)]) {
    expect(memberNamePattern.test(ok), ok).toBe(true);
  }
  for (const bad of ["", "-lee", "_lee", "Lee", "李", "a b", "a".repeat(33)]) {
    expect(memberNamePattern.test(bad), bad).toBe(false);
  }
});

it("登录后回跳只接受站内相对路径", () => {
  expect(safeRedirect("/i/abc")).toBe("/i/abc");
  expect(safeRedirect("/w/pay/issues/3?x=1#e")).toBe("/w/pay/issues/3?x=1#e");
  expect(safeRedirect(undefined)).toBe("/");
  expect(safeRedirect(null)).toBe("/");
  expect(safeRedirect("")).toBe("/");
  for (const bad of [
    "https://evil.com",
    "//evil.com",
    "/\\evil.com",
    "/\\/evil.com",
    "/\t/evil.com",
    "/\n/evil.com",
    "javascript:alert(1)",
    "evil.com",
    "/login",
    "/login?redirect=/x",
  ]) {
    expect(safeRedirect(bad), JSON.stringify(bad)).toBe("/");
  }
  expect(safeRedirect("//evil.com", "/me")).toBe("/me");
});

it("设备令牌按工作空间分组，工具去重", () => {
  expect(
    tokensByWorkspace([
      { workspace: "pay", tool: "claude-code" },
      { workspace: "ops", tool: "codex" },
      { workspace: "pay", tool: "codex" },
      { workspace: "pay", tool: "claude-code" },
    ]),
  ).toEqual([
    { workspace: "pay", tools: ["Claude Code", "Codex"] },
    { workspace: "ops", tools: ["Codex"] },
  ]);
  expect(tokensByWorkspace(null)).toEqual([]);
});

it("状态、动作、严重程度、角色等名称按当前语言取（中文）", () => {
  expect(statusLabel("READY_FOR_VERIFICATION")).toBe("待验证");
  expect(statusLabel("UNKNOWN", "服务端给的")).toBe("服务端给的");
  expect(statusLabel("UNKNOWN")).toBe("UNKNOWN");
  expect(statusOptions()[0]).toEqual({ value: "", label: "全部状态" });
  expect(statusOptions()).toHaveLength(9);
  expect(actionLabel("request_verification")).toBe("请求验证");
  expect(actionLabel("whatever")).toBe("whatever");
  expect(severityLabel("critical")).toBe("紧急");
  expect(roleLabel("admin")).toBe("管理员");
  expect(roleLabel("member")).toBe("成员");
  expect(toolsText(["claude-code", "web"])).toBe("Claude Code、网页");
  expect(toolsText("")).toBe("未注明");
  expect(workspaceListText([])).toBe("暂无");
  expect(workspaceListText(["pay", "ops"])).toBe("pay、ops");
  expect(deviceAuthStatusLabel["awaiting_admin"]).toBe("等待管理员批准");
  expect(deviceAuthStatusLabel["nope"]).toBeUndefined();
});

it("切到英文后名称、列表连接符、时间格式都跟着变", async () => {
  await i18n.changeLanguage("en");
  expect(statusLabel("READY_FOR_VERIFICATION")).toBe("Ready for verification");
  expect(statusOptions()[0].label).toBe("All statuses");
  expect(actionLabel("start")).toBe("started fixing");
  expect(severityLabel("high")).toBe("High");
  expect(roleLabel("admin")).toBe("Admin");
  expect(toolsText(["claude-code", "codex"])).toBe("Claude Code, Codex");
  expect(workspaceListText([])).toBe("None");
  expect(workspaceLabel({ name: "pay", display_name: "Payments" })).toBe("Payments (pay)");
  expect(deviceAuthStatusLabel["pending"]).toBe("Waiting for your confirmation");

  const now = Date.parse("2026-10-17T06:10:00Z");
  expect(relativeTime("2026-10-17T06:05:00Z", now)).toBe("5 minutes ago");
  expect(relativeTime("2026-10-17T03:10:00Z", now)).toBe("3 hours ago");
  expect(relativeTime("2026-10-17T06:09:58Z", now)).toBe("just now");
  expect(absoluteTime("2026-10-17T06:05:00Z")).toMatch(/Oct 17, 2026/);
});

it("中文下的时间写法", () => {
  const now = Date.parse("2026-10-17T06:10:00Z");
  expect(relativeTime("2026-10-17T06:05:00Z", now)).toBe("5分钟前");
  expect(relativeTime("2026-10-14T06:10:00Z", now)).toBe("3天前");
  expect(absoluteTime("2026-10-17T06:05:00Z")).toMatch(/2026年10月17日/);
});
