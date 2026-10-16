import { useState } from "react";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError, needsPasswordLogin, type InviteInfo } from "../api";
import { memberNamePattern, minPasswordLength, roleLabel } from "../format";
import { inviteLookupFailure } from "../rules";
import { AuthShell, btn, Callout, ErrorText, Field, inputClass, errMsg } from "../ui";

export function RegisterPage() {
  const { code } = useParams({ from: "/i/$code" });
  const invite = useQuery({ queryKey: ["invite", code], queryFn: () => api.invite(code), retry: false });
  const d = invite.data;

  return (
    <AuthShell title="加入 Cohive">
      {invite.isPending ? (
        <p className="text-sm text-muted">正在核对邀请链接…</p>
      ) : invite.isError ? (
        inviteLookupFailure(invite.error) === "invalid" ? (
          <InvalidInvite />
        ) : (
          <div>
            <ErrorText>
              {inviteLookupFailure(invite.error) === "busy"
                ? "请求过于频繁，请稍等一会儿再点重试。"
                : `读取邀请失败：${invite.error.message}`}
            </ErrorText>
            <button type="button" onClick={() => invite.refetch()} className={`${btn.secondary} mt-4`}>
              重试
            </button>
          </div>
        )
      ) : !d || !d.valid ? (
        <InvalidInvite />
      ) : d.signed_in_as ? (
        <JoinAsSignedIn code={code} invite={d} signedInAs={d.signed_in_as} onSwitched={() => invite.refetch()} />
      ) : (
        <RegisterForm code={code} workspace={d.workspace} kind={d.kind} role={d.role} />
      )}
    </AuthShell>
  );
}

function InviteSummary({ workspace, kind, role }: { workspace: string; kind: string; role?: string }) {
  return (
    <div className="rounded border border-line bg-bg px-3 py-2 text-sm">
      <p>
        工作空间：<span className="font-medium">{workspace}</span>
        {role && <span className="ml-2 text-muted">加入后为{roleLabel(role)}</span>}
      </p>
      <p className="text-muted">
        {kind === "personal"
          ? "个人邀请：加入后立即可用。"
          : kind === "team"
            ? "团队邀请：需要管理员批准后才能加入。"
            : `邀请类型：${kind}`}
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

  if (invite.already_member) {
    return (
      <div className="space-y-4 text-sm">
        <Callout tone="teal">
          你（<span className="font-mono">{signedInAs}</span>）已经是工作空间 <span className="font-medium">{invite.workspace}</span>{" "}
          的成员，不需要再用这个邀请。
        </Callout>
        <Link to="/w/$ws" params={{ ws: invite.workspace }} className="inline-block text-teal hover:underline">
          进入 {invite.workspace}
        </Link>
      </div>
    );
  }

  if (pending) {
    return (
      <div className="space-y-4 text-sm">
        <Callout tone="teal">
          已提交加入申请，等待工作空间 <span className="font-medium">{invite.workspace}</span> 的管理员批准。批准后它会出现在顶栏的工作空间列表里。
        </Callout>
        <Link to="/" className="inline-block text-teal hover:underline">
          回到首页
        </Link>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <InviteSummary workspace={invite.workspace} kind={invite.kind} role={invite.role} />
      <p className="text-sm">
        你已登录为 <span className="font-mono font-medium">{signedInAs}</span>。接受邀请后，这个账号会成为该工作空间的成员，不会新建账号。
      </p>
      {needPassword ? (
        <div className="space-y-3">
          <Callout>
            接受邀请需要用账号密码登录。你现在是用已配对电脑免密打开的网页（cohive web），为安全起见不能接受邀请。
          </Callout>
          <button type="button" onClick={() => signOut("login")} disabled={busy} className={btn.primary}>
            退出并用账号密码登录
          </button>
        </div>
      ) : mustChange ? (
        <Callout>
          请先
          <Link to="/password" search={{ redirect: here }} className="text-teal hover:underline">
            修改密码
          </Link>
          ，改完会回到这里。
        </Callout>
      ) : (
        <>
          <ErrorText>{error}</ErrorText>
          <button type="button" onClick={accept} disabled={busy} className={`${btn.primary} w-full`}>
            {busy ? "正在加入…" : `以 ${signedInAs} 身份加入 ${invite.workspace}`}
          </button>
        </>
      )}
      <p className="text-sm text-muted">
        不是你的账号？
        <button type="button" onClick={() => signOut("stay")} disabled={busy} className="text-teal hover:underline">
          退出后换个账号
        </button>
      </p>
    </div>
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

function RegisterForm({ code, workspace, kind, role }: { code: string; workspace: string; kind: string; role?: string }) {
  const [name, setName] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [submitted, setSubmitted] = useState(false);
  const navigate = useNavigate();
  const qc = useQueryClient();

  const nameError = name && !memberNamePattern.test(name) ? "只能用小写字母、数字、下划线和横线，1 到 32 位，第一个字符须是字母或数字。" : "";
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
          已提交，等待管理员批准。批准后用登录名 <span className="font-mono font-medium">{name}</span>{" "}
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
      <InviteSummary workspace={workspace} kind={kind} role={role} />
      <Field
        label="登录名"
        htmlFor="name"
        hint={
          nameError
            ? undefined
            : "全服务器唯一，用于登录和在问题单里被指派，注册后不能修改。小写字母、数字、下划线、横线，1 到 32 位。"
        }
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
        已有账号？
        <Link to="/login" search={{ redirect: `/i/${encodeURIComponent(code)}` }} className="text-teal hover:underline">
          登录后加入
        </Link>
        ，不用再注册一个。
      </p>
    </form>
  );
}
