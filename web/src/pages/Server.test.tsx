// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ServerSettings } from "../api";
import { ExternalURLForm } from "./Server";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function show(initial: ServerSettings) {
  const bodies: unknown[] = [];
  vi.stubGlobal("fetch", async (_url: string, init: RequestInit) => {
    const body = JSON.parse(String(init.body ?? "{}"));
    bodies.push(body);
    const out = { external_url: String(body.external_url).toLowerCase(), locked: false, suggested: initial.suggested };
    return new Response(JSON.stringify(out), { status: 200, headers: { "Content-Type": "application/json" } });
  });
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <ExternalURLForm initial={initial} />
    </QueryClientProvider>,
  );
  return bodies;
}

it("还没设置时预填当前地址，并明确提示尚未保存；保存时去掉末尾斜杠", async () => {
  const bodies = show({ external_url: "", locked: false, suggested: "http://127.0.0.1:8787" });
  const input = screen.getByLabelText("对外地址") as HTMLInputElement;
  expect(input.value).toBe("http://127.0.0.1:8787");
  expect(screen.getByText(/尚未设置/)).toBeTruthy();
  fireEvent.change(input, { target: { value: "https://cohive.example.com/" } });
  fireEvent.click(screen.getByText("保存"));
  await waitFor(() => expect(bodies).toEqual([{ external_url: "https://cohive.example.com" }]));
  expect(await screen.findByText("已保存，立即生效。")).toBeTruthy();
  expect(screen.queryByText(/尚未设置/)).toBeNull();
  expect((screen.getByLabelText("对外地址") as HTMLInputElement).value).toBe("https://cohive.example.com");
});

it("带路径的地址不能保存", () => {
  show({ external_url: "https://a.example.com", locked: false, suggested: "" });
  expect(screen.queryByText(/尚未设置/)).toBeNull();
  fireEvent.change(screen.getByLabelText("对外地址"), { target: { value: "https://a.example.com/cohive" } });
  expect((screen.getByText("保存") as HTMLButtonElement).disabled).toBe(true);
});

it("由环境变量锁定时只读", () => {
  show({ external_url: "https://env.example.com", locked: true, suggested: "" });
  expect(screen.getByText("https://env.example.com")).toBeTruthy();
  expect(screen.getByText(/由环境变量 COHIVE_EXTERNAL_URL 锁定/)).toBeTruthy();
  expect(screen.queryByLabelText("对外地址")).toBeNull();
  expect(screen.queryByText("保存")).toBeNull();
});
