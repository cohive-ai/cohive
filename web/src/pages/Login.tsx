import { useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { api, setToken, UnauthorizedError } from "../api";

export function LoginPage() {
  const [token, setValue] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const navigate = useNavigate();
  const qc = useQueryClient();

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const t = token.trim();
    if (!t) return;
    setBusy(true);
    setError("");
    try {
      await api.me(t);
      setToken(t);
      qc.clear();
      navigate({ to: "/" });
    } catch (err) {
      setError(
        err instanceof UnauthorizedError
          ? "令牌无效或已被吊销。请向管理员确认，或用 cohive-server token issue 重新签发。"
          : `无法连接协作服务：${(err as Error).message}`,
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="flex min-h-dvh items-center justify-center px-4">
      <form onSubmit={submit} className="w-full max-w-md rounded-lg border border-line bg-surface p-6">
        <div className="mb-5 flex items-center gap-2">
          <svg viewBox="0 0 32 32" className="h-6 w-6 text-teal" aria-hidden="true">
            <path d="M16 3l11 6.5v13L16 29 5 22.5v-13z" fill="currentColor" />
          </svg>
          <h1 className="text-lg font-semibold">Cohive 看板</h1>
        </div>
        <label htmlFor="token" className="mb-1 block text-sm">
          成员令牌
        </label>
        <input
          id="token"
          type="password"
          autoComplete="off"
          value={token}
          onChange={(e) => setValue(e.target.value)}
          placeholder="cohive_…"
          className="w-full rounded border border-line bg-bg px-3 py-2 font-mono text-sm text-ink placeholder:text-muted"
        />
        <p className="mt-2 text-sm text-muted">
          看板只读，你看到的内容与这个成员在编程工具里看到的一致。令牌只保存在这台电脑的浏览器里。
        </p>
        {error && <p className="mt-3 text-sm text-danger">{error}</p>}
        <button
          type="submit"
          disabled={busy || !token.trim()}
          className="mt-5 w-full rounded bg-teal px-3 py-2 text-sm font-medium text-surface disabled:opacity-50"
        >
          {busy ? "正在验证…" : "进入看板"}
        </button>
      </form>
    </div>
  );
}
