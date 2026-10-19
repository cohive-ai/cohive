// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import type { Me, WorkspaceSettings } from "../api";
import i18n from "../i18n";
import { CodeBind, CodeLink } from "./ChatCode";
import { WorkspaceForm } from "./Admin";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const wrap = (ui: ReactNode) =>
  render(<QueryClientProvider client={new QueryClient()}>{ui}</QueryClientProvider>);

const json = (status: number, body: unknown) => new Response(status === 204 ? null : JSON.stringify(body), { status });

const me: Me = {
  account: { id: 1, name: "ann", server_admin: false, disabled: false, has_password: true },
  must_change_password: false,
  tool: "web",
  workspaces: [],
  chat: [],
  channels: [{ provider: "dingtalk" }],
};

it("连接钉钉：显示绑定码与私聊说明；机器人收到码后显示钉钉昵称，本人确认后才连接", async () => {
  let polls = 0;
  const posts: string[] = [];
  vi.stubGlobal("fetch", async (url: string, init: RequestInit) => {
    if (url === "/api/v1/me/links" && init.method === "POST") {
      posts.push(String(init.body));
      return json(200, { id: 7, code: "ABCD2345", expires_in: 600 });
    }
    if (url === "/api/v1/me/links/7") {
      polls++;
      return json(200, polls < 2 ? { status: "pending" } : { status: "awaiting_confirm", candidate_name: "安<b>" });
    }
    if (url === "/api/v1/me/links/7/confirm") {
      posts.push(String(init.body));
      return json(204, null);
    }
    return json(404, { error: "没有这个接口" });
  });
  wrap(<CodeLink provider="dingtalk" me={me} />);
  fireEvent.click(screen.getByText("连接钉钉"));
  await screen.findByDisplayValue("ABCD2345");
  expect(screen.getByText(/私聊发给机器人/)).toBeTruthy();
  expect(posts[0]).toContain('"provider":"dingtalk"');
  await screen.findByText("安<b>", {}, { timeout: 6000 }); // 按纯文本显示
  fireEvent.click(screen.getByText("是我，确认连接"));
  await screen.findByText(/已连接钉钉/);
  expect(posts[1]).toBe('{"accept":true}');
}, 10000);

it("绑定钉钉群：生成绑定码后给出要在群里发送的整句；已绑别的通讯工具的群时不能生成", async () => {
  vi.stubGlobal("fetch", async (url: string, init: RequestInit) => {
    if (url === "/api/v1/w/pay/admin/chat-binds" && init.method === "POST") return json(200, { id: 3, code: "WXYZ6789", expires_in: 600 });
    if (url === "/api/v1/w/pay/admin/chat-binds/3") return json(200, { status: "pending" });
    return json(404, { error: "没有这个接口" });
  });
  wrap(<CodeBind provider="dingtalk" ws="pay" chat={null} onChange={() => {}} />);
  fireEvent.click(screen.getByText("生成群绑定码"));
  await screen.findByDisplayValue("绑定群 WXYZ6789");
  cleanup();
  wrap(<CodeBind provider="dingtalk" ws="pay" chat={{ provider: "telegram", chat_id: "-100" }} onChange={() => {}} />);
  expect((screen.getByText("生成群绑定码") as HTMLButtonElement).disabled).toBe(true);
  expect(screen.getByText(/要先解绑/)).toBeTruthy();
});

const settings = (over: Partial<WorkspaceSettings>): WorkspaceSettings => ({
  name: "pay",
  path: "pay",
  display_name: "pay",
  telegram_chat: "",
  show_title: true,
  show_brief: true,
  bot_enabled: false,
  external_url_set: false,
  issue_link_example: "",
  ...over,
});

it("工作空间设置：只启用钉钉时不显示 Telegram 群编号；两种都启用、已绑钉钉群时 Telegram 群编号不能填", () => {
  wrap(<WorkspaceForm ws="pay" serverAdmin={false} initial={settings({ channels: [{ provider: "dingtalk" }], chat: null })} />);
  expect(screen.queryByLabelText("Telegram 群编号")).toBeNull();
  expect(screen.getByText("生成群绑定码")).toBeTruthy();
  expect(screen.queryByText(/没有启用 Telegram 机器人/)).toBeNull();
  cleanup();
  wrap(
    <WorkspaceForm
      ws="pay"
      serverAdmin={false}
      initial={settings({
        bot_enabled: true,
        channels: [{ provider: "telegram" }, { provider: "dingtalk" }],
        chat: { provider: "dingtalk", chat_id: "cid", title: "支付群" },
      })}
    />,
  );
  expect((screen.getByLabelText("Telegram 群编号") as HTMLInputElement).disabled).toBe(true);
  expect(screen.getByText("已绑定：支付群")).toBeTruthy();
});

it("英文界面：群绑定指令用机器人也认的 bindgroup，说明与按钮是英文", async () => {
  await i18n.changeLanguage("en");
  try {
    vi.stubGlobal("fetch", async (url: string, init: RequestInit) => {
      if (url === "/api/v1/w/pay/admin/chat-binds" && init.method === "POST") return json(200, { id: 3, code: "WXYZ6789", expires_in: 600 });
      if (url === "/api/v1/w/pay/admin/chat-binds/3") return json(200, { status: "pending" });
      return json(404, { error: "not found" });
    });
    wrap(<CodeBind provider="dingtalk" ws="pay" chat={null} onChange={() => {}} />);
    expect(screen.getByText("Not bound")).toBeTruthy();
    fireEvent.click(screen.getByText("Generate group code"));
    await screen.findByDisplayValue("bindgroup WXYZ6789");
    expect(screen.getByText(/exactly as shown/)).toBeTruthy();
    expect(screen.getByLabelText("Send in the group")).toBeTruthy();
  } finally {
    await i18n.changeLanguage("zh-CN");
  }
});

it("连接 Slack：同一套流程，按 Slack 的文案与通讯工具名发起", async () => {
  let polls = 0;
  const posts: string[] = [];
  vi.stubGlobal("fetch", async (url: string, init: RequestInit) => {
    if (url === "/api/v1/me/links" && init.method === "POST") {
      posts.push(String(init.body));
      return json(200, { id: 8, code: "SLAK2345", expires_in: 600 });
    }
    if (url === "/api/v1/me/links/8") {
      polls++;
      return json(200, polls < 2 ? { status: "pending" } : { status: "awaiting_confirm", candidate_name: "lee.s" });
    }
    if (url === "/api/v1/me/links/8/confirm") return json(204, null);
    return json(404, { error: "没有这个接口" });
  });
  wrap(<CodeLink provider="slack" me={{ ...me, channels: [{ provider: "slack" }] }} />);
  fireEvent.click(screen.getByText("连接 Slack"));
  await screen.findByDisplayValue("SLAK2345");
  expect(posts[0]).toBe('{"provider":"slack"}');
  expect(screen.getByText(/“消息”/)).toBeTruthy();
  await screen.findByText("lee.s", {}, { timeout: 6000 });
  expect(screen.getByText(/Slack 用户/)).toBeTruthy();
  fireEvent.click(screen.getByText("是我，确认连接"));
  await screen.findByText(/已连接 Slack/);
}, 10000);

it("工作空间设置：只启用 Slack 时显示频道绑定、不显示 Telegram 群编号；绑定码发到 slack", async () => {
  const posts: string[] = [];
  vi.stubGlobal("fetch", async (url: string, init: RequestInit) => {
    if (url === "/api/v1/w/pay/admin/chat-binds" && init.method === "POST") {
      posts.push(String(init.body));
      return json(200, { id: 4, code: "CHAN6789", expires_in: 600 });
    }
    if (url === "/api/v1/w/pay/admin/chat-binds/4") return json(200, { status: "pending" });
    return json(404, { error: "没有这个接口" });
  });
  wrap(<WorkspaceForm ws="pay" serverAdmin={false} initial={settings({ channels: [{ provider: "slack" }], chat: null })} />);
  expect(screen.queryByLabelText("Telegram 群编号")).toBeNull();
  expect(screen.queryByText("生成群绑定码")).toBeNull(); // 没启用钉钉
  fireEvent.click(screen.getByText("生成频道绑定码"));
  await screen.findByDisplayValue("绑定群 CHAN6789");
  expect(posts[0]).toBe('{"provider":"slack"}');
  expect(screen.getByText(/invite @Cohive/)).toBeTruthy();
  cleanup();
  wrap(
    <WorkspaceForm
      ws="pay"
      serverAdmin={false}
      initial={settings({
        bot_enabled: true,
        channels: [{ provider: "telegram" }, { provider: "slack" }],
        chat: { provider: "slack", chat_id: "C1", title: "#pay" },
      })}
    />,
  );
  expect((screen.getByLabelText("Telegram 群编号") as HTMLInputElement).disabled).toBe(true);
  expect(screen.getByText("已绑定：#pay")).toBeTruthy();
  expect(screen.getByText(/已绑定 Slack 频道/)).toBeTruthy();
});
