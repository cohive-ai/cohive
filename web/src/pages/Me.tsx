import { useEffect, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Trans, useTranslation } from "react-i18next";
import { safeTrans } from "../i18n/safeTrans";
import { api, ApiError, codeProviders, type Me } from "../api";
import { DeviceTokens, Notice } from "../components";
import { absoluteTime, roleLabel, workspaceLabel } from "../format";
import i18n, { followBrowserLanguage, languages, setLanguage } from "../i18n";
import { readThemePref, setThemePref, type ThemePref } from "../theme";
import { btn, ConfirmButton, CopyField, ErrorText, errMsg, inputClass, Section, SuccessText } from "../ui";
import { initCommands, installCommands, isTelegramLink, revokeDeviceMessage, shouldRetryPoll } from "../rules";
import { ChangePasswordForm } from "./Password";
import { CodeLink } from "./ChatCode";

export function MePage() {
  const { t } = useTranslation(["me", "auth"]);
  const me = useQuery({ queryKey: ["me"], queryFn: api.me });

  if (me.isPending) return <Notice>{t("loading")}</Notice>;
  if (me.isError) return <Notice tone="danger">{t("loadFailed", { message: me.error.message })}</Notice>;
  const m = me.data;

  return (
    <div className="mx-auto max-w-3xl space-y-5">
      <h1 className="text-lg font-semibold">{t("title")}</h1>

      <Section title={t("basic.title")}>
        <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 text-sm">
          <dt className="text-muted">{t("basic.username")}</dt>
          <dd className="font-mono">{m.account.name}</dd>
          <dt className="text-muted">{t("basic.displayName")}</dt>
          <dd>
            <DisplayNameEditor me={m} />
          </dd>
          {m.account.server_admin && (
            <>
              <dt className="text-muted">{t("basic.role")}</dt>
              <dd>{t("basic.serverAdmin")}</dd>
            </>
          )}
        </dl>
      </Section>

      <Section title={t("workspaces.title")} description={t("workspaces.description")}>
        {m.workspaces.length === 0 ? (
          <p className="text-sm text-muted">{t("workspaces.empty")}</p>
        ) : (
          <ul className="divide-y divide-line rounded border border-line">
            {m.workspaces.map((w) => (
              <li key={w.name} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2.5 text-sm">
                <Link to="/w/$ws" params={{ ws: w.name }} className="font-medium text-ink hover:text-accent">
                  {workspaceLabel(w)}
                </Link>
                <span
                  className={`rounded px-1.5 py-px text-xs ${
                    w.role === "admin" ? "border-[1.5px] border-ink bg-ink font-bold text-surface" : "bg-sunken text-muted"
                  }`}
                >
                  {roleLabel(w.role)}
                </span>
                {w.role === "admin" && (
                  <Link to="/w/$ws/admin" params={{ ws: w.name }} className="ml-auto text-accent hover:underline">
                    {t("workspaces.manage")}
                  </Link>
                )}
              </li>
            ))}
          </ul>
        )}
      </Section>

      <BindComputer workspaces={m.workspaces.map((w) => w.name)} />

      <MyDevices />

      <ChatTools me={m} />

      <Section title={t("auth:password.title")}>
        <ChangePasswordForm hasPassword={m.account.has_password} />
      </Section>

      <Section title={t("language.title")} description={t("language.description")}>
        <AccountLanguage me={m} />
      </Section>

      <Section title={t("appearance.title")} description={t("appearance.description")}>
        <Appearance />
      </Section>
    </div>
  );
}

// 显示名同事都能看到的位置（页头、看板、成员列表、问题单、群消息）都按它显示，保存后刷新这些缓存，不用整页刷新
const displayNameQueries = [["me"], ["board"], ["issues"], ["issue"], ["admin"], ["server", "accounts"], ["events"]];

// 显示名：本人可改可清空（清空后各处显示登录名）。不能和所在工作空间里其他成员的登录名或显示名相同，由服务器检查
function DisplayNameEditor({ me }: { me: Me }) {
  const { t } = useTranslation("me");
  const qc = useQueryClient();
  const current = me.account.display_name ?? "";
  const [editing, setEditing] = useState(false);
  const [value, setValue] = useState(current);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [ok, setOk] = useState("");

  const start = () => {
    setValue(current);
    setError("");
    setOk("");
    setEditing(true);
  };

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    setOk("");
    try {
      const res = await api.setMyDisplayName(value.trim());
      setEditing(false);
      setOk(res.display_name ? t("basic.displayNameSaved") : t("basic.displayNameCleared", { name: me.account.name }));
      await Promise.all(displayNameQueries.map((queryKey) => qc.invalidateQueries({ queryKey })));
    } catch (err) {
      setError(errMsg(err));
    } finally {
      setBusy(false);
    }
  };

  if (!editing) {
    return (
      <div>
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
          {current ? <span>{current}</span> : <span className="text-muted">{t("basic.notSet")}</span>}
          <button type="button" className="text-sm text-accent hover:underline" onClick={start}>
            {t("basic.editDisplayName")}
          </button>
        </div>
        {ok && <SuccessText>{ok}</SuccessText>}
      </div>
    );
  }

  return (
    <form onSubmit={save} className="max-w-sm">
      <input
        aria-label={t("basic.displayName")}
        value={value}
        maxLength={64}
        autoFocus
        disabled={busy}
        onChange={(e) => setValue(e.target.value)}
        placeholder={t("basic.displayNamePlaceholder", { name: me.account.name })}
        className={inputClass}
      />
      <p className="mt-1 text-xs text-muted">{t("basic.displayNameHint")}</p>
      <div className="mt-2 flex gap-2">
        <button type="submit" className={btn.primary} disabled={busy}>
          {t("basic.save")}
        </button>
        <button type="button" className={btn.secondary} disabled={busy} onClick={() => setEditing(false)}>
          {t("basic.cancel")}
        </button>
      </div>
      <ErrorText>{error}</ErrorText>
    </form>
  );
}

// 账号语言：存在账号上，网页、编程工具（MCP）、命令行与会话提醒都按它；没设时网页跟随浏览器，其余用服务器缺省语言
function AccountLanguage({ me }: { me: Me }) {
  const { t } = useTranslation("me");
  const qc = useQueryClient();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  // 旧版服务端不返回语言列表时，用网页自带的语言
  const options = me.languages ?? languages.map((code) => ({ code, name: i18n.getFixedT(code)("language.name") }));
  const defaultName = options.find((o) => o.code === me.default_language)?.name ?? me.default_language ?? "";

  const change = async (code: string) => {
    setBusy(true);
    setError("");
    try {
      await api.setMyLanguage(code);
      if (!code) await followBrowserLanguage();
      else if (languages.includes(code)) await setLanguage(code);
      await qc.invalidateQueries({ queryKey: ["me"] });
    } catch (err) {
      setError(errMsg(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div>
      <select
        aria-label={t("language.label")}
        className="rounded border border-line-strong bg-surface px-2 py-1 text-sm text-ink disabled:opacity-60"
        value={me.account.language ?? ""}
        disabled={busy}
        onChange={(e) => void change(e.target.value)}
      >
        <option value="">{t("language.unset", { name: defaultName })}</option>
        {options.map((o) => (
          <option key={o.code} value={o.code}>
            {o.name}
          </option>
        ))}
      </select>
      <ErrorText>{error}</ErrorText>
    </div>
  );
}

// 外观：浅色、深色或跟随系统。存在本机浏览器而不是账号上，见 theme.ts
function Appearance() {
  const { t } = useTranslation("me");
  const [pref, setPref] = useState<ThemePref>(readThemePref);
  return (
    <select
      aria-label={t("appearance.label")}
      className="rounded border border-line-strong bg-surface px-2 py-1 text-sm text-ink"
      value={pref}
      onChange={(e) => {
        const v = e.target.value as ThemePref;
        setThemePref(v);
        setPref(v);
      }}
    >
      <option value="system">{t("appearance.system")}</option>
      <option value="light">{t("appearance.light")}</option>
      <option value="dark">{t("appearance.dark")}</option>
    </select>
  );
}

// 绑定新电脑：一条命令装好命令行（只从本服务下载、核对校验和）并接着配对，再在项目目录初始化
function BindComputer({ workspaces }: { workspaces: string[] }) {
  const { t } = useTranslation("me");
  const v = useQuery({ queryKey: ["version"], queryFn: api.version, staleTime: 5 * 60_000 });
  const origin = window.location.origin;
  const cmd = installCommands(origin, true);
  const codeEl = <code className="rounded bg-sunken px-1 font-mono text-ink" />;

  return (
    <Section title={t("bind.title")} description={t("bind.description")}>
      <ol className="space-y-4 text-sm">
        <li>
          <p className="mb-2 font-medium">{t("bind.step1")}</p>
          {v.isPending ? (
            <p className="text-muted">{t("loading")}</p>
          ) : v.isError ? (
            <p className="text-danger">{t("bind.loadFailed", { message: v.error.message })}</p>
          ) : v.data.cli === null ? (
            <Notice tone="danger">{t("bind.noCli")}</Notice>
          ) : (
            <div className="space-y-2">
              <p className="text-muted">{t("bind.unixIntro")}</p>
              <CopyField label={t("bind.unixLabel")} value={cmd.unix} />
              <p className="text-muted">{t("bind.winIntro")}</p>
              <CopyField label={t("bind.winLabel")} value={cmd.windows} />
              <p className="text-muted">
                <Trans {...safeTrans} t={t} i18nKey="bind.after" values={{ version: v.data.cli.version }} components={{ code: codeEl }} />
              </p>
              <p className="text-muted">{t("bind.alreadyInstalled")}</p>
              <CopyField label={t("bind.pairLabel")} value={`cohive login --server ${origin}`} />
            </div>
          )}
        </li>
        <li>
          <p className="mb-2 font-medium">{t("bind.step2")}</p>
          <p className="mb-2 text-muted">{t("bind.step2Desc")}</p>
          {initCommands(workspaces).map((c) => (
            <CopyField key={c} label={t("bind.initLabel")} value={c} />
          ))}
        </li>
        <li>
          <p className="mb-1 font-medium">{t("bind.step3")}</p>
          <p className="text-muted">
            <Trans t={t} i18nKey="bind.step3Desc" components={{ code: codeEl }} />
          </p>
        </li>
      </ol>
    </Section>
  );
}

function MyDevices() {
  const { t } = useTranslation("me");
  const qc = useQueryClient();
  const list = useQuery({ queryKey: ["me", "devices"], queryFn: api.myDevices });

  return (
    <Section title={t("devices.title")} description={t("devices.description")}>
      {list.isPending ? (
        <p className="text-sm text-muted">{t("loading")}</p>
      ) : list.isError ? (
        <p className="text-sm text-danger">{t("loadFailed", { message: list.error.message })}</p>
      ) : list.data.items.length === 0 ? (
        <p className="text-sm text-muted">{t("devices.empty")}</p>
      ) : (
        <ul className="divide-y divide-line rounded border border-line">
          {list.data.items.map((d) => (
            <li key={d.id} className="flex flex-wrap items-center gap-x-4 gap-y-2 px-3 py-2.5 text-sm">
              <div className="min-w-0 flex-1">
                <p className="break-words font-medium">{d.name || t("devices.unnamed")}</p>
                <DeviceTokens tokens={d.tokens} revoked={!!d.revoked_at} />
                <p className="text-xs text-muted">
                  {[
                    t("devices.added", { time: absoluteTime(d.created_at) }),
                    d.last_used_at && t("devices.lastUsed", { time: absoluteTime(d.last_used_at) }),
                    d.revoked_at && t("devices.revokedAt", { time: absoluteTime(d.revoked_at) }),
                  ]
                    .filter(Boolean)
                    .join(" · ")}
                </p>
              </div>
              {d.revoked_at ? (
                <span className="rounded bg-sunken px-1.5 py-px text-xs text-muted">{t("devices.revokedTag")}</span>
              ) : (
                <>
                  <span className="rounded bg-teal-soft px-1.5 py-px text-xs text-teal">{t("devices.active")}</span>
                  <ConfirmButton
                    label={t("devices.revoke")}
                    title={t("devices.revokeTitle")}
                    message={revokeDeviceMessage(d.name, list.data.items.filter((x) => !x.revoked_at).length === 1)}
                    confirmLabel={t("devices.revokeConfirm")}
                    action={async () => {
                      await api.revokeMyDevice(d.id);
                      await qc.invalidateQueries({ queryKey: ["me", "devices"] });
                    }}
                  />
                </>
              )}
            </li>
          ))}
        </ul>
      )}
    </Section>
  );
}

interface LinkAttempt {
  id: number | string;
  url: string;
  startedAt: number;
}

const LINK_POLL_MS = 2000;
const LINK_TIMEOUT_MS = 10 * 60 * 1000;

function Telegram({ me }: { me: Me }) {
  const { t } = useTranslation("me");
  const qc = useQueryClient();
  const [attempt, setAttempt] = useState<LinkAttempt | null>(null);
  const [state, setState] = useState<{ status: string; detail?: string } | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const linked = !!me.account.telegram?.linked;

  // 每 2 秒查一次连接结果，最多等 10 分钟
  useEffect(() => {
    if (!attempt) return;
    let stopped = false;
    let timer: ReturnType<typeof setTimeout>;
    const tick = async () => {
      if (stopped) return;
      if (Date.now() - attempt.startedAt > LINK_TIMEOUT_MS) {
        setState({ status: "timeout" });
        return;
      }
      try {
        const res = await api.linkStatus(attempt.id);
        if (stopped) return;
        setState(res);
        setError("");
        if (res.status !== "pending") {
          if (res.status === "linked") qc.invalidateQueries({ queryKey: ["me"] });
          return;
        }
      } catch (err) {
        if (stopped) return;
        if (!shouldRetryPoll(err)) {
          // 401 已由全局处理回登录页；其余 4xx 重试也不会好，停下并提示
          if (!(err instanceof ApiError && err.status === 401)) {
            setState(null);
            setError(t("telegram.pollStopped", { message: errMsg(err) }));
          }
          return;
        }
        // 网络抖动或服务端暂时出错时继续等
        setError(t("telegram.pollRetry", { message: errMsg(err) }));
      }
      timer = setTimeout(tick, LINK_POLL_MS);
    };
    timer = setTimeout(tick, LINK_POLL_MS);
    return () => {
      stopped = true;
      clearTimeout(timer);
    };
  }, [attempt, qc, t]);

  const connect = async () => {
    setBusy(true);
    setError("");
    setState(null);
    // 先同步打开空白窗口，避免等接口返回后再打开被浏览器拦截
    const win = window.open("", "_blank");
    try {
      const res = await api.startLink();
      if (!isTelegramLink(res.url)) {
        throw new Error(t("telegram.badLink"));
      }
      if (win) {
        win.opener = null;
        win.location.href = res.url;
      }
      setAttempt({ id: res.id, url: res.url, startedAt: Date.now() });
      setState({ status: "pending" });
    } catch (err) {
      win?.close();
      setError(
        err instanceof ApiError && err.status === 503
          ? t("telegram.notEnabled")
          : t("telegram.startFailed", { message: errMsg(err) }),
      );
    } finally {
      setBusy(false);
    }
  };

  const status = state?.status;
  const waiting = status === "pending";

  return (
    <div>
      <div className="flex flex-wrap items-center gap-3 text-sm">
        <span className="font-medium">Telegram</span>
        {linked ? (
          <span className="rounded bg-teal-soft px-1.5 py-px text-xs text-teal">
            {me.account.telegram?.username
              ? t("telegram.connectedAs", { username: me.account.telegram.username })
              : t("telegram.connected")}
          </span>
        ) : (
          <span className="rounded bg-sunken px-1.5 py-px text-xs text-muted">{t("telegram.notConnected")}</span>
        )}
        <span className="ml-auto">
          {linked ? (
            <ConfirmButton
              label={t("telegram.unlink")}
              title={t("telegram.unlinkTitle")}
              message={t("telegram.unlinkMessage")}
              confirmLabel={t("telegram.unlinkConfirm")}
              action={async () => {
                await api.unlinkTelegram();
                setAttempt(null);
                setState(null);
                await qc.invalidateQueries({ queryKey: ["me"] });
              }}
            />
          ) : (
            <button type="button" onClick={connect} disabled={busy || waiting} className={btn.primary}>
              {busy ? t("telegram.opening") : waiting ? t("telegram.waiting") : t("telegram.connect")}
            </button>
          )}
        </span>
      </div>

      {attempt && waiting && !linked && (
        <div className="mt-3 rounded border border-line-strong bg-bg px-3 py-2 text-sm">
          <p>{t("telegram.opened")}</p>
          <p className="mt-1 text-muted">
            <Trans
              t={t}
              i18nKey="telegram.notOpened"
              components={{
                l: <a href={attempt.url} target="_blank" rel="noopener noreferrer" className="text-accent hover:underline" />,
              }}
            />
          </p>
          {state?.detail && <p className="mt-1 text-muted">{state.detail}</p>}
        </div>
      )}
      {status === "linked" && (
        <p className="mt-3 text-sm text-teal">{t("telegram.linked", { detail: state?.detail ?? "" })}</p>
      )}
      {(status === "failed" || status === "expired" || status === "timeout") && (
        <p className="mt-3 text-sm text-danger">
          {status === "failed"
            ? t("telegram.failed", { detail: state?.detail ?? "" })
            : t("telegram.expired", { detail: state?.detail ?? "" })}
        </p>
      )}
      <ErrorText>{error}</ErrorText>
    </div>
  );
}

// ChatTools 列出服务器启用的通讯工具（以及本人已连接的）：连接后群通知会 @ 到你，管理员也能在群里点按钮审批。
function ChatTools({ me }: { me: Me }) {
  const { t } = useTranslation("me");
  const providers = me.channels?.map((c) => c.provider);
  // 用码绑定的通讯工具：服务器启用了，或本人已连接（停用后仍可解除）
  const shown = codeProviders.filter((p) => !!providers?.includes(p) || !!me.chat?.some((c) => c.provider === p));
  // 旧版服务端不返回 channels；都没启用时照旧显示 Telegram（点了会提示没有启用）
  const showTG = !providers || providers.includes("telegram") || !!me.account.telegram?.linked || shown.length === 0;
  return (
    <Section title={t("chat.title")} description={t("chat.description")}>
      <div className="space-y-3">
        {showTG && <Telegram me={me} />}
        {shown.map((p) => (
          <CodeLink key={p} me={me} provider={p} />
        ))}
      </div>
    </Section>
  );
}
