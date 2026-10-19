// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import i18n from "../i18n";
import { MePage } from "./Me";

beforeEach(async () => {
  localStorage.clear();
  await i18n.changeLanguage("zh-CN");
});

afterEach(async () => {
  cleanup();
  vi.unstubAllGlobals();
  localStorage.clear();
  await i18n.changeLanguage("zh-CN");
});

const me = (display_name: string) => ({
  account: { id: 1, name: "ann", display_name, server_admin: false, disabled: false, has_password: true },
  must_change_password: false,
  tool: "web",
  workspaces: [],
});

// 假服务器：改成 taken 时按撞名拒绝，其余照存
function serve(initial: string) {
  const posts: unknown[] = [];
  let current = initial;
  vi.stubGlobal("fetch", async (url: string, init: RequestInit) => {
    const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });
    if (url === "/api/v1/me/display-name") {
      const body = JSON.parse(String(init.body)) as { display_name: string };
      posts.push(body);
      if (body.display_name === "taken") {
        return json({ code: "display_name_taken", clash: "name", error: "这个昵称和另一位成员的登录名相同" }, 409);
      }
      current = body.display_name;
      return json({ display_name: current });
    }
    if (url === "/api/v1/me") return json(me(current));
    if (url === "/api/v1/version") return json({ cli: null });
    if (url === "/api/v1/me/devices") return json({ items: [] });
    return json({});
  });
  return posts;
}

function show() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const invalidate = vi.spyOn(qc, "invalidateQueries");
  render(
    <QueryClientProvider client={qc}>
      <MePage />
    </QueryClientProvider>,
  );
  return invalidate;
}

it("个人中心：改显示名，保存后显示新名字并刷新看板、成员列表等缓存；撞名时显示服务器的报错", async () => {
  const posts = serve("");
  const invalidate = show();
  expect(await screen.findByText("未设置")).toBeTruthy();

  fireEvent.click(screen.getByRole("button", { name: "修改" }));
  const input = screen.getByLabelText("显示名") as HTMLInputElement;
  expect(input.placeholder).toBe("留空则显示登录名 ann");
  fireEvent.change(input, { target: { value: "taken" } });
  fireEvent.click(screen.getByRole("button", { name: "保存" }));
  expect(await screen.findByRole("alert")).toHaveProperty("textContent", "这个昵称和另一位成员的登录名相同");
  expect(screen.getByLabelText("显示名")).toBeTruthy(); // 报错时留在编辑状态

  fireEvent.change(screen.getByLabelText("显示名"), { target: { value: "  安妮  " } });
  fireEvent.click(screen.getByRole("button", { name: "保存" }));
  expect(await screen.findByText("显示名已保存。")).toBeTruthy();
  expect(await screen.findByText("安妮")).toBeTruthy();
  expect(posts).toEqual([{ display_name: "taken" }, { display_name: "安妮" }]);
  const keys = invalidate.mock.calls.map((c) => JSON.stringify(c[0]?.queryKey));
  for (const k of [["me"], ["board"], ["issues"], ["issue"], ["admin"]]) expect(keys).toContain(JSON.stringify(k));

  // 清空：回退显示登录名
  fireEvent.click(screen.getByRole("button", { name: "修改" }));
  fireEvent.change(screen.getByLabelText("显示名"), { target: { value: "" } });
  fireEvent.click(screen.getByRole("button", { name: "保存" }));
  expect(await screen.findByText("显示名已清空，各处将显示登录名 ann。")).toBeTruthy();
  await waitFor(() => expect(screen.getByText("未设置")).toBeTruthy());
  expect(posts[2]).toEqual({ display_name: "" });
});

it("英文界面：显示名编辑文案；取消不提交", async () => {
  await i18n.changeLanguage("en");
  const posts = serve("Ann");
  show();
  expect(await screen.findByText("Ann")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Edit" }));
  expect((screen.getByLabelText("Display name") as HTMLInputElement).value).toBe("Ann");
  expect(screen.getByText(/It can't match another member's username or display name/)).toBeTruthy();
  fireEvent.change(screen.getByLabelText("Display name"), { target: { value: "Annie" } });
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(screen.getByText("Ann")).toBeTruthy();
  expect(posts).toEqual([]);
});
