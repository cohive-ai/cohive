import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { MutationCache, QueryCache, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  redirect,
  RouterProvider,
} from "@tanstack/react-router";
import { clearToken, getToken, UnauthorizedError } from "./api";
import { Layout } from "./components";
import { LoginPage } from "./pages/Login";
import { BoardPage } from "./pages/Board";
import { IssuesPage } from "./pages/Issues";
import { IssueDetailPage } from "./pages/IssueDetail";
import "./styles.css";

const rootRoute = createRootRoute({ component: Outlet });

const loginRoute = createRoute({ getParentRoute: () => rootRoute, path: "/login", component: LoginPage });

const layoutRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: "layout",
  component: Layout,
  beforeLoad: () => {
    if (!getToken()) throw redirect({ to: "/login" });
  },
});

const boardRoute = createRoute({ getParentRoute: () => layoutRoute, path: "/", component: BoardPage });
const issuesRoute = createRoute({ getParentRoute: () => layoutRoute, path: "/issues", component: IssuesPage });
const issueRoute = createRoute({ getParentRoute: () => layoutRoute, path: "/issues/$id", component: IssueDetailPage });

const routeTree = rootRoute.addChildren([loginRoute, layoutRoute.addChildren([boardRoute, issuesRoute, issueRoute])]);
const router = createRouter({ routeTree });

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}

// 令牌失效（例如被吊销）时回到登录页
const onError = (err: unknown) => {
  if (err instanceof UnauthorizedError) {
    clearToken();
    queryClient.clear();
    router.navigate({ to: "/login" });
  }
};

const queryClient = new QueryClient({
  queryCache: new QueryCache({ onError }),
  mutationCache: new MutationCache({ onError }),
  defaultOptions: {
    queries: { retry: (count, err) => !(err instanceof UnauthorizedError) && count < 2 },
  },
});

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>,
);
