// @vitest-environment jsdom
import { afterEach, beforeAll, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { AdminMember } from "../api";
import { HandoverDialog, InviteLinkDialog } from "./Admin";

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

it("一次性邀请链接对话框不能按 Esc 关闭，只能点“我已复制”", () => {
  const done = vi.fn();
  render(<InviteLinkDialog url="https://cohive.example.com/i/abc" onDone={done} />);
  const dialog = document.querySelector("dialog")!;
  expect(dialog.hasAttribute("open")).toBe(true);
  const ev = new Event("cancel", { cancelable: true });
  dialog.dispatchEvent(ev);
  if (!ev.defaultPrevented) dialog.close();
  expect(dialog.hasAttribute("open")).toBe(true);
  expect(done).not.toHaveBeenCalled();
  fireEvent.click(screen.getByText("我已复制"));
  expect(done).toHaveBeenCalledTimes(1);
});

const memberOf = (member_id: number, account: string, over: Partial<AdminMember> = {}): AdminMember => ({
  member_id,
  account,
  role: "member",
  status: "active",
  disabled: false,
  telegram_linked: false,
  devices: 1,
  has_password: true,
  pending_issues: 0,
  owned_services: 0,
  ...over,
});

it("转交对话框：全部转给同一人后提交；409 时用最新清单刷新、保留已选并提示，补选后再提交成功", async () => {
  const issue = (id: number, version: number) => ({
    id,
    title: `单子 ${id}`,
    status: "OPEN",
    status_label: "待接单",
    as: "owner",
    version,
  });
  const first = { member_id: 7, account: "bob", issues: [issue(1, 3)], services: [{ name: "pay-api" }] };
  const latest = { ...first, issues: [issue(1, 4), issue(2, 1)] };
  const posts: unknown[] = [];
  vi.stubGlobal("fetch", async (url: string, init: RequestInit) => {
    const json = (status: number, body: unknown) =>
      new Response(status === 204 ? null : JSON.stringify(body), { status });
    if (url === "/api/v1/w/pay/admin/members/7/handover" && init.method === "GET") return json(200, first);
    if (url === "/api/v1/w/pay/admin/members/7/remove") {
      posts.push(JSON.parse(String(init.body)));
      return posts.length === 1
        ? json(409, { error: "问题单 #1 已有新进展，请刷新转交清单后重试", handover: latest })
        : json(204, null);
    }
    return json(404, { error: "没有这个接口" });
  });
  const done = vi.fn(async () => {});
  render(
    <HandoverDialog
      ws="pay"
      member={memberOf(7, "bob")}
      remove
      candidates={[memberOf(3, "ann"), memberOf(4, "cat")]}
      onClose={() => {}}
      onDone={done}
    />,
  );
  expect(await screen.findByText(/单子 1/)).toBeTruthy();
  fireEvent.change(screen.getByDisplayValue("逐项选择"), { target: { value: "3" } });
  fireEvent.click(screen.getByText("转交并移出"));

  expect(await screen.findByText(/清单已按最新情况刷新/)).toBeTruthy();
  expect(posts[0]).toEqual({
    issues: [{ id: 1, as: "owner", to_member_id: 3, expected_version: 3 }],
    services: [{ name: "pay-api", to_member_id: 3 }],
  });
  expect(screen.getByText(/单子 2/)).toBeTruthy();
  // 新出现的单子还没选人：提交前拦下
  fireEvent.click(screen.getByText("转交并移出"));
  expect(await screen.findByText("还有 1 项没有选接手人。")).toBeTruthy();
  expect(posts).toHaveLength(1);

  fireEvent.change(screen.getByLabelText("#2 的接手人"), { target: { value: "4" } });
  fireEvent.click(screen.getByText("转交并移出"));
  await waitFor(() => expect(done).toHaveBeenCalledTimes(1));
  expect(posts[1]).toEqual({
    issues: [
      { id: 1, as: "owner", to_member_id: 3, expected_version: 4 },
      { id: 2, as: "owner", to_member_id: 4, expected_version: 1 },
    ],
    services: [{ name: "pay-api", to_member_id: 3 }],
  });
});

it("工作空间设置：超时催办时长默认 4、24 小时，保存时一并提交；不是 0 到 720 的整数时不让保存", async () => {
  const { QueryClient, QueryClientProvider } = await import("@tanstack/react-query");
  const { WorkspaceForm } = await import("./Admin");
  const bodies: unknown[] = [];
  vi.stubGlobal("fetch", async (url: string, init: RequestInit) => {
    if (url === "/api/v1/w/pay/admin/workspace" && init.method === "POST") {
      bodies.push(JSON.parse(String(init.body)));
      return new Response(null, { status: 204 });
    }
    return new Response(JSON.stringify({ error: "没有这个接口" }), { status: 404 });
  });
  render(
    <QueryClientProvider client={new QueryClient()}>
      <WorkspaceForm
        ws="pay"
        serverAdmin={false}
        initial={{
          name: "pay",
          path: "pay",
          display_name: "pay",
          telegram_chat: "",
          show_title: true,
          show_brief: true,
          bot_enabled: true,
          external_url_set: false,
          issue_link_example: "",
        }}
      />
    </QueryClientProvider>,
  );
  const open = screen.getByLabelText("待接单催办时长（小时）") as HTMLInputElement;
  const verify = screen.getByLabelText("待验证催办时长（小时）") as HTMLInputElement;
  expect(open.value).toBe("4");
  expect(verify.value).toBe("24");
  fireEvent.change(open, { target: { value: "1000" } });
  expect(screen.getByText("请填 0 到 720 的整数小时。")).toBeTruthy();
  fireEvent.click(screen.getByText("保存"));
  await new Promise((r) => setTimeout(r, 50));
  expect(bodies).toHaveLength(0);
  fireEvent.change(open, { target: { value: "0" } });
  fireEvent.change(verify, { target: { value: "48" } });
  fireEvent.click(screen.getByText("保存"));
  await waitFor(() => expect(bodies).toHaveLength(1));
  expect(bodies[0]).toMatchObject({ remind_open_hours: 0, remind_verify_hours: 48 });
});
