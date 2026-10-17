// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { AdminEvent } from "../api";
import { EventLog } from "./EventLog";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const ev = (id: number, text: string, over: Partial<AdminEvent> = {}): AdminEvent => ({
  id,
  at: "2026-10-17T06:03:00Z",
  via: "telegram",
  via_label: "群按钮",
  actor: "lee",
  workspace: "pay",
  action: "signup.approve",
  action_label: "批准注册申请",
  category: "approval",
  target_kind: "signup",
  target: "tester-d",
  text,
  detail: [{ label: "申请人", value: "tester-d" }],
  ...over,
});

const meta = {
  types: [
    { id: "approval", label: "审批" },
    { id: "role", label: "角色" },
  ],
  vias: [
    { id: "web", label: "网页" },
    { id: "telegram", label: "群按钮" },
  ],
};

function stub(pages: Record<string, unknown>) {
  const urls: string[] = [];
  vi.stubGlobal("fetch", async (url: string) => {
    urls.push(url);
    const body = pages[url] ?? { items: [], next_before: null, ...meta };
    return new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
  });
  return urls;
}

function show(scope: { ws: string } | { server: true }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <EventLog scope={scope} />
    </QueryClientProvider>,
  );
}

it("显示人话、入口与动作；筛选重新请求并带参数；“加载更早的记录”带游标", async () => {
  const urls = stub({
    "/api/v1/w/pay/admin/events": { items: [ev(12, "lee 在群里批准了 tester-d 的注册申请")], next_before: 12, ...meta },
    "/api/v1/w/pay/admin/events?before=12": {
      items: [ev(11, "lee 在网页上把 ann 的角色从成员改为管理员", { via_label: "网页", action_label: "改角色" })],
      next_before: null,
      ...meta,
    },
  });
  show({ ws: "pay" });
  expect(await screen.findByText("lee 在群里批准了 tester-d 的注册申请")).toBeTruthy();
  expect(screen.getAllByText("群按钮").length).toBeGreaterThan(0);
  fireEvent.click(screen.getByText("加载更早的记录"));
  expect(await screen.findByText("lee 在网页上把 ann 的角色从成员改为管理员")).toBeTruthy();
  expect(screen.queryByText("加载更早的记录")).toBeNull();

  fireEvent.change(screen.getByLabelText("类型"), { target: { value: "role" } });
  await waitFor(() => expect(urls).toContain("/api/v1/w/pay/admin/events?type=role"));
  fireEvent.change(screen.getByLabelText("操作人"), { target: { value: " lee " } });
  fireEvent.click(screen.getByText("筛选"));
  await waitFor(() => expect(urls).toContain("/api/v1/w/pay/admin/events?type=role&actor=lee"));
  fireEvent.change(screen.getByLabelText("入口"), { target: { value: "telegram" } });
  await waitFor(() => expect(urls).toContain("/api/v1/w/pay/admin/events?type=role&actor=lee&via=telegram"));
});

it("人话按纯文本显示，不当作网页代码", async () => {
  stub({ "/api/v1/server/events": { items: [ev(1, "lee 吊销了 ann 的设备 <b>x</b>")], next_before: null, ...meta } });
  show({ server: true });
  expect(await screen.findByText("lee 吊销了 ann 的设备 <b>x</b>")).toBeTruthy();
  expect(document.querySelector("li b")).toBeNull();
  // 服务器管理页显示工作空间与工作空间筛选
  expect(screen.getByText("pay")).toBeTruthy();
  expect(screen.getByLabelText("工作空间")).toBeTruthy();
});

it("没有记录时提示", async () => {
  stub({});
  show({ ws: "pay" });
  expect(await screen.findByText("还没有记录。")).toBeTruthy();
});
