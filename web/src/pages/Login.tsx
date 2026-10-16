import { useState } from "react";
import { useRouter, useSearch } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api";
import { safeRedirect } from "../format";
import { AuthShell, btn, ErrorText, Field, inputClass, errMsg, Callout } from "../ui";

export function LoginPage() {
  const { redirect, next } = useSearch({ from: "/login" });
  const router = useRouter();
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  // 待审或已停用：服务端给出的说明
  const [blocked, setBlocked] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim() || !password) return;
    setBusy(true);
    setError("");
    setBlocked("");
    try {
      await api.login({ name: name.trim(), password });
      qc.clear();
      // 去哪个工作空间由首页决定；改密码的强制跳转由进入站点时的检查负责
      router.history.push(safeRedirect(redirect ?? next));
    } catch (err) {
      if (err instanceof ApiError && err.status === 403) {
        setBlocked(err.message.startsWith("请求失败") ? "这个账号暂时不能登录，请联系管理员。" : err.message);
      } else if (err instanceof ApiError && err.status === 401) {
        setError(err.message.startsWith("请求失败") ? "登录名或密码不对。" : err.message);
      } else if (err instanceof ApiError && err.status === 429) {
        setError(err.message.startsWith("请求失败") ? "尝试次数太多，请稍等几分钟再试。" : err.message);
      } else {
        setError(errMsg(err));
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <AuthShell title="登录 Cohive">
      <form onSubmit={submit} className="space-y-4">
        <Field label="登录名" htmlFor="name">
          <input
            id="name"
            autoComplete="username"
            autoCapitalize="none"
            spellCheck={false}
            value={name}
            onChange={(e) => setName(e.target.value)}
            className={inputClass}
          />
        </Field>
        <Field label="密码" htmlFor="password">
          <input
            id="password"
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            className={inputClass}
          />
        </Field>
        {blocked && <Callout>{blocked}</Callout>}
        <ErrorText>{error}</ErrorText>
        <button type="submit" disabled={busy || !name.trim() || !password} className={`${btn.primary} w-full`}>
          {busy ? "正在登录…" : "登录"}
        </button>
      </form>
      <div className="mt-6 space-y-2 border-t border-line pt-4 text-sm text-muted">
        <p>
          也可以在已登录的电脑上执行 <code className="rounded bg-sunken px-1 font-mono text-ink">cohive web</code>{" "}
          免密打开。
        </p>
        <p>还没有账号？向管理员要一个邀请链接，打开后注册。忘记密码请联系服务器管理员重置。</p>
      </div>
    </AuthShell>
  );
}
