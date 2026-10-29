import { expect, it } from "vitest";
import {
  canAdminWorkspace,
  LAST_WORKSPACE_KEY,
  pickWorkspace,
  readLastWorkspace,
  rememberWorkspace,
  switchWorkspacePath,
  workspaceFromPath,
  wsPath,
} from "./workspace";

const list = [
  { name: "pay", role: "member" },
  { name: "ops", role: "admin" },
];

it("首页去上次用的工作空间；不再是成员或没有记录时去第一个；一个都没有时为空", () => {
  expect(pickWorkspace(list, "ops")).toBe("ops");
  expect(pickWorkspace(list, "gone")).toBe("pay");
  expect(pickWorkspace(list, null)).toBe("pay");
  expect(pickWorkspace([], "ops")).toBeNull();
  expect(pickWorkspace(undefined, null)).toBeNull();
});

it("上次用的工作空间读写失败时不影响使用", () => {
  const store = new Map<string, string>();
  const ok = {
    getItem: (k: string) => store.get(k) ?? null,
    setItem: (k: string, v: string) => void store.set(k, v),
  };
  expect(readLastWorkspace(ok)).toBeNull();
  rememberWorkspace("ops", ok);
  expect(store.get(LAST_WORKSPACE_KEY)).toBe("ops");
  expect(readLastWorkspace(ok)).toBe("ops");

  const broken = {
    getItem: () => {
      throw new Error("禁用");
    },
    setItem: () => {
      throw new Error("已满");
    },
  };
  expect(readLastWorkspace(broken)).toBeNull();
  expect(() => rememberWorkspace("ops", broken)).not.toThrow();
  expect(readLastWorkspace(null)).toBeNull();
  expect(() => rememberWorkspace("ops", null)).not.toThrow();
});

it("工作空间地址与从地址取工作空间名", () => {
  expect(wsPath("pay")).toBe("/w/pay/");
  expect(wsPath("pay", "issues")).toBe("/w/pay/issues");
  expect(wsPath("pay", "/issues/3")).toBe("/w/pay/issues/3");
  expect(workspaceFromPath("/w/pay/issues/3")).toBe("pay");
  expect(workspaceFromPath("/w/pay")).toBe("pay");
  expect(workspaceFromPath("/me")).toBeNull();
  expect(workspaceFromPath("/w/%E0%A4%A")).toBeNull();
});

it("切换工作空间时停在同类页面：问题单 → 问题单列表，管理页 → 管理页（有权限时），其余 → 看板", () => {
  expect(switchWorkspacePath("/w/pay/", "ops", true)).toBe("/w/ops/");
  expect(switchWorkspacePath("/w/pay/issues", "ops", false)).toBe("/w/ops/issues");
  expect(switchWorkspacePath("/w/pay/issues/12", "ops", false)).toBe("/w/ops/issues");
  expect(switchWorkspacePath("/w/pay/admin", "ops", true)).toBe("/w/ops/admin");
  expect(switchWorkspacePath("/w/pay/admin", "ops", false)).toBe("/w/ops/");
  expect(switchWorkspacePath("/me", "ops", true)).toBe("/w/ops/");
});

it("管理页：本空间管理员或服务器管理员才能进", () => {
  const me = (server_admin: boolean) => ({ account: { server_admin }, workspaces: list });
  expect(canAdminWorkspace(me(false), "ops")).toBe(true);
  expect(canAdminWorkspace(me(false), "pay")).toBe(false);
  expect(canAdminWorkspace(me(false), "other")).toBe(false);
  expect(canAdminWorkspace(me(true), "other")).toBe(true);
  expect(canAdminWorkspace(undefined, "ops")).toBe(false);
});

it("默认工作空间不选已归档的，上次用的已归档时也跳过", () => {
  const list = [
    { name: "old", role: "admin", archived: true },
    { name: "pay", role: "member" },
  ];
  expect(pickWorkspace(list, "old")).toBe("pay");
  expect(pickWorkspace(list.slice(0, 1), null)).toBeNull();
});
