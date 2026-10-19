import {
  createRootRoute,
  createRoute,
  createRouter,
  Link,
  Outlet,
  redirect,
  type RouterHistory,
} from "@tanstack/react-router";
import { Trans, useTranslation } from "react-i18next";
import type { QueryClient } from "@tanstack/react-query";
import { api, UnauthorizedError } from "./api";
import { Layout, NoWorkspace, Notice } from "./components";
import { LoginPage } from "./pages/Login";
import { RegisterPage } from "./pages/Register";
import { BoardPage } from "./pages/Board";
import { IssuesPage } from "./pages/Issues";
import { IssueDetailPage } from "./pages/IssueDetail";
import { DeviceAuthPage } from "./pages/DeviceAuth";
import { MePage } from "./pages/Me";
import { PasswordPage } from "./pages/Password";
import { AdminPage, adminTabs, type AdminTab } from "./pages/Admin";
import { ServerPage, serverTabs, type ServerTab } from "./pages/Server";
import { WorkspaceShell } from "./pages/WorkspaceShell";
import { pickWorkspace, readLastWorkspace } from "./workspace";
import { LoadError } from "./LoadError";

function NotFound() {
  const { t } = useTranslation();
  return (
    <div className="mx-auto max-w-md px-4 py-16">
      <Notice>
        <Trans t={t} i18nKey="notFound" components={{ home: <Link to="/" className="text-accent hover:underline" /> }} />
      </Notice>
    </div>
  );
}

// 网页路由。工作空间内的页面带 /w/{工作空间} 前缀；账号级页面不带。history 只在测试里传入。
export function createAppRouter(queryClient: QueryClient, history?: RouterHistory) {
  // 登录后回跳地址：redirect 是站内统一用的参数，next 作为别名也接受（例如邀请页“登录后加入”）
  const redirectSearch = (s: Record<string, unknown>): { redirect?: string; next?: string } => ({
    ...(typeof s.redirect === "string" && s.redirect ? { redirect: s.redirect } : {}),
    ...(typeof s.next === "string" && s.next ? { next: s.next } : {}),
  });

  const adminSearch = (s: Record<string, unknown>): { tab?: AdminTab } =>
    adminTabs.some((t) => t.id === s.tab) ? { tab: s.tab as AdminTab } : {};

  const rootRoute = createRootRoute({ component: Outlet, notFoundComponent: NotFound });

  const loginRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: "/login",
    component: LoginPage,
    validateSearch: redirectSearch,
  });

  // 邀请页：公开，不需要登录（已登录时可直接以当前账号加入）
  const registerRoute = createRoute({ getParentRoute: () => rootRoute, path: "/i/$code", component: RegisterPage });

  const loadMe = () => queryClient.ensureQueryData({ queryKey: ["me"], queryFn: api.me });

  // 上次用的工作空间（仍是成员时），否则第一个；一个都没有时为 null
  const defaultWorkspace = async () => pickWorkspace((await loadMe()).workspaces, readLastWorkspace());

  // 需要登录的页面：进入前确认会话；被要求改密码时任何页面都先去改密码
  const layoutRoute = createRoute({
    getParentRoute: () => rootRoute,
    id: "layout",
    component: Layout,
    errorComponent: LoadError,
    beforeLoad: async ({ location }) => {
      let me;
      try {
        me = await loadMe();
      } catch (err) {
        if (err instanceof UnauthorizedError) {
          throw redirect({ to: "/login", search: { redirect: location.href } });
        }
        throw err;
      }
      if (me.must_change_password && location.pathname !== "/password") {
        throw redirect({ to: "/password", search: { redirect: location.href } });
      }
    },
  });

  // 首页：跳到上次用的或第一个工作空间；一个都没有时显示提示
  const homeRoute = createRoute({
    getParentRoute: () => layoutRoute,
    path: "/",
    component: NoWorkspace,
    beforeLoad: async () => {
      const ws = await defaultWorkspace();
      if (ws) throw redirect({ to: "/w/$ws", params: { ws } });
    },
  });

  // 旧地址（群消息里 M5 之前的链接）：跳到上次用的工作空间对应的新地址
  const legacyIssuesRoute = createRoute({
    getParentRoute: () => layoutRoute,
    path: "/issues",
    beforeLoad: async () => {
      const ws = await defaultWorkspace();
      throw ws ? redirect({ to: "/w/$ws/issues", params: { ws } }) : redirect({ to: "/" });
    },
  });
  const legacyIssueRoute = createRoute({
    getParentRoute: () => layoutRoute,
    path: "/issues/$id",
    beforeLoad: async ({ params }) => {
      const ws = await defaultWorkspace();
      throw ws ? redirect({ to: "/w/$ws/issues/$id", params: { ws, id: params.id } }) : redirect({ to: "/" });
    },
  });
  const legacyAdminRoute = createRoute({
    getParentRoute: () => layoutRoute,
    path: "/admin",
    validateSearch: adminSearch,
    beforeLoad: async ({ search }) => {
      const ws = await defaultWorkspace();
      throw ws ? redirect({ to: "/w/$ws/admin", params: { ws }, search }) : redirect({ to: "/" });
    },
  });

  // 工作空间内的页面：地址带工作空间名，多个标签页可以同时看不同的工作空间
  const wsRoute = createRoute({ getParentRoute: () => layoutRoute, path: "/w/$ws", component: WorkspaceShell });
  const boardRoute = createRoute({ getParentRoute: () => wsRoute, path: "/", component: BoardPage });
  const issuesRoute = createRoute({ getParentRoute: () => wsRoute, path: "/issues", component: IssuesPage });
  const issueRoute = createRoute({ getParentRoute: () => wsRoute, path: "/issues/$id", component: IssueDetailPage });
  const adminRoute = createRoute({
    getParentRoute: () => wsRoute,
    path: "/admin",
    component: AdminPage,
    validateSearch: adminSearch,
  });

  // 账号级页面
  const deviceRoute = createRoute({
    getParentRoute: () => layoutRoute,
    path: "/device/$nonce",
    component: DeviceAuthPage,
  });
  const meRoute = createRoute({ getParentRoute: () => layoutRoute, path: "/me", component: MePage });
  const passwordRoute = createRoute({
    getParentRoute: () => layoutRoute,
    path: "/password",
    component: PasswordPage,
    validateSearch: redirectSearch,
  });
  const serverRoute = createRoute({
    getParentRoute: () => layoutRoute,
    path: "/server",
    component: ServerPage,
    validateSearch: (s: Record<string, unknown>): { tab?: ServerTab } =>
      serverTabs.some((t) => t.id === s.tab) ? { tab: s.tab as ServerTab } : {},
  });

  const routeTree = rootRoute.addChildren([
    loginRoute,
    registerRoute,
    layoutRoute.addChildren([
      homeRoute,
      legacyIssuesRoute,
      legacyIssueRoute,
      legacyAdminRoute,
      wsRoute.addChildren([boardRoute, issuesRoute, issueRoute, adminRoute]),
      deviceRoute,
      meRoute,
      passwordRoute,
      serverRoute,
    ]),
  ]);

  return createRouter({ routeTree, history });
}

export type AppRouter = ReturnType<typeof createAppRouter>;

declare module "@tanstack/react-router" {
  interface Register {
    router: AppRouter;
  }
}
