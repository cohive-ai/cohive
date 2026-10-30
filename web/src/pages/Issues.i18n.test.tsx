// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { act, cleanup, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, RouterProvider } from "@tanstack/react-router";
import type { Board, BoardIssue, Issue, IssueEvent, Me } from "../api";
import i18n from "../i18n";
import { createAppRouter } from "../router";

// 看板与问题单详情的语言切换：切到英文后，页面自己的文案、状态名、动作名都随之变化。

const me: Me = {
  account: { id: 1, name: "lee", server_admin: false, disabled: false, has_password: true },
  must_change_password: false,
  login_method: "password",
  tool: "web",
  workspaces: [{ name: "pay", role: "member" }],
};

const issue: Issue = {
  id: 7,
  version: 3,
  status: "READY_FOR_VERIFICATION",
  status_label: "待验证",
  category: "in_progress",
  title: "Login fails",
  repro: {},
  severity: "high",
  source: "ann",
  source_tool: "codex",
  owner: "lee",
  reopen_count: 1,
  escalated: false,
  your_actions: [],
  created_at: "2026-10-17T06:03:00Z",
  updated_at: "2026-10-17T06:03:00Z",
};

const board: Board = {
  me: "lee",
  members: [{ name: "lee" }, { name: "ann" }],
  issues: [
    { ...issue, holder: "lee", needs: "已修复，等发起人验证" } as BoardIssue,
    { ...issue, id: 8, holder: "ann", status: "OPEN", status_label: "待接单", needs: "等负责人接单" } as BoardIssue,
  ],
};

const events: IssueEvent[] = [
  { at: "2026-10-17T06:03:00Z", actor: "lee", tool: "claude-code", action: "transfer", payload: { assignee: "ann" } },
  { at: "2026-10-17T06:04:00Z", actor: "ann", tool: "codex", action: "request_verification", from: "FIXING", to: "READY_FOR_VERIFICATION" },
];

function stubApi() {
  vi.stubGlobal("fetch", async (url: string) => {
    const json = (body: unknown) => new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
    if (url === "/api/v1/me") return json(me);
    if (url === "/api/v1/w/pay/board") return json(board);
    if (url === "/api/v1/w/pay/issues/7") return json({ issue, events, can_cancel: false, can_delete: false, watchers: [], watching: false, can_watch: false, can_add_watchers: false, can_remove_watchers: false });
    return new Response(JSON.stringify({ error: "not found" }), { status: 404, headers: { "Content-Type": "application/json" } });
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
}

beforeEach(() => {
  window.scrollTo = () => {};
  stubApi();
});
afterEach(async () => {
  cleanup();
  vi.unstubAllGlobals();
  await i18n.changeLanguage("zh-CN");
});

it("看板：中文显示服务端无关的本地文案", async () => {
  open("/w/pay/");
  expect(await screen.findByText("2 张待处理，", { exact: false })).toBeTruthy();
  expect(screen.getByText("其中 1 张等你处理")).toBeTruthy();
  expect(screen.getByText("已修复，等你验证")).toBeTruthy();
  expect(screen.getByText("等负责人接单")).toBeTruthy();
  expect(screen.getByText("已结束")).toBeTruthy();
});

it("看板：切到英文后文案、状态名、导航随之变化", async () => {
  await i18n.changeLanguage("en");
  open("/w/pay/");
  expect(await screen.findByText("2 issues pending,", { exact: false })).toBeTruthy();
  expect(screen.getByText("1 waiting for you")).toBeTruthy();
  expect(screen.getByText("Fixed, waiting for you to verify")).toBeTruthy();
  expect(screen.getByText("Waiting for the owner to accept")).toBeTruthy();
  expect(screen.getByText("Finished")).toBeTruthy();
  expect(screen.getByText("Ready for verification")).toBeTruthy();
  expect(screen.getByRole("link", { name: "Issues" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "Sign out" })).toBeTruthy();
});

it("问题单详情：英文下的字段名与动态句子", async () => {
  await i18n.changeLanguage("en");
  open("/w/pay/issues/7");
  expect(await screen.findByText("Reporter")).toBeTruthy();
  expect(screen.getByText("High")).toBeTruthy();
  expect(screen.getByText("v3")).toBeTruthy();
  expect(screen.getByText("The reporter did not provide a repro script, so verification can only be done by hand.")).toBeTruthy();
  const items = screen.getAllByRole("listitem").map((li) => li.textContent);
  expect(items[0]).toContain("lee (Claude Code) transferred to ann");
  expect(items[1]).toContain("ann (Codex) requested verification, status changed to Ready for verification");
});

it("问题单详情：中文下的动态句子", async () => {
  open("/w/pay/issues/7");
  expect(await screen.findByText("发起人")).toBeTruthy();
  const items = screen.getAllByRole("listitem").map((li) => li.textContent);
  expect(items[0]).toContain("lee（Claude Code）转交给 ann");
  expect(items[1]).toContain("ann（Codex）请求验证，状态变为待验证");
});

it("页面打开后切换语言，已显示的内容立即换成新语言", async () => {
  open("/w/pay/");
  expect(await screen.findByText("已结束")).toBeTruthy();
  await act(async () => {
    await i18n.changeLanguage("en");
  });
  expect(screen.getByText("Finished")).toBeTruthy();
  expect(screen.getByRole("link", { name: "Board" })).toBeTruthy();
  expect(screen.getByText("Ready for verification")).toBeTruthy();
});
