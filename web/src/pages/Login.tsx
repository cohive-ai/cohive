import { useState } from "react";
import { useRouter, useSearch } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api";
import { safeRedirect } from "../format";
import { AuthShell, btn, ErrorText, Field, inputClass, errMsg, Callout } from "../ui";

export function LoginPage() {
  const { redirect } = useSearch({ from: "/login" });
  const router = useRouter();
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  // 同名成员在多个工作空间时由服务器返回可选列表
  const [workspaces, setWorkspaces] = useState<string[]>([]);
  const [workspace, setWorkspace] = useState("");
  const [error, setError] = useState("");
  const [pending, setPending] = useState(false);
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim() || !password) return;
    if (workspaces.length > 0 && !workspace) {
      setError("请先选择要进入的工作空间。");
      return;
    }
    setBusy(true);
    setError("");
    setPending(false);
    try {
      await api.login({ name: name.trim(), password, workspace: workspace || undefined });
      qc.clear();
      // 改密码的强制跳转由进入站点时的检查负责
      router.history.push(safeRedirect(redirect));
    } catch (err) {
      if (err instanceof ApiError && err.status === 409 && Array.isArray(err.body.workspaces)) {
        const list = (err.body.workspaces as unknown[]).map(String);
        setWorkspaces(list);
        setWorkspace((cur) => (list.includes(cur) ? cur : ""));
        setError("这个成员名在多个工作空间里都有，请选择要进入的工作空间后再登录。");
      } else if (err instanceof ApiError && err.status === 403) {
        setPending(true);
      } else if (err instanceof ApiError && err.status === 401) {
        setError(err.message.startsWith("请求失败") ? "成员名或密码不对。" : err.message);
      } else if (err instanceof ApiError && err.status === 429) {
        setError("尝试次数太多，请稍等几分钟再试。");
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
        <Field label="成员名" htmlFor="name">
          <input
            id="name"
            autoComplete="username"
            autoCapitalize="none"
            spellCheck={false}
            value={name}
            onChange={(e) => {
              setName(e.target.value);
              setWorkspaces([]);
              setWorkspace("");
            }}
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
        {workspaces.length > 0 && (
          <Field label="工作空间" htmlFor="workspace">
            <select
              id="workspace"
              value={workspace}
              onChange={(e) => setWorkspace(e.target.value)}
              className={inputClass}
            >
              <option value="">请选择</option>
              {workspaces.map((w) => (
                <option key={w} value={w}>
                  {w}
                </option>
              ))}
            </select>
          </Field>
        )}
        {pending && (
          <Callout>你的注册申请还在等待管理员批准。批准后再用这个成员名和密码登录。</Callout>
        )}
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
        <p>还没有账号？向管理员要一个邀请链接，打开后注册。忘记密码请联系管理员重置。</p>
      </div>
    </AuthShell>
  );
}
