import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useRouter, useSearch } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { api, type Me } from "../api";
import { minPasswordLength, safeRedirect } from "../format";
import { btn, Callout, ErrorText, Field, inputClass, errMsg, SuccessText } from "../ui";

// 首次登录或被重置密码后必须先改密码；也作为个人中心里的改密码区块
export function PasswordPage() {
  const { t } = useTranslation("auth");
  const { redirect } = useSearch({ from: "/layout/password" });
  const me = useQuery({ queryKey: ["me"], queryFn: api.me });
  const router = useRouter();
  const forced = !!me.data?.must_change_password;

  return (
    <div className="mx-auto max-w-md space-y-4">
      <h1 className="text-lg font-semibold">{t("password.title")}</h1>
      {forced && <Callout>{t("password.forced")}</Callout>}
      <div className="rounded-lg border border-line bg-surface p-4 sm:p-5">
        <ChangePasswordForm
          hasPassword={me.data?.account.has_password ?? true}
          onDone={() => {
            if (forced) router.history.push(safeRedirect(redirect, "/"));
          }}
        />
      </div>
    </div>
  );
}

export function ChangePasswordForm({ hasPassword, onDone }: { hasPassword: boolean; onDone?: () => void }) {
  const { t } = useTranslation("auth");
  const qc = useQueryClient();
  const [oldPassword, setOld] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState("");
  const [ok, setOk] = useState("");
  const [busy, setBusy] = useState(false);

  const nextError = next && next.length < minPasswordLength ? t("password.nextError", { min: minPasswordLength }) : "";
  const confirmError = confirm && confirm !== next ? t("password.confirmError") : "";
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
      qc.setQueryData<Me>(["me"], (cur) =>
        cur ? { ...cur, must_change_password: false, account: { ...cur.account, has_password: true } } : cur,
      );
      await qc.invalidateQueries({ queryKey: ["me"] });
      setOld("");
      setNext("");
      setConfirm("");
      setOk(t("password.done"));
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
        label={t("password.old")}
        htmlFor="old-password"
        hint={hasPassword ? undefined : t("password.oldHint")}
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
      <Field
        label={t("password.new")}
        htmlFor="new-password"
        hint={nextError ? undefined : t("password.newHint", { min: minPasswordLength })}
      >
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
      <Field label={t("password.confirm")} htmlFor="confirm-password">
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
        {busy ? t("password.saving") : t("password.submit")}
      </button>
    </form>
  );
}
