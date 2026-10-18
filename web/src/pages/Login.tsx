import { useState } from "react";
import { useRouter, useSearch } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { Trans, useTranslation } from "react-i18next";
import { api, ApiError } from "../api";
import { safeRedirect } from "../format";
import { LanguageSelect } from "../i18n/LanguageSelect";
import { AuthShell, btn, ErrorText, Field, inputClass, errMsg, Callout } from "../ui";

// 服务端没给出说明时（接口只回了通用错误），网页用自己的说明；给了就照服务端的
const hasServerMessage = (err: ApiError) => typeof err.body.error === "string" && err.body.error !== "";

export function LoginPage() {
  const { t } = useTranslation("auth");
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
        setBlocked(hasServerMessage(err) ? err.message : t("login.blocked"));
      } else if (err instanceof ApiError && err.status === 401) {
        setError(hasServerMessage(err) ? err.message : t("login.badCredentials"));
      } else if (err instanceof ApiError && err.status === 429) {
        setError(hasServerMessage(err) ? err.message : t("login.tooMany"));
      } else {
        setError(errMsg(err));
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <AuthShell title={t("login.title")}>
      <form onSubmit={submit} className="space-y-4">
        <Field label={t("login.name")} htmlFor="name">
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
        <Field label={t("login.password")} htmlFor="password">
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
          {busy ? t("login.submitting") : t("login.submit")}
        </button>
      </form>
      <div className="mt-6 space-y-2 border-t border-line pt-4 text-sm text-muted">
        <p>
          <Trans
            t={t}
            i18nKey="login.cliHint"
            components={{ code: <code className="rounded bg-sunken px-1 font-mono text-ink" /> }}
          />
        </p>
        <p>{t("login.noAccount")}</p>
        <LanguageSelect />
      </div>
    </AuthShell>
  );
}
