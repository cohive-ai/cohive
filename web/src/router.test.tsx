// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, RouterProvider } from "@tanstack/react-router";
import type { Me } from "./api";
import { createAppRouter } from "./router";
import { LAST_WORKSPACE_KEY } from "./workspace";

const baseMe: Me = {
  account: { id: 1, name: "lee", server_admin: false, disabled: false, has_password: true },
  must_change_password: false,
  login_method: "password",
  tool: "web",
  workspaces: [
    { name: "pay", role: "member" },
    { name: "ops", role: "admin" },
  ],
};

function stubApi(me: Me) {
  vi.stubGlobal("fetch", async (url: string) => {
    const json = (status: number, body: unknown) =>
      new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
    if (url === "/api/v1/me") return json(200, me);
    return json(404, { error: "没有这个接口" });
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
  localStorage.clear();
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const path = (r: ReturnType<typeof createAppRouter>) => r.state.location.pathname.replace(/\/$/, "");

it("首页跳到上次用的工作空间", async () => {
  stubApi(baseMe);
  localStorage.setItem(LAST_WORKSPACE_KEY, "ops");
  const r = open("/");
  await waitFor(() => expect(path(r)).toBe("/w/ops"));
});

it("首页没有记录（或记录的工作空间已不在）时跳到第一个", async () => {
  stubApi(baseMe);
  localStorage.setItem(LAST_WORKSPACE_KEY, "gone");
  const r = open("/");
  await waitFor(() => expect(path(r)).toBe("/w/pay"));
});

it("一个工作空间都没有时显示提示，服务器管理员额外看到新建入口", async () => {
  stubApi({ ...baseMe, account: { ...baseMe.account, server_admin: true }, workspaces: [] });
  const r = open("/");
  expect(await screen.findByText("你还没有加入任何工作空间，请联系管理员。")).toBeTruthy();
  expect(await screen.findByText("新建工作空间")).toBeTruthy();
  expect(path(r)).toBe("");
});

it("群消息里的旧链接跳到上次用的工作空间对应的新地址", async () => {
  stubApi(baseMe);
  localStorage.setItem(LAST_WORKSPACE_KEY, "ops");
  const r = open("/issues/5");
  await waitFor(() => expect(path(r)).toBe("/w/ops/issues/5"));
});

it("旧的问题单列表与管理页地址也会跳转，管理页保留分类", async () => {
  stubApi(baseMe);
  const r1 = open("/issues");
  await waitFor(() => expect(path(r1)).toBe("/w/pay/issues"));
  cleanup();
  const r2 = open("/admin?tab=members");
  await waitFor(() => expect(path(r2)).toBe("/w/pay/admin"));
  expect(r2.state.location.search).toEqual({ tab: "members" });
});

it("访问工作空间页面会记下它，供下次进入首页使用", async () => {
  stubApi(baseMe);
  open("/w/ops/issues");
  await waitFor(() => expect(localStorage.getItem(LAST_WORKSPACE_KEY)).toBe("ops"));
});

it("不是成员的工作空间不记为上次用的", async () => {
  stubApi(baseMe);
  open("/w/other/");
  expect(await screen.findByText(/不存在，或者你不是它的成员/)).toBeTruthy();
  expect(localStorage.getItem(LAST_WORKSPACE_KEY)).toBeNull();
});

it("被要求改密码时先去改密码", async () => {
  stubApi({ ...baseMe, must_change_password: true });
  const r = open("/w/pay/issues");
  await waitFor(() => expect(path(r)).toBe("/password"));
});

it("管理页与服务器管理页都有“记录”一栏", async () => {
  stubApi({ ...baseMe, account: { ...baseMe.account, server_admin: true } });
  const r1 = open("/w/ops/admin?tab=events");
  await waitFor(() => expect(path(r1)).toBe("/w/ops/admin"));
  expect(r1.state.location.search).toEqual({ tab: "events" });
  expect(await screen.findByRole("link", { name: "记录" })).toBeTruthy();
  cleanup();
  const r2 = open("/server?tab=events");
  await waitFor(() => expect(r2.state.location.search).toEqual({ tab: "events" }));
  expect(await screen.findByRole("link", { name: "记录" })).toBeTruthy();
});

it("设备授权页的“可用于”只出现一次", async () => {
  vi.stubGlobal("fetch", async (url: string) => {
    const json = (status: number, body: unknown) =>
      new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
    if (url === "/api/v1/me") return json(200, baseMe);
    if (url === "/api/v1/device/auth/da_x")
      return json(200, {
        device_name: "mac",
        tools: ["codex"],
        created_at: new Date().toISOString(),
        status: "pending",
        first_device: true,
        workspaces: ["pay", "ops"],
      });
    return json(404, { error: "没有这个接口" });
  });
  open("/device/da_x");
  expect(await screen.findByText("pay、ops")).toBeTruthy();
  expect(document.body.textContent!.match(/可用于/g)?.length).toBe(1);
});
