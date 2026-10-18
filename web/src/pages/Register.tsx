import { useState } from "react";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Trans, useTranslation } from "react-i18next";
import { safeTrans } from "../i18n/safeTrans";
import { api, ApiError, needsPasswordLogin, type InviteInfo } from "../api";
import { memberNamePattern, minPasswordLength, roleLabel } from "../format";
import { LanguageSelect } from "../i18n/LanguageSelect";
import { inviteLookupFailure } from "../rules";
import { AuthShell, btn, Callout, ErrorText, Field, inputClass, errMsg } from "../ui";

const mono = <span className="font-mono" />;
const strong = <span className="font-medium" />;

export function RegisterPage() {
  const { t } = useTranslation("auth");
  const { code } = useParams({ from: "/i/$code" });
  const invite = useQuery({ queryKey: ["invite", code], queryFn: () => api.invite(code), retry: false });
  const d = invite.data;

  return (
    <AuthShell title={t("register.title")}>
      {invite.isPending ? (
        <p className="text-sm text-muted">{t("register.checking")}</p>
      ) : invite.isError ? (
        inviteLookupFailure(invite.error) === "invalid" ? (
          <InvalidInvite />
        ) : (
          <div>
            <ErrorText>
              {inviteLookupFailure(invite.error) === "busy"
                ? t("register.busy")
                : t("register.loadFailed", { message: invite.error.message })}
            </ErrorText>
            <button type="button" onClick={() => invite.refetch()} className={`${btn.secondary} mt-4`}>
              {t("register.retry")}
            </button>
          </div>
        )
      ) : !d || !d.valid ? (
        <InvalidInvite />
      ) : d.signed_in_as ? (
        <JoinAsSignedIn code={code} invite={d} signedInAs={d.signed_in_as} onSwitched={() => invite.refetch()} />
      ) : (
        <RegisterForm code={code} workspace={d.workspace_display || d.workspace} kind={d.kind} role={d.role} />
      )}
      <div className="mt-6 border-t border-line pt-4">
        <LanguageSelect />
      </div>
    </AuthShell>
  );
}

function InviteSummary({ workspace, kind, role }: { workspace: string; kind: string; role?: string }) {
  const { t } = useTranslation("auth");
  return (
    <div className="rounded border border-line bg-bg px-3 py-2 text-sm">
      <p>
        <Trans {...safeTrans} t={t} i18nKey="register.summary.workspace" values={{ workspace }} components={{ b: strong }} />
        {role && <span className="ml-2 text-muted">{t("register.summary.role", { role: roleLabel(role) })}</span>}
      </p>
      <p className="text-muted">
        {kind === "personal"
          ? t("register.summary.personal")
          : kind === "team"
            ? t("register.summary.team")
            : t("register.summary.other", { kind })}
      </p>
    </div>
  );
}

// 已登录的人打开邀请链接：以当前账号加入，不再新建账号
function JoinAsSignedIn({
  code,
  invite,
  signedInAs,
  onSwitched,
}: {
  code: string;
  invite: InviteInfo;
  signedInAs: string;
  onSwitched: () => void;
}) {
  const { t } = useTranslation("auth");
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [needPassword, setNeedPassword] = useState(false);
  const [mustChange, setMustChange] = useState(false);
  const [pending, setPending] = useState(false);
  const here = `/i/${encodeURIComponent(code)}`;

  const signOut = async (then: "login" | "stay") => {
    setBusy(true);
    try {
      await api.logout();
    } catch {
      // 退出失败也照样继续
    }
    setBusy(false);
    if (then === "login") {
      qc.clear();
      navigate({ to: "/login", search: { redirect: here } });
    } else {
      // 留在本页：清掉账号相关的缓存，重新查一次邀请（这次不带会话，显示注册表单）
      qc.removeQueries({ predicate: (q) => q.queryKey[0] !== "invite" });
      onSwitched();
    }
  };

  const accept = async () => {
    setBusy(true);
    setError("");
    try {
      const res = await api.acceptInvite(code);
      if (res.status === "active") {
        await qc.invalidateQueries({ queryKey: ["me"] });
        navigate({ to: "/w/$ws", params: { ws: res.workspace.name } });
        return;
      }
      setPending(true);
    } catch (err) {
      if (needsPasswordLogin(err)) setNeedPassword(true);
      else if (err instanceof ApiError && err.status === 403 && err.body.must_change_password === true) setMustChange(true);
      else setError(errMsg(err));
    } finally {
      setBusy(false);
    }
  };

  const wsName = invite.workspace_display || invite.workspace;

  if (invite.already_member) {
    return (
      <div className="space-y-4 text-sm">
        <Callout tone="teal">
          <Trans {...safeTrans}
            t={t}
            i18nKey="register.joined.already"
            values={{ account: signedInAs, workspace: wsName }}
            components={{ m: mono, b: strong }}
          />
        </Callout>
        <Link to="/w/$ws" params={{ ws: invite.workspace }} className="inline-block text-teal hover:underline">
          {t("register.joined.enter", { workspace: wsName })}
        </Link>
      </div>
    );
  }

  if (pending) {
    return (
      <div className="space-y-4 text-sm">
        <Callout tone="teal">
          <Trans {...safeTrans} t={t} i18nKey="register.joined.pending" values={{ workspace: wsName }} components={{ b: strong }} />
        </Callout>
        <Link to="/" className="inline-block text-teal hover:underline">
          {t("register.joined.home")}
        </Link>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <InviteSummary workspace={wsName} kind={invite.kind} role={invite.role} />
      <p className="text-sm">
        <Trans {...safeTrans}
          t={t}
          i18nKey="register.joined.signedInAs"
          values={{ account: signedInAs }}
          components={{ m: <span className="font-mono font-medium" /> }}
        />
      </p>
      {needPassword ? (
        <div className="space-y-3">
          <Callout>{t("register.joined.needPassword")}</Callout>
          <button type="button" onClick={() => signOut("login")} disabled={busy} className={btn.primary}>
            {t("register.joined.signOutAndLogin")}
          </button>
        </div>
      ) : mustChange ? (
        <Callout>
          <Trans
            t={t}
            i18nKey="register.joined.mustChange"
            components={{ l: <Link to="/password" search={{ redirect: here }} className="text-teal hover:underline" /> }}
          />
        </Callout>
      ) : (
        <>
          <ErrorText>{error}</ErrorText>
          <button type="button" onClick={accept} disabled={busy} className={`${btn.primary} w-full`}>
            {busy ? t("register.joined.joining") : t("register.joined.join", { account: signedInAs, workspace: wsName })}
          </button>
        </>
      )}
      <p className="text-sm text-muted">
        <Trans
          t={t}
          i18nKey="register.joined.notYou"
          components={{
            b: (
              <button
                type="button"
                onClick={() => signOut("stay")}
                disabled={busy}
                className="text-teal hover:underline"
              />
            ),
          }}
        />
      </p>
    </div>
  );
}

function InvalidInvite() {
  const { t } = useTranslation("auth");
  return (
    <div className="space-y-3 text-sm">
      <p className="text-danger">{t("register.invalid.title")}</p>
      <p className="text-muted">{t("register.invalid.hint")}</p>
      <Link to="/login" className="inline-block text-teal hover:underline">
        {t("register.invalid.login")}
      </Link>
    </div>
  );
}

function RegisterForm({ code, workspace, kind, role }: { code: string; workspace: string; kind: string; role?: string }) {
  const { t } = useTranslation("auth");
  const [name, setName] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [submitted, setSubmitted] = useState(false);
  const navigate = useNavigate();
  const qc = useQueryClient();

  const nameError = name && !memberNamePattern.test(name) ? t("register.form.nameError") : "";
  const passwordError =
    password && password.length < minPasswordLength ? t("register.form.passwordError", { min: minPasswordLength }) : "";
  const confirmError = confirm && confirm !== password ? t("register.form.confirmError") : "";
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
          <Trans {...safeTrans} t={t} i18nKey="register.form.submitted" values={{ name }} components={{ m: <span className="font-mono font-medium" /> }} />
        </Callout>
        <Link to="/login" className="inline-block text-teal hover:underline">
          {t("register.form.toLogin")}
        </Link>
      </div>
    );
  }

  return (
    <form onSubmit={submit} className="space-y-4">
      <InviteSummary workspace={workspace} kind={kind} role={role} />
      <Field
        label={t("register.form.name")}
        htmlFor="name"
        hint={nameError ? undefined : t("register.form.nameHint")}
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
      <Field label={t("register.form.displayName")} htmlFor="display" hint={t("register.form.displayNameHint")}>
        <input
          id="display"
          autoComplete="nickname"
          maxLength={64}
          value={displayName}
          onChange={(e) => setDisplayName(e.target.value)}
          className={inputClass}
        />
      </Field>
      <Field
        label={t("register.form.password")}
        htmlFor="password"
        hint={passwordError ? undefined : t("register.form.passwordHint", { min: minPasswordLength })}
      >
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
      <Field label={t("register.form.confirm")} htmlFor="confirm">
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
        {busy ? t("register.form.submitting") : t("register.form.submit")}
      </button>
      <p className="text-sm text-muted">
        <Trans
          t={t}
          i18nKey="register.form.haveAccount"
          components={{
            l: (
              <Link
                to="/login"
                search={{ redirect: `/i/${encodeURIComponent(code)}` }}
                className="text-teal hover:underline"
              />
            ),
          }}
        />
      </p>
    </form>
  );
}
