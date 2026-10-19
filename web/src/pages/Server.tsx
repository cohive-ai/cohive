import { useState } from "react";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { keepPreviousData, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, type CodeProvider, type ServerAccount, type ServerSettings, type ServerWorkspace } from "../api";

// linkedTo 判断账号是否连接了某种用码绑定的通讯工具（旧版服务端只有 dingtalk_linked）。
const linkedTo = (a: ServerAccount, p: CodeProvider) => (a.chat_linked ? a.chat_linked.includes(p) : p === "dingtalk" && !!a.dingtalk_linked);
import { NeedPasswordLogin, Notice } from "../components";
import { absoluteTime, memberNamePattern, roleLabel, workspaceName } from "../format";
import { btn, Callout, ConfirmButton, ErrorText, errMsg, Field, inputClass, Section, SuccessText } from "../ui";
import { adminNeedsPasswordLogin, joinNames } from "../rules";
import { PendingDeviceRow } from "./Admin";
import { EventLog } from "./EventLog";

export const serverTabs = [
  { id: "workspaces" },
  { id: "accounts" },
  { id: "devices" },
  { id: "settings" },
  { id: "events" },
] as const;

export type ServerTab = (typeof serverTabs)[number]["id"];

const listClass = "divide-y divide-line rounded-lg border border-line bg-surface";
const rowClass = "flex flex-wrap items-center gap-x-4 gap-y-2 px-3 py-3 text-sm";
const tag = "rounded px-1.5 py-px text-xs";

// 服务器管理：工作空间、账号、待批设备、管理操作记录。只有服务器管理员能用，且要求账号密码登录。
export function ServerPage() {
  const { t } = useTranslation("server");
  const tabLabel: Record<ServerTab, string> = {
    workspaces: t("tabs.workspaces"),
    accounts: t("tabs.accounts"),
    devices: t("tabs.devices"),
    settings: t("tabs.settings"),
    events: t("tabs.events"),
  };
  const { tab = "workspaces" } = useSearch({ from: "/layout/server" });
  const me = useQuery({ queryKey: ["me"], queryFn: api.me });

  if (me.isPending) return <Notice>{t("loading")}</Notice>;
  if (me.isError) return <Notice tone="danger">{t("loadFailed", { message: me.error.message })}</Notice>;
  if (!me.data.account.server_admin) {
    return <Notice tone="danger">{t("noPermission")}</Notice>;
  }
  if (adminNeedsPasswordLogin(me.data)) {
    return (
      <NeedPasswordLogin redirectTo="/server" message={t("needPassword")} />
    );
  }

  return (
    <div className="mx-auto max-w-4xl">
      <h1 className="mb-3 text-lg font-semibold">{t("title")}</h1>
      <nav aria-label={t("navLabel")} className="mb-5 flex flex-wrap gap-1 border-b border-line">
        {serverTabs.map((x) => (
          <Link
            key={x.id}
            to="/server"
            search={{ tab: x.id }}
            aria-current={tab === x.id ? "page" : undefined}
            className={`-mb-px border-b-[3px] px-3 py-2 text-sm ${
              tab === x.id ? "border-accent-fill font-semibold text-ink" : "border-transparent text-muted hover:text-ink"
            }`}
          >
            {tabLabel[x.id]}
          </Link>
        ))}
      </nav>
      {tab === "workspaces" && <WorkspacesTab self={me.data.account.name} />}
      {tab === "accounts" && <AccountsTab self={me.data.account.name} />}
      {tab === "devices" && <DevicesTab />}
      {tab === "settings" && <SettingsTab />}
      {tab === "events" && <EventLog scope={{ server: true }} />}
    </div>
  );
}

function WorkspacesTab({ self }: { self: string }) {
  const { t } = useTranslation("server");
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["server", "workspaces"], queryFn: api.serverWorkspaces });

  return (
    <div className="space-y-6">
      <NewWorkspaceForm self={self} />
      <section>
        <h2 className="mb-2 font-semibold">{t("workspaces.all")}</h2>
        {q.isPending ? (
          <Notice>{t("loading")}</Notice>
        ) : q.isError ? (
          <Notice tone="danger">{t("loadFailed", { message: q.error.message })}</Notice>
        ) : q.data.items.length === 0 ? (
          <Notice>{t("workspaces.empty")}</Notice>
        ) : (
          <ul className={listClass}>
            {q.data.items.map((w) => (
              <WorkspaceRow
                key={w.name}
                w={w}
                onDeleted={async () => {
                  await qc.invalidateQueries({ queryKey: ["server"] });
                  await qc.invalidateQueries({ queryKey: ["me"] });
                }}
              />
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}

function WorkspaceRow({ w, onDeleted }: { w: ServerWorkspace; onDeleted: () => Promise<void> }) {
  const { t } = useTranslation("server");
  const admins = w.admins ?? [];
  return (
    <li className={rowClass}>
      <div className="min-w-0 flex-1">
        <p className="flex flex-wrap items-center gap-x-2">
          <span className="font-medium">{workspaceName(w)}</span>
          {workspaceName(w) !== w.name && <span className="font-mono text-xs text-muted">{w.name}</span>}
          {w.i_am_member && <span className={`${tag} bg-teal-soft text-teal`}>{t("workspaces.member")}</span>}
          <span className={`${tag} ${w.telegram_bound ? "bg-sunken text-muted" : "bg-wait-soft text-wait"}`}>
            {w.telegram_bound ? t("workspaces.bound") : t("workspaces.unbound")}
          </span>
        </p>
        <p className="text-xs text-muted">
          {[
            t("workspaces.members", { count: w.members }),
            t("workspaces.admins", { names: admins.length ? joinNames(admins) : t("workspaces.noAdmins") }),
            t("workspaces.openIssues", { count: w.open_issues }),
            t("workspaces.created", { time: absoluteTime(w.created_at) }),
          ].join(" · ")}
        </p>
      </div>
      <div className="flex flex-wrap gap-2">
        {w.i_am_member && (
          <Link to="/w/$ws" params={{ ws: w.name }} className={btn.secondary}>
            {t("workspaces.board")}
          </Link>
        )}
        <Link to="/w/$ws/admin" params={{ ws: w.name }} className={btn.secondary}>
          {t("workspaces.manage")}
        </Link>
        <ConfirmButton
          label={t("workspaces.delete")}
          title={t("workspaces.deleteTitle", { name: w.name })}
          message={
            <>
              <p>{t("workspaces.deleteMessage1")}</p>
              <p className="mt-2">{t("workspaces.deleteMessage2")}</p>
            </>
          }
          confirmLabel={t("workspaces.deleteConfirm")}
          action={async () => {
            await api.deleteWorkspace(w.name);
            await onDeleted();
          }}
        />
      </div>
    </li>
  );
}

function NewWorkspaceForm({ self }: { self: string }) {
  const { t } = useTranslation("server");
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [name, setName] = useState("");
  const [display, setDisplay] = useState("");
  const [admin, setAdmin] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const trimmed = name.trim();
  const adminName = admin.trim();
  const nameError = trimmed && !memberNamePattern.test(trimmed) ? t("new.nameError") : "";
  const adminError = adminName && !memberNamePattern.test(adminName) ? t("new.adminError") : "";
  const ready = memberNamePattern.test(trimmed) && !adminError && !busy;

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!ready) return;
    setBusy(true);
    setError("");
    try {
      const res = await api.createWorkspace({
        name: trimmed,
        ...(display.trim() ? { display_name: display.trim() } : {}),
        ...(adminName ? { admin: adminName } : {}),
      });
      await qc.invalidateQueries({ queryKey: ["server"] });
      await qc.invalidateQueries({ queryKey: ["me"] });
      // 下一步：绑群、加人
      navigate({ to: "/w/$ws/admin", params: { ws: res?.name || trimmed }, search: { tab: "workspace" } });
    } catch (err) {
      setError(errMsg(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Section
      title={t("new.title")}
      description={t("new.description")}
    >
      <form onSubmit={submit} className="space-y-4">
        <div className="grid gap-4 sm:grid-cols-3">
          <Field label={t("new.path")} htmlFor="ws-name" hint={nameError ? undefined : t("new.pathHint")}>
            <input
              id="ws-name"
              autoCapitalize="none"
              spellCheck={false}
              maxLength={32}
              value={name}
              onChange={(e) => setName(e.target.value)}
              aria-invalid={!!nameError}
              className={`${inputClass} font-mono`}
            />
            {nameError && <p className="mt-1 text-xs text-danger">{nameError}</p>}
          </Field>
          <Field label={t("new.display")} htmlFor="ws-display" hint={t("new.displayHint")}>
            <input
              id="ws-display"
              maxLength={40}
              value={display}
              onChange={(e) => setDisplay(e.target.value)}
              className={inputClass}
            />
          </Field>
          <Field
            label={t("new.admin")}
            htmlFor="ws-admin"
            hint={adminError ? undefined : t("new.adminHint", { self })}
          >
            <input
              id="ws-admin"
              autoCapitalize="none"
              spellCheck={false}
              maxLength={32}
              value={admin}
              placeholder={self}
              onChange={(e) => setAdmin(e.target.value)}
              aria-invalid={!!adminError}
              className={`${inputClass} font-mono`}
            />
            {adminError && <p className="mt-1 text-xs text-danger">{adminError}</p>}
          </Field>
        </div>
        <ErrorText>{error}</ErrorText>
        <button type="submit" disabled={!ready} className={btn.primary}>
          {busy ? t("new.submitting") : t("new.submit")}
        </button>
      </form>
    </Section>
  );
}

function AccountsTab({ self }: { self: string }) {
  const { t } = useTranslation("server");
  const qc = useQueryClient();
  const [q, setQ] = useState("");
  const query = q.trim();
  const list = useQuery({
    queryKey: ["server", "accounts", query],
    queryFn: () => api.serverAccounts(query),
    placeholderData: keepPreviousData,
  });
  const refresh = async () => {
    await qc.invalidateQueries({ queryKey: ["server", "accounts"] });
    await qc.invalidateQueries({ queryKey: ["me"] });
  };

  return (
    <div className="space-y-3">
      <label className="sr-only" htmlFor="account-q">
        {t("accounts.searchLabel")}
      </label>
      <input
        id="account-q"
        autoCapitalize="none"
        spellCheck={false}
        value={q}
        onChange={(e) => setQ(e.target.value)}
        placeholder={t("accounts.searchPlaceholder")}
        className={`${inputClass} sm:max-w-sm`}
      />
      {list.isPending ? (
        <Notice>{t("loading")}</Notice>
      ) : list.isError ? (
        <Notice tone="danger">{t("loadFailed", { message: list.error.message })}</Notice>
      ) : list.data.items.length === 0 ? (
        <Notice>{query ? t("accounts.noMatch") : t("accounts.none")}</Notice>
      ) : (
        <ul className={listClass}>
          {list.data.items.map((a) => (
            <AccountRow key={a.name} a={a} isSelf={a.name === self} onChanged={refresh} />
          ))}
        </ul>
      )}
    </div>
  );
}

function AccountRow({ a, isSelf, onChanged }: { a: ServerAccount; isSelf: boolean; onChanged: () => Promise<void> }) {
  const { t } = useTranslation("server");
  const label = a.display_name && a.display_name !== a.name ? t("accounts.label", { display: a.display_name, name: a.name }) : a.name;
  const memberships = a.workspaces ?? [];
  return (
    <li className={rowClass}>
      <div className="min-w-0 flex-1">
        <p className="flex flex-wrap items-center gap-x-2">
          <span className="font-mono font-medium">{a.name}</span>
          {a.display_name && <span>{a.display_name}</span>}
          {isSelf && <span className="text-xs text-muted">{t("accounts.you")}</span>}
          {a.server_admin && <span className={`${tag} border-[1.5px] border-ink bg-ink font-bold text-surface`}>{t("accounts.serverAdmin")}</span>}
          {a.disabled && <span className={`${tag} border-[1.5px] border-danger bg-danger font-bold text-on-status`}>{t("accounts.disabled")}</span>}
        </p>
        <p className="text-xs text-muted">
          {[
            a.telegram_linked ? t("accounts.telegramLinked") : t("accounts.telegramNotLinked"),
            linkedTo(a, "dingtalk") ? t("accounts.dingtalkLinked") : "",
            linkedTo(a, "slack") ? t("accounts.slackLinked") : "",
            t("accounts.devices", { count: a.devices }),
          ]
            .filter(Boolean)
            .join(" · ")}
        </p>
        <p className="text-xs text-muted">
          {t("accounts.workspacesLabel")}
          {memberships.length === 0
            ? t("accounts.noWorkspaces")
            : memberships.map((m, i) => (
                <span key={m.name}>
                  {i > 0 && t("accounts.listSeparator")}
                  <span className={m.status === "removed" ? "line-through" : "text-ink"}>{m.name}</span>
                  {t("accounts.roleSuffix", { role: m.status === "removed" ? t("accounts.removed") : roleLabel(m.role) })}
                </span>
              ))}
        </p>
      </div>
      <div className="flex flex-wrap gap-2">
        <ConfirmButton
          label={a.server_admin ? t("accounts.unsetAdmin") : t("accounts.setAdmin")}
          danger={a.server_admin}
          title={a.server_admin ? t("accounts.unsetAdminTitle") : t("accounts.setAdminTitle")}
          message={
            a.server_admin
              ? isSelf
                ? t("accounts.unsetSelfMessage")
                : t("accounts.unsetOtherMessage", { label })
              : t("accounts.setAdminMessage", { label })
          }
          confirmLabel={t("accounts.confirm")}
          action={async () => {
            await api.setServerAdmin(a.name, !a.server_admin);
            await onChanged();
          }}
        />
        {a.disabled ? (
          <ConfirmButton
            label={t("accounts.enable")}
            title={t("accounts.enableTitle")}
            message={t("accounts.enableMessage", { label })}
            confirmLabel={t("accounts.enableConfirm")}
            danger={false}
            action={async () => {
              await api.enableAccount(a.name);
              await onChanged();
            }}
          />
        ) : (
          !isSelf && (
            <ConfirmButton
              label={t("accounts.disable")}
              title={t("accounts.disableTitle")}
              message={t("accounts.disableMessage", { label })}
              confirmLabel={t("accounts.disableConfirm")}
              action={async () => {
                await api.disableAccount(a.name);
                await onChanged();
              }}
            />
          )
        )}
        {linkedTo(a, "dingtalk") && (
          <ConfirmButton
            label={t("accounts.unlinkDingtalk")}
            title={t("accounts.unlinkDingtalkTitle")}
            message={t("accounts.unlinkDingtalkMessage", { label })}
            confirmLabel={t("accounts.unlinkConfirm")}
            action={async () => {
              await api.serverUnlink(a.name, "dingtalk");
              await onChanged();
            }}
          />
        )}
        {linkedTo(a, "slack") && (
          <ConfirmButton
            label={t("accounts.unlinkSlack")}
            title={t("accounts.unlinkSlackTitle")}
            message={t("accounts.unlinkSlackMessage", { label })}
            confirmLabel={t("accounts.unlinkConfirm")}
            action={async () => {
              await api.serverUnlink(a.name, "slack");
              await onChanged();
            }}
          />
        )}
        {a.telegram_linked && (
          <ConfirmButton
            label={t("accounts.unlinkTelegram")}
            title={t("accounts.unlinkTelegramTitle")}
            message={t("accounts.unlinkTelegramMessage", { label })}
            confirmLabel={t("accounts.unlinkConfirm")}
            action={async () => {
              await api.serverUnlinkTelegram(a.name);
              await onChanged();
            }}
          />
        )}
      </div>
    </li>
  );
}

function DevicesTab() {
  const { t } = useTranslation("server");
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["server", "pending-devices"], queryFn: api.serverPendingDevices, refetchInterval: 10000 });

  return (
    <div className="space-y-2">
      <p className="text-sm text-muted">{t("devices.description")}</p>
      {q.isPending ? (
        <Notice>{t("loading")}</Notice>
      ) : q.isError ? (
        <Notice tone="danger">{t("loadFailed", { message: q.error.message })}</Notice>
      ) : q.data.items.length === 0 ? (
        <Notice>{t("devices.empty")}</Notice>
      ) : (
        <ul className={listClass}>
          {q.data.items.map((v) => (
            <PendingDeviceRow
              key={v.id}
              v={v}
              onDecide={async (approve) => {
                await api.serverDecideDevice(v.id, approve);
                await qc.invalidateQueries({ queryKey: ["server", "pending-devices"] });
              }}
            />
          ))}
        </ul>
      )}
    </div>
  );
}

// 服务器设置：对外地址（参照 GitLab 的 external_url）。群消息里的链接与编程工具经隧道访问 MCP 时的主机名都用它。
function SettingsTab() {
  const { t } = useTranslation("server");
  const q = useQuery({ queryKey: ["server", "settings"], queryFn: api.serverSettings });
  if (q.isPending) return <Notice>{t("loading")}</Notice>;
  if (q.isError) return <Notice tone="danger">{t("loadFailed", { message: q.error.message })}</Notice>;
  return (
    <div className="space-y-4">
      <ExternalURLForm initial={q.data} />
      <ChannelsPanel />
    </div>
  );
}

// ChannelsPanel 显示服务器启用的通讯工具与连接状态（钉钉的 Stream 掉线、额度用完等原因在这里看）。
function ChannelsPanel() {
  const { t } = useTranslation("server");
  const q = useQuery({ queryKey: ["server", "channels"], queryFn: api.serverChannels, refetchInterval: 30_000 });
  return (
    <Section title={t("channels.title")} description={t("channels.description")}>
      {q.isPending ? (
        <p className="text-sm text-muted">{t("loading")}</p>
      ) : q.isError ? (
        <p className="text-sm text-danger">{t("loadFailed", { message: q.error.message })}</p>
      ) : q.data.channels.length === 0 ? (
        <p className="text-sm text-muted">{t("channels.none")}</p>
      ) : (
        <ul className="divide-y divide-line rounded border border-line text-sm">
          {q.data.channels.map((c) => (
            <li key={c.provider} className="space-y-1 px-3 py-2.5">
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-medium">
                  {c.provider === "dingtalk" ? t("channels.dingtalk") : c.provider === "slack" ? t("channels.slack") : "Telegram"}
                </span>
                {c.bot_name && <span className="text-muted">@{c.bot_name}</span>}
                {c.app_id && <span className="text-muted">{t("channels.appClientId", { id: c.app_id })}</span>}
                <span
                  className={`rounded px-1.5 py-px text-xs ${c.online ? "bg-teal-soft text-teal" : "bg-wait-soft text-wait"}`}
                >
                  {c.online ? t("channels.online") : t("channels.offline")}
                </span>
              </div>
              {c.detail && <p className="break-words text-xs text-muted">{c.detail}</p>}
            </li>
          ))}
        </ul>
      )}
    </Section>
  );
}

export function ExternalURLForm({ initial }: { initial: ServerSettings }) {
  const { t } = useTranslation("server");
  const qc = useQueryClient();
  // 还没设时用当前访问地址预填，但明确告诉人这还没保存
  const [saved, setSaved] = useState(initial.external_url);
  const unset = !saved;
  const [value, setValue] = useState(initial.external_url || initial.suggested);
  const [error, setError] = useState("");
  const [ok, setOk] = useState("");
  const [busy, setBusy] = useState(false);
  const trimmed = value.trim().replace(/\/+$/, "");
  const shapeOk = trimmed === "" || /^https?:\/\/[^\s/?#@]+$/i.test(trimmed);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!shapeOk) return;
    setBusy(true);
    setError("");
    setOk("");
    try {
      const res = await api.saveServerSettings(trimmed);
      setValue(res.external_url);
      setSaved(res.external_url);
      setOk(res.external_url ? t("external.saved") : t("external.cleared"));
      await qc.invalidateQueries({ queryKey: ["server", "settings"] });
    } catch (err) {
      setError(errMsg(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Section
      title={t("external.title")}
      description={t("external.description")}
    >
      {initial.locked ? (
        <div className="space-y-2 text-sm">
          <p className="break-all font-mono">{initial.external_url}</p>
          <Callout>{t("external.locked")}</Callout>
        </div>
      ) : (
        <form onSubmit={submit} className="space-y-4">
          {unset && (
            <Callout>{t("external.unsetWarn")}</Callout>
          )}
          <Field
            label={t("external.title")}
            htmlFor="external-url"
            hint={shapeOk ? t("external.hint") : undefined}
          >
            <input
              id="external-url"
              type="url"
              inputMode="url"
              autoCapitalize="none"
              spellCheck={false}
              value={value}
              onChange={(e) => setValue(e.target.value)}
              className={`${inputClass} font-mono`}
            />
            {!shapeOk && <p className="mt-1 text-xs text-danger">{t("external.shapeError")}</p>}
          </Field>
          <ErrorText>{error}</ErrorText>
          <SuccessText>{ok}</SuccessText>
          <button type="submit" disabled={busy || !shapeOk} className={btn.primary}>
            {busy ? t("external.saving") : t("external.save")}
          </button>
        </form>
      )}
    </Section>
  );
}
