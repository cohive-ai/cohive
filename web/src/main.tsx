import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import "./i18n";
import { ApiError, setUnauthorizedHandler } from "./api";
import { createAppRouter } from "./router";
import "./styles.css";

const queryClient = new QueryClient({
  defaultOptions: {
    // 4xx（含未登录、无权限、不存在）重试没有意义
    queries: { retry: (count, err) => !(err instanceof ApiError && err.status >= 400 && err.status < 500) && count < 2 },
  },
});

const router = createAppRouter(queryClient);

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
