// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, RouterProvider } from "@tanstack/react-router";
import type { Issue, IssueDetail, Me } from "../api";
import { createAppRouter } from "../router";

// 关注者（CC-1）：详情页列出关注者；按权限显示关注、抄送、移除；操作后刷新详情；归档时不给操作。

const me: Me = {
  account: { id: 1, name: "lee", server_admin: false, disabled: false, has_password: true },
  must_change_password: false,
  login_method: "password",
  tool: "web",
  workspaces: [{ name: "pay", role: "member" }],
};

const issue: Issue = {
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
  your_actions: ["comment"],
  created_at: "2026-10-18T06:03:00Z",
  updated_at: "2026-10-18T06:03:00Z",
};

let calls: { method: string; url: string; body?: string }[] = [];
let detail: IssueDetail;
let failWith: string | null = null;

function stub(over: Partial<IssueDetail>) {
  calls = [];
  failWith = null;
  detail = {
    issue,
    events: [],
    can_cancel: false,
    can_delete: false,
    watchers: [{ name: "bob", display_name: "鲍勃" }],
    watching: false,
    can_watch: true,
    can_add_watchers: true,
    can_remove_watchers: true,
    ...over,
  };
  vi.stubGlobal("fetch", async (url: string, init?: RequestInit) => {
    calls.push({ method: init?.method ?? "GET", url, body: init?.body as string | undefined });
    const json = (body: unknown, status = 200) =>
      new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
    if (url === "/api/v1/me") return json(me);
    if (url === "/api/v1/w/pay/issues/7/watchers") {
      if (failWith) return json({ error: failWith, code: "member_nickname" }, 400);
      return json({ watchers: detail.watchers });
    }
    if (url === "/api/v1/w/pay/issues/7") return json(detail);
    return json({ error: "not found" }, 404);
  });
}

function open() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const router = createAppRouter(qc, createMemoryHistory({ initialEntries: ["/w/pay/issues/7"] }));
  render(
    <QueryClientProvider client={qc}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

const posts = () => calls.filter((c) => c.method === "POST");

beforeEach(() => {
  window.scrollTo = () => {};
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

it("列出关注者；关注自己只提交 watch", async () => {
  stub({});
  open();
  expect(await screen.findByText("鲍勃")).toBeTruthy();
  expect(screen.getByText("bob")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "关注" }));
  await waitFor(() => expect(posts().length).toBe(1));
  expect(posts()[0].url).toBe("/api/v1/w/pay/issues/7/watchers");
  expect(JSON.parse(posts()[0].body!)).toEqual({ watch: true });
});

it("已关注时显示取消关注", async () => {
  stub({ watching: true });
  open();
  fireEvent.click(await screen.findByRole("button", { name: "取消关注" }));
  await waitFor(() => expect(posts().length).toBe(1));
  expect(JSON.parse(posts()[0].body!)).toEqual({ watch: false });
});

it("抄送：输入登录名后提交，空输入提示；错误原样显示", async () => {
  stub({});
  open();
  const input = await screen.findByLabelText("抄送：输入登录名");
  fireEvent.click(screen.getByRole("button", { name: "抄送" }));
  expect(await screen.findByText("请输入要抄送的登录名")).toBeTruthy();
  expect(posts().length).toBe(0);
  failWith = "“小明”是昵称，请用登录名：xiaoming";
  fireEvent.change(input, { target: { value: "小明, carol" } });
  fireEvent.click(screen.getByRole("button", { name: "抄送" }));
  expect(await screen.findByText("“小明”是昵称，请用登录名：xiaoming")).toBeTruthy();
  expect(JSON.parse(posts()[0].body!)).toEqual({ watch: true, members: ["小明", "carol"] });
});

it("有权限的人可以移除别人", async () => {
  stub({});
  open();
  fireEvent.click(await screen.findByRole("button", { name: "移除关注者 bob" }));
  await waitFor(() => expect(posts().length).toBe(1));
  expect(JSON.parse(posts()[0].body!)).toEqual({ watch: false, members: ["bob"] });
});

it("没有权限时只列出关注者，不显示任何操作", async () => {
  stub({ can_watch: false, can_add_watchers: false, can_remove_watchers: false });
  open();
  expect(await screen.findByText("鲍勃")).toBeTruthy();
  expect(screen.queryByRole("button", { name: "关注" })).toBeNull();
  expect(screen.queryByRole("button", { name: "取消关注" })).toBeNull();
  expect(screen.queryByLabelText("抄送：输入登录名")).toBeNull();
  expect(screen.queryByRole("button", { name: /移除关注者/ })).toBeNull();
});
