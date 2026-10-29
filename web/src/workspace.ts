// 工作空间相关的纯函数：上次用的工作空间、切换时去哪一页。单独放出来便于测试。
import type { WorkspaceRef } from "./api";

export const LAST_WORKSPACE_KEY = "cohive.lastWorkspace";

type Reader = Pick<Storage, "getItem">;
type Writer = Pick<Storage, "setItem">;

// 浏览器本地存储可能不可用（隐私模式、被禁用），读写失败一律当作没有记录，不影响使用。
function defaultStorage(): Storage | null {
  try {
    return window.localStorage;
  } catch {
    return null;
  }
}

export function readLastWorkspace(storage: Reader | null = defaultStorage()): string | null {
  try {
    const v = storage?.getItem(LAST_WORKSPACE_KEY);
    return v ? v : null;
  } catch {
    return null;
  }
}

export function rememberWorkspace(ws: string, storage: Writer | null = defaultStorage()): void {
  try {
    storage?.setItem(LAST_WORKSPACE_KEY, ws);
  } catch {
    // 记不下来也不影响
  }
}

// 进入站点时去哪个工作空间：上次用的（仍是成员且未归档时），否则列表第一个；一个都没有时返回 null。
export function pickWorkspace(list: WorkspaceRef[] | null | undefined, last: string | null): string | null {
  const items = (list ?? []).filter((w) => !w.archived);
  if (last && items.some((w) => w.name === last)) return last;
  return items[0]?.name ?? null;
}

// 工作空间内页面的地址
export const wsPath = (ws: string, rest = "") => `/w/${encodeURIComponent(ws)}/${rest.replace(/^\/+/, "")}`;

// 从当前地址取工作空间名（不在工作空间页面时返回 null）
export function workspaceFromPath(pathname: string): string | null {
  const m = /^\/w\/([^/]+)/.exec(pathname);
  if (!m) return null;
  try {
    return decodeURIComponent(m[1]);
  } catch {
    return null;
  }
}

// 顶栏切换工作空间时去哪一页：问题单列表与详情 → 新空间的问题单列表；管理页 → 新空间的管理页（有权限时）；
// 其余（看板、账号级页面）→ 新空间的看板。
export function switchWorkspacePath(pathname: string, ws: string, canAdmin: boolean): string {
  const rest = /^\/w\/[^/]+\/?(.*)$/.exec(pathname)?.[1] ?? "";
  if (rest === "issues" || rest.startsWith("issues/")) return wsPath(ws, "issues");
  if ((rest === "admin" || rest.startsWith("admin/")) && canAdmin) return wsPath(ws, "admin");
  return wsPath(ws);
}

// 能否打开某个工作空间的管理页：该空间的管理员，或服务器管理员
export function canAdminWorkspace(
  me: { account: { server_admin: boolean }; workspaces: WorkspaceRef[] } | undefined,
  ws: string,
): boolean {
  if (!me) return false;
  if (me.account.server_admin) return true;
  return me.workspaces.some((w) => w.name === ws && w.role === "admin");
}
