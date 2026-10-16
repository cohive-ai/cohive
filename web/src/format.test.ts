import { expect, it } from "vitest";
import { memberNamePattern, safeRedirect, tokensByWorkspace } from "./format";

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
