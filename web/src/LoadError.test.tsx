// @vitest-environment jsdom
import { afterEach, expect, it } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from "@tanstack/react-router";
import { LoadError } from "./LoadError";

afterEach(cleanup);

// 进入前的检查第一次失败、第二次成功：点“重试”后页面应正常显示（检查被重新执行）。
it("重试会重新执行进入前的检查", async () => {
  let calls = 0;
  const root = createRootRoute({ component: Outlet });
  const page = createRoute({
    getParentRoute: () => root,
    path: "/",
    errorComponent: LoadError,
    beforeLoad: () => {
      calls++;
      if (calls === 1) throw new Error("网络中断");
    },
    component: () => <p>页面正常</p>,
  });
  const router = createRouter({
    routeTree: root.addChildren([page]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
  render(<RouterProvider router={router} />);
  expect(await screen.findByText(/页面加载失败/)).toBeTruthy();
  fireEvent.click(screen.getByText("重试"));
  expect(await screen.findByText("页面正常")).toBeTruthy();
  expect(calls).toBe(2);
});
