import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createRootRoute,
  createRoute,
  createRouter,
  Link,
  Outlet,
  redirect,
  RouterProvider,
} from "@tanstack/react-router";
import { api, ApiError, setUnauthorizedHandler, UnauthorizedError } from "./api";
import { Layout, Notice } from "./components";
import { LoginPage } from "./pages/Login";
import { RegisterPage } from "./pages/Register";
import { BoardPage } from "./pages/Board";
import { IssuesPage } from "./pages/Issues";
import { IssueDetailPage } from "./pages/IssueDetail";
import { DeviceAuthPage } from "./pages/DeviceAuth";
import { MePage } from "./pages/Me";
import { PasswordPage } from "./pages/Password";
import { AdminPage, adminTabs, type AdminTab } from "./pages/Admin";
import { LoadError } from "./LoadError";
import "./styles.css";

const queryClient = new QueryClient({
  defaultOptions: {
    // 4xx（含未登录、无权限、不存在）重试没有意义
    queries: { retry: (count, err) => !(err instanceof ApiError && err.status >= 400 && err.status < 500) && count < 2 },
  },
});

const redirectSearch = (s: Record<string, unknown>): { redirect?: string } =>
  typeof s.redirect === "string" && s.redirect ? { redirect: s.redirect } : {};

function NotFound() {
  return (
    <div className="mx-auto max-w-md px-4 py-16">
      <Notice>
        这个页面不存在。<Link to="/" className="text-teal hover:underline">回到看板</Link>
      </Notice>
    </div>
  );
}

const rootRoute = createRootRoute({ component: Outlet, notFoundComponent: NotFound });

const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/login",
  component: LoginPage,
  validateSearch: redirectSearch,
});

// 邀请注册页：公开，不需要登录
const registerRoute = createRoute({ getParentRoute: () => rootRoute, path: "/i/$code", component: RegisterPage });

// 需要登录的页面：进入前确认会话；被要求改密码时任何页面都先去改密码
const layoutRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: "layout",
  component: Layout,
  errorComponent: LoadError,
  beforeLoad: async ({ location }) => {
    let me;
    try {
      me = await queryClient.ensureQueryData({ queryKey: ["me"], queryFn: api.me });
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

const boardRoute = createRoute({ getParentRoute: () => layoutRoute, path: "/", component: BoardPage });
const issuesRoute = createRoute({ getParentRoute: () => layoutRoute, path: "/issues", component: IssuesPage });
const issueRoute = createRoute({ getParentRoute: () => layoutRoute, path: "/issues/$id", component: IssueDetailPage });
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
const adminRoute = createRoute({
  getParentRoute: () => layoutRoute,
  path: "/admin",
  component: AdminPage,
  validateSearch: (s: Record<string, unknown>): { tab?: AdminTab } =>
    adminTabs.some((t) => t.id === s.tab) ? { tab: s.tab as AdminTab } : {},
});

const routeTree = rootRoute.addChildren([
  loginRoute,
  registerRoute,
  layoutRoute.addChildren([boardRoute, issuesRoute, issueRoute, deviceRoute, meRoute, passwordRoute, adminRoute]),
]);
const router = createRouter({ routeTree });

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}

// 会话失效（过期、退出、改密码后其他会话失效）时回到登录页，登录后回到原处
const publicPath = (p: string) => p === "/login" || p.startsWith("/i/");
setUnauthorizedHandler(() => {
  // 用浏览器地址而不是路由状态：首次加载的检查进行时路由状态可能还停在上一页
  const { pathname, search, hash } = window.location;
  if (publicPath(pathname)) return;
  queryClient.clear();
  router.navigate({ to: "/login", search: { redirect: pathname + search + hash } });
});

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>,
);
