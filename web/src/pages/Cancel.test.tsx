// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, RouterProvider } from "@tanstack/react-router";
import type { Board, BoardIssue, Issue, Me } from "../api";
import { createAppRouter } from "../router";

// 作废（#12）：看板默认隐藏已作废的单，打开开关才显示；详情页作废要写理由，删除草稿后回到看板。

const me: Me = {
  account: { id: 1, name: "lee", server_admin: false, disabled: false, has_password: true },
  must_change_password: false,
  login_method: "password",
  tool: "web",
  workspaces: [{ name: "pay", role: "member" }],
};

const base: Issue = {
  id: 7,
  version: 2,
  status: "OPEN",
  status_label: "待接单",
  category: "in_progress",
  title: "下单接口 500",
  repro: {},
  severity: "normal",
  source: "lee",
  source_tool: "codex",
  owner: "ann",
  reopen_count: 0,
  escalated: false,
  your_actions: ["cancel", "comment"],
  created_at: "2026-10-18T06:03:00Z",
  updated_at: "2026-10-18T06:03:00Z",
};

const board: Board = {
  me: "lee",
  members: [{ name: "lee" }, { name: "ann" }],
  issues: [
    { ...base, holder: "ann", needs: "" } as BoardIssue,
    { ...base, id: 9, title: "作废掉的单", status: "CANCELLED", status_label: "已作废", category: "done", your_actions: ["comment"] } as BoardIssue,
  ],
};

let calls: { method: string; url: string; body?: string }[] = [];

function stubApi(detail: { issue: Issue; can_cancel: boolean; can_delete: boolean }) {
  calls = [];
  vi.stubGlobal("fetch", async (url: string, init?: RequestInit) => {
    calls.push({ method: init?.method ?? "GET", url, body: init?.body as string | undefined });
    const json = (body: unknown, status = 200) =>
      new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
    if (url === "/api/v1/me") return json(me);
    if (url === "/api/v1/w/pay/board") return json(board);
    if (url === `/api/v1/w/pay/issues/${detail.issue.id}/cancel`) return json({ ...detail.issue, status: "CANCELLED" });
    if (url === `/api/v1/w/pay/issues/${detail.issue.id}` && init?.method === "DELETE") return new Response(null, { status: 204 });
    if (url === `/api/v1/w/pay/issues/${detail.issue.id}`) return json({ ...detail, events: [] });
    return json({ error: "not found" }, 404);
  });
}

function open(path: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const router = createAppRouter(qc, createMemoryHistory({ initialEntries: [path] }));
  render(
    <QueryClientProvider client={qc}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return router;
}

beforeEach(() => {
  window.scrollTo = () => {};
  HTMLDialogElement.prototype.showModal = function () {
    this.open = true;
  };
  HTMLDialogElement.prototype.close = function () {
    this.open = false;
  };
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

it("看板默认隐藏已作废的单，打开开关后显示", async () => {
  stubApi({ issue: base, can_cancel: true, can_delete: false });
  open("/w/pay");
  await screen.findByText("下单接口 500");
  expect(screen.queryByText("作废掉的单")).toBeNull();
  fireEvent.click(screen.getByLabelText("显示已作废（1）"));
  expect(await screen.findByText("作废掉的单")).toBeTruthy();
});

it("作废必须写理由，写了才提交", async () => {
  stubApi({ issue: base, can_cancel: true, can_delete: false });
  open("/w/pay/issues/7");
  fireEvent.click(await screen.findByRole("button", { name: "作废" }));
  fireEvent.click(screen.getByRole("button", { name: "确认作废" }));
  expect(await screen.findByText("请写明作废理由")).toBeTruthy();
  expect(calls.some((c) => c.method === "POST")).toBe(false);
  fireEvent.change(screen.getByLabelText("作废理由"), { target: { value: "重复提交" } });
  fireEvent.click(screen.getByRole("button", { name: "确认作废" }));
  await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
  const post = calls.find((c) => c.method === "POST")!;
  expect(post.url).toBe("/api/v1/w/pay/issues/7/cancel");
  expect(JSON.parse(post.body!)).toEqual({ expected_version: 2, reason: "重复提交" });
});

it("没有权限时不显示作废按钮；草稿显示删除，删除后回到看板", async () => {
  stubApi({ issue: { ...base, status: "DRAFT", your_actions: ["submit", "comment"] }, can_cancel: false, can_delete: true });
  const router = open("/w/pay/issues/7");
  await screen.findByRole("button", { name: "删除草稿" });
  expect(screen.queryByRole("button", { name: "作废" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "删除草稿" }));
  fireEvent.click(screen.getByRole("button", { name: "确认删除" }));
  await waitFor(() => expect(calls.some((c) => c.method === "DELETE" && c.url === "/api/v1/w/pay/issues/7")).toBe(true));
  await waitFor(() => expect(router.state.location.pathname).toBe("/w/pay"));
});
