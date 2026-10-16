import { useState } from "react";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError } from "../api";
import { memberNamePattern, minPasswordLength } from "../format";
import { AuthShell, btn, Callout, ErrorText, Field, inputClass, errMsg } from "../ui";

export function RegisterPage() {
  const { code } = useParams({ from: "/i/$code" });
  const invite = useQuery({ queryKey: ["invite", code], queryFn: () => api.invite(code), retry: false });

  return (
    <AuthShell title="加入 Cohive">
      {invite.isPending ? (
        <p className="text-sm text-muted">正在核对邀请链接…</p>
      ) : invite.isError ? (
        invite.error instanceof ApiError && invite.error.status >= 400 && invite.error.status < 500 ? (
          <InvalidInvite />
        ) : (
          <div>
            <ErrorText>读取邀请失败：{invite.error.message}</ErrorText>
            <button type="button" onClick={() => invite.refetch()} className={`${btn.secondary} mt-4`}>
              重试
            </button>
          </div>
        )
      ) : !invite.data.valid ? (
        <InvalidInvite />
      ) : (
        <RegisterForm code={code} workspace={invite.data.workspace} kind={invite.data.kind} />
      )}
    </AuthShell>
  );
}

function InvalidInvite() {
  return (
    <div className="space-y-3 text-sm">
      <p className="text-danger">这个邀请链接无效：可能已过期、已被作废，或者名额已经用完。</p>
      <p className="text-muted">请向管理员要一个新的邀请链接。已经有账号的话可以直接登录。</p>
      <Link to="/login" className="inline-block text-teal hover:underline">
        去登录
      </Link>
    </div>
  );
}

function RegisterForm({ code, workspace, kind }: { code: string; workspace: string; kind: string }) {
  const [name, setName] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [submitted, setSubmitted] = useState(false);
  const navigate = useNavigate();
  const qc = useQueryClient();

  const nameError = name && !memberNamePattern.test(name) ? "只能用小写字母、数字、下划线和横线，1 到 32 位。" : "";
  const passwordError =
    password && password.length < minPasswordLength ? `密码至少 ${minPasswordLength} 位。` : "";
  const confirmError = confirm && confirm !== password ? "两次输入的密码不一致。" : "";
  const ready =
    memberNamePattern.test(name) && password.length >= minPasswordLength && confirm === password && !busy;

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!ready) return;
    setBusy(true);
    setError("");
    try {
      const res = await api.register({ code, name, display_name: displayName.trim(), password });
      if (res.status === "active") {
        qc.clear();
        navigate({ to: "/me" });
        return;
      }
      setSubmitted(true);
    } catch (err) {
      setError(errMsg(err));
    } finally {
      setBusy(false);
    }
  };

  if (submitted) {
    return (
      <div className="space-y-4 text-sm">
        <Callout tone="teal">
          已提交，等待管理员批准。批准后用成员名 <span className="font-mono font-medium">{name}</span>{" "}
          和刚才设的密码登录。
        </Callout>
        <Link to="/login" className="inline-block text-teal hover:underline">
          去登录页
        </Link>
      </div>
    );
  }

  return (
    <form onSubmit={submit} className="space-y-4">
      <div className="rounded border border-line bg-bg px-3 py-2 text-sm">
        <p>
          工作空间：<span className="font-medium">{workspace}</span>
        </p>
        <p className="text-muted">
          {kind === "personal"
            ? "个人邀请：注册后立即可用。"
            : kind === "team"
              ? "团队邀请：注册后需要管理员批准才能登录。"
              : `邀请类型：${kind}`}
        </p>
      </div>
      <Field
        label="成员名"
        htmlFor="name"
        hint={nameError ? undefined : "用于登录和在问题单里被指派，注册后不能修改。小写字母、数字、下划线、横线，1 到 32 位。"}
      >
        <input
          id="name"
          autoComplete="username"
          autoCapitalize="none"
          spellCheck={false}
          maxLength={32}
          value={name}
          onChange={(e) => setName(e.target.value)}
          aria-invalid={!!nameError}
          className={inputClass}
        />
        {nameError && <p className="mt-1 text-xs text-danger">{nameError}</p>}
      </Field>
      <Field label="显示名" htmlFor="display" hint="同事在看板上看到的名字，可以用中文，可留空。">
        <input
          id="display"
          autoComplete="nickname"
          maxLength={64}
          value={displayName}
          onChange={(e) => setDisplayName(e.target.value)}
          className={inputClass}
        />
      </Field>
      <Field label="密码" htmlFor="password" hint={passwordError ? undefined : `至少 ${minPasswordLength} 位。`}>
        <input
          id="password"
          type="password"
          autoComplete="new-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          aria-invalid={!!passwordError}
          className={inputClass}
        />
        {passwordError && <p className="mt-1 text-xs text-danger">{passwordError}</p>}
      </Field>
      <Field label="确认密码" htmlFor="confirm">
        <input
          id="confirm"
          type="password"
          autoComplete="new-password"
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
          aria-invalid={!!confirmError}
          className={inputClass}
        />
        {confirmError && <p className="mt-1 text-xs text-danger">{confirmError}</p>}
      </Field>
      <ErrorText>{error}</ErrorText>
      <button type="submit" disabled={!ready} className={`${btn.primary} w-full`}>
        {busy ? "正在提交…" : "注册"}
      </button>
      <p className="text-sm text-muted">
        已经有账号？
        <Link to="/login" className="text-teal hover:underline">
          直接登录
        </Link>
      </p>
    </form>
  );
}
