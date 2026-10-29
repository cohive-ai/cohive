// @vitest-environment jsdom
import { afterEach, beforeAll, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider, createMemoryHistory, createRootRoute, createRouter } from "@tanstack/react-router";
import type { ServerWorkspace, WorkspaceSettings } from "../api";
import "../i18n";
import { ArchiveSection } from "./Admin";
import { WorkspacesTab } from "./Server";

beforeAll(() => {
  HTMLDialogElement.prototype.showModal = function (this: HTMLDialogElement) {
    this.setAttribute("open", "");
  };
  HTMLDialogElement.prototype.close = function (this: HTMLDialogElement) {
    if (!this.hasAttribute("open")) return;
    this.removeAttribute("open");
    this.dispatchEvent(new Event("close"));
  };
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const json = (status: number, body: unknown) =>
  new Response(status === 204 ? null : JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const settings = (over: Partial<WorkspaceSettings> = {}): WorkspaceSettings =>
  ({ name: "pay", path: "pay", display_name: "pay", open_issues: 0, archived: false, ...over }) as WorkspaceSettings;

function renderArchive(initial: WorkspaceSettings) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <ArchiveSection ws="pay" initial={initial} />
    </QueryClientProvider>,
  );
}

it("归档：有未结束单子时须勾选才能提交，带上确认数", async () => {
  const bodies: unknown[] = [];
  vi.stubGlobal("fetch", async (url: string, init: RequestInit) => {
    if (url === "/api/v1/w/pay/admin/archive") {
      bodies.push(JSON.parse(String(init.body)));
      return json(204, null);
    }
    return json(200, {});
  });
  renderArchive(settings({ open_issues: 3 }));
  fireEvent.click(screen.getByText("归档工作空间"));
  expect(screen.getByText("还有 3 张未结束的问题单")).toBeTruthy();
  const confirm = screen.getByText("确认归档") as HTMLButtonElement;
  expect(confirm.disabled).toBe(true);
  fireEvent.click(screen.getByRole("checkbox"));
  expect(confirm.disabled).toBe(false);
  fireEvent.click(confirm);
  await waitFor(() => expect(bodies).toEqual([{ confirm_open: 3 }]));
});

it("归档：没有未结束单子时只需一次普通确认，带 0", async () => {
  const bodies: unknown[] = [];
  vi.stubGlobal("fetch", async (_u: string, init: RequestInit) => {
    if (init.method === "POST") bodies.push(JSON.parse(String(init.body)));
    return json(204, null);
  });
  renderArchive(settings());
  fireEvent.click(screen.getByText("归档工作空间"));
  expect(screen.queryByRole("checkbox")).toBeNull();
  fireEvent.click(screen.getByText("确认归档"));
  await waitFor(() => expect(bodies).toEqual([{ confirm_open: 0 }]));
});

it("归档：后端回 open_issues_unconfirmed 时用返回的数字刷新并要求重新勾选", async () => {
  const bodies: unknown[] = [];
  vi.stubGlobal("fetch", async (_u: string, init: RequestInit) => {
    if (init.method !== "POST") return json(200, {});
    bodies.push(JSON.parse(String(init.body)));
    return bodies.length === 1
      ? json(409, { error: "数量变了", code: "open_issues_unconfirmed", open_issues: 5 })
      : json(204, null);
  });
  renderArchive(settings({ open_issues: 3 }));
  fireEvent.click(screen.getByText("归档工作空间"));
  fireEvent.click(screen.getByRole("checkbox"));
  fireEvent.click(screen.getByText("确认归档"));
  expect(await screen.findByText("还有 5 张未结束的问题单")).toBeTruthy();
  expect((screen.getByRole("checkbox") as HTMLInputElement).checked).toBe(false);
  expect((screen.getByText("确认归档") as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(screen.getByRole("checkbox"));
  fireEvent.click(screen.getByText("确认归档"));
  await waitFor(() => expect(bodies).toEqual([{ confirm_open: 3 }, { confirm_open: 5 }]));
});

it("已归档：显示取消归档按钮并调用接口", async () => {
  const urls: string[] = [];
  vi.stubGlobal("fetch", async (url: string, init: RequestInit) => {
    if (init.method === "POST") urls.push(url);
    return json(204, null);
  });
  renderArchive(settings({ archived: true, archived_at: "2026-10-01T00:00:00Z" }));
  fireEvent.click(screen.getByText("取消归档"));
  fireEvent.click(screen.getByText("确认取消归档"));
  await waitFor(() => expect(urls).toEqual(["/api/v1/w/pay/admin/unarchive"]));
});

const wsItem = (name: string, over: Partial<ServerWorkspace> = {}): ServerWorkspace => ({
  name,
  created_at: "2026-10-01T00:00:00Z",
  members: 1,
  admins: ["lee"],
  open_issues: 0,
  telegram_bound: false,
  i_am_member: false,
  archived: false,
  issues: 4,
  unfinished_issues: 0,
  ...over,
});

function renderServer(items: ServerWorkspace[], purges: unknown[] = []) {
  vi.stubGlobal("fetch", async (url: string, init: RequestInit) => {
    if (url === "/api/v1/server/workspaces" && (init.method ?? "GET") === "GET") return json(200, { items });
    if (url.endsWith("/purge")) {
      purges.push([url, JSON.parse(String(init.body))]);
      return json(200, { issues: 4, drafts: 0 });
    }
    return json(200, {});
  });
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const router = createRouter({
    routeTree: createRootRoute({ component: () => <WorkspacesTab self="lee" /> }),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
  render(
    <QueryClientProvider client={qc}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

it("服务器页：按使用中 / 已归档筛选，已归档带标记", async () => {
  renderServer([wsItem("live"), wsItem("gone", { archived: true })]);
  expect(await screen.findByText("live")).toBeTruthy();
  expect(screen.getByText("gone")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "已归档" }));
  expect(screen.queryByText("live")).toBeNull();
  expect(screen.getByText("gone")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "使用中" }));
  expect(screen.getByText("live")).toBeTruthy();
  expect(screen.queryByText("gone")).toBeNull();
});

it("服务器页：彻底删除须输入完全一致的路径", async () => {
  const purges: unknown[] = [];
  renderServer([wsItem("gone", { archived: true })], purges);
  fireEvent.click(await screen.findByRole("button", { name: "彻底删除" }));
  expect(screen.getByText(/将删除 4 张问题单/)).toBeTruthy();
  const confirm = screen.getAllByRole("button", { name: "彻底删除" }).at(-1) as HTMLButtonElement;
  expect(confirm.disabled).toBe(true);
  const input = screen.getByLabelText(/输入工作空间路径/);
  fireEvent.change(input, { target: { value: "gon" } });
  expect(confirm.disabled).toBe(true);
  fireEvent.change(input, { target: { value: "gone" } });
  expect(confirm.disabled).toBe(false);
  fireEvent.click(confirm);
  await waitFor(() => expect(purges).toEqual([["/api/v1/server/workspaces/gone/purge", { confirm: "gone" }]]));
  expect(await screen.findByText("已彻底删除 gone（4 张问题单）。")).toBeTruthy();
});

it("服务器页：有未结束单子时彻底删除按钮禁用并提示", async () => {
  renderServer([wsItem("busy", { unfinished_issues: 2, open_issues: 2 })]);
  const btn = (await screen.findByRole("button", { name: "彻底删除" })) as HTMLButtonElement;
  expect(btn.disabled).toBe(true);
  expect(screen.getByText("还有 2 张未结束，不能彻底删除")).toBeTruthy();
});
