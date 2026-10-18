import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// 开发时把接口请求转发给本机的协作服务
export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    port: 5173,
    proxy: {
      "/api": "http://127.0.0.1:8787",
      // cohive web 生成的一次性免密登录链接
      "/auth": "http://127.0.0.1:8787",
      "/healthz": "http://127.0.0.1:8787",
    },
  },
  // vitest 读取 test 字段；vite 8 的配置类型里没有它
  ...({ test: { setupFiles: ["./src/test-setup.ts"] } } as object),
  build: {
    outDir: "dist",
    emptyOutDir: true,
    assetsDir: "assets",
  },
});
