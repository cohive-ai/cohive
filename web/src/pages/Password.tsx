import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useRouter, useSearch } from "@tanstack/react-router";
import { api, type Me } from "../api";
import { minPasswordLength, safeRedirect } from "../format";
import { btn, Callout, ErrorText, Field, inputClass, errMsg, SuccessText } from "../ui";

// 首次登录或被重置密码后必须先改密码；也作为个人中心里的改密码区块
export function PasswordPage() {
  const { redirect } = useSearch({ from: "/layout/password" });
  const me = useQuery({ queryKey: ["me"], queryFn: api.me });
  const router = useRouter();
  const forced = !!me.data?.must_change_password;

  return (
    <div className="mx-auto max-w-md space-y-4">
      <h1 className="text-lg font-semibold">修改密码</h1>
      {forced && <Callout>为了账号安全，请先设置一个新密码，之后才能使用其他页面。</Callout>}
      <div className="rounded-lg border border-line bg-surface p-4 sm:p-5">
        <ChangePasswordForm
          hasPassword={me.data?.has_password ?? true}
          onDone={() => {
            if (forced) router.history.push(safeRedirect(redirect, "/me"));
          }}
        />
      </div>
    </div>
  );
}

export function ChangePasswordForm({ hasPassword, onDone }: { hasPassword: boolean; onDone?: () => void }) {
  const qc = useQueryClient();
  const [oldPassword, setOld] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState("");
  const [ok, setOk] = useState("");
  const [busy, setBusy] = useState(false);

  const nextError = next && next.length < minPasswordLength ? `新密码至少 ${minPasswordLength} 位。` : "";
  const confirmError = confirm && confirm !== next ? "两次输入的新密码不一致。" : "";
  const ready =
    (!hasPassword || oldPassword.length > 0) && next.length >= minPasswordLength && confirm === next && !busy;

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!ready) return;
    setBusy(true);
    setError("");
    setOk("");
    try {
      await api.changePassword({ old_password: oldPassword, new_password: next });
      qc.setQueryData<Me>(["me"], (cur) => (cur ? { ...cur, must_change_password: false, has_password: true } : cur));
      await qc.invalidateQueries({ queryKey: ["me"] });
      setOld("");
      setNext("");
      setConfirm("");
      setOk("密码已修改。其他浏览器上的登录已失效，需要用新密码重新登录。");
      onDone?.();
    } catch (err) {
      setError(errMsg(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} className="space-y-4">
      <Field
        label="旧密码"
        htmlFor="old-password"
        hint={hasPassword ? undefined : "你还没有设过密码（例如通过免密链接登录），旧密码可以留空。"}
      >
        <input
          id="old-password"
          type="password"
          autoComplete="current-password"
          value={oldPassword}
          onChange={(e) => setOld(e.target.value)}
          className={inputClass}
        />
      </Field>
      <Field label="新密码" htmlFor="new-password" hint={nextError ? undefined : `至少 ${minPasswordLength} 位。`}>
        <input
          id="new-password"
          type="password"
          autoComplete="new-password"
          value={next}
          onChange={(e) => setNext(e.target.value)}
          aria-invalid={!!nextError}
          className={inputClass}
        />
        {nextError && <p className="mt-1 text-xs text-danger">{nextError}</p>}
      </Field>
      <Field label="确认新密码" htmlFor="confirm-password">
        <input
          id="confirm-password"
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
      <SuccessText>{ok}</SuccessText>
      <button type="submit" disabled={!ready} className={btn.primary}>
        {busy ? "正在保存…" : "修改密码"}
      </button>
    </form>
  );
}
