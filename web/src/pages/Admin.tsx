import { useCallback, useEffect, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { Link, useSearch } from "@tanstack/react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  api,
  ApiError,
  type AccountLookup,
  type AdminMember,
  type CreatedInvite,
  type Handover,
  type Invite,
  type PendingDevice,
  type WorkspaceSettings,
  codeProviders,
} from "../api";
import { DeviceTokens, NeedPasswordLogin, Notice } from "../components";
import i18n from "../i18n";
import { safeTrans } from "../i18n/safeTrans";
import {
  absoluteTime,
  inviteKindLabel,
  memberNamePattern,
  relativeTime,
  roleLabel,
  signupKindLabel,
  toolsText,
  workspaceLabel,
  workspaceListText,
} from "../format";
import {
  btn,
  Callout,
  ConfirmButton,
  CopyField,
  Dialog,
  ErrorText,
  errMsg,
  Field,
  inputClass,
  Section,
  SuccessText,
} from "../ui";
import { adminNeedsPasswordLogin } from "../rules";
import {
  assignAll,
  buildHandoverPlan,
  handoverCandidates,
  handoverFromConflict,
  issueKey,
  keepAssignments,
  serviceKey,
  type Assignments,
} from "../handover";
import { canAdminWorkspace } from "../workspace";
import { useWs } from "./WorkspaceShell";
import { EventLog } from "./EventLog";
import { CodeBind } from "./ChatCode";

export const adminTabs = [
  { id: "pending" },
  { id: "members" },
  { id: "devices" },
  { id: "invites" },
  { id: "workspace" },
  { id: "events" },
] as const;

export type AdminTab = (typeof adminTabs)[number]["id"];

export function AdminPage() {
  const { t } = useTranslation("admin");
  const ws = useWs();
  const { tab = "pending" } = useSearch({ from: "/layout/w/$ws/admin" });
  const me = useQuery({ queryKey: ["me"], queryFn: api.me });

  if (me.isPending) return <Notice>{t("loading")}</Notice>;
  if (me.isError) return <Notice tone="danger">{t("loadFailed", { message: me.error.message })}</Notice>;
  if (!canAdminWorkspace(me.data, ws)) {
    return <Notice tone="danger">{t("noPermission")}</Notice>;
  }
  if (adminNeedsPasswordLogin(me.data)) {
    return (
      <NeedPasswordLogin
        redirectTo={`/w/${encodeURIComponent(ws)}/admin`}
        message={t("needPasswordLogin")}
      />
    );
  }
  const member = me.data.workspaces.some((w) => w.name === ws);
  const ref = me.data.workspaces.find((w) => w.name === ws);
  const wsTitle = ref ? workspaceLabel(ref) : ws;
  const self = me.data.account.name;
  const tabLabels = {
    pending: t("tabs.pending"),
    members: t("tabs.members"),
    devices: t("tabs.devices"),
    invites: t("tabs.invites"),
    workspace: t("tabs.workspace"),
    events: t("tabs.events"),
  };

  return (
    <div className="mx-auto max-w-4xl">
      <h1 className="mb-3 text-lg font-semibold">{t("title", { name: wsTitle })}</h1>
      {!member && <JoinSelf ws={ws} self={self} />}
      <nav aria-label={t("navLabel")} className="mb-5 flex flex-wrap gap-1 border-b border-line">
        {adminTabs.map((item) => (
          <Link
            key={item.id}
            to="/w/$ws/admin"
            params={{ ws }}
            search={{ tab: item.id }}
            aria-current={tab === item.id ? "page" : undefined}
            className={`-mb-px border-b-[3px] px-3 py-2 text-sm ${
              tab === item.id ? "border-accent-fill font-semibold text-ink" : "border-transparent text-muted hover:text-ink"
            }`}
          >
            {tabLabels[item.id]}
          </Link>
        ))}
      </nav>
      {tab === "pending" && <PendingTab ws={ws} />}
      {tab === "members" && <MembersTab ws={ws} self={self} />}
      {tab === "devices" && <DevicesTab ws={ws} />}
      {tab === "invites" && <InvitesTab ws={ws} />}
      {tab === "workspace" && <WorkspaceTab ws={ws} serverAdmin={me.data.account.server_admin} />}
      {tab === "events" && <EventLog scope={{ ws }} />}
    </div>
  );
}

// 服务器管理员管理自己不是成员的工作空间：看板与问题单要先把自己加进来（留痕）才能看
function JoinSelf({ ws, self }: { ws: string; self: string }) {
  const { t } = useTranslation("admin");
  const qc = useQueryClient();
  return (
    <div className="mb-4">
      <Callout>
        <p>{t("joinSelf.notice")}</p>
        <div className="mt-2">
          <ConfirmButton
            label={t("joinSelf.button")}
            title={t("joinSelf.title")}
            message={t("joinSelf.message", { ws })}
            confirmLabel={t("joinSelf.confirm")}
            danger={false}
            action={async () => {
              await api.addMember(ws, self, "admin");
              await qc.invalidateQueries({ queryKey: ["me"] });
              await qc.invalidateQueries({ queryKey: ["admin", ws] });
            }}
          />
        </div>
      </Callout>
    </div>
  );
}

const listClass = "divide-y divide-line rounded-lg border border-line bg-surface";
const rowClass = "flex flex-wrap items-center gap-x-4 gap-y-2 px-3 py-3 text-sm";

function Loading<T>({
  q,
  empty,
  isEmpty,
  children,
}: {
  q: { isPending: boolean; isError: boolean; error: Error | null; data?: T };
  empty: string;
  isEmpty: (d: T) => boolean;
  children: (d: T) => React.ReactNode;
}) {
  const { t } = useTranslation("admin");
  if (q.isPending) return <Notice>{t("loading")}</Notice>;
  if (q.isError) return <Notice tone="danger">{t("loadFailed", { message: q.error?.message })}</Notice>;
  const d = q.data as T;
  if (isEmpty(d)) return <Notice>{empty}</Notice>;
  return <>{children(d)}</>;
}

// 批准 / 拒绝：批准直接执行，拒绝需二次确认
function DecideButtons({ name, onDecide }: { name: string; onDecide: (approve: boolean) => Promise<void> }) {
  const { t } = useTranslation("admin");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const approve = async () => {
    setBusy(true);
    setError("");
    try {
      await onDecide(true);
    } catch (err) {
      setError(errMsg(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="flex flex-wrap items-center gap-2">
      <button type="button" onClick={approve} disabled={busy} className={btn.primary}>
        {busy ? t("decide.approving") : t("decide.approve")}
      </button>
      <ConfirmButton
        label={t("decide.reject")}
        title={t("decide.rejectTitle")}
        message={t("decide.rejectMessage", { name })}
        confirmLabel={t("decide.rejectConfirm")}
        disabled={busy}
        action={() => onDecide(false)}
      />
      {error && <p className="w-full text-xs text-danger">{error}</p>}
    </div>
  );
}

// 一条待批设备申请：申请人、设备、工具，以及批准后这台电脑可用于哪些工作空间
export function PendingDeviceRow({
  v,
  onDecide,
}: {
  v: PendingDevice;
  onDecide: (approve: boolean) => Promise<void>;
}) {
  const { t } = useTranslation("admin");
  return (
    <li className={rowClass}>
      <div className="min-w-0 flex-1">
        <p className="break-words">
          <span className="font-mono font-medium">{v.account}</span>
          {v.display_name && v.display_name !== v.account && <span className="ml-2">{v.display_name}</span>}
          <span className="ml-2">{v.device_name || t("pending.devices.unnamed")}</span>
          <span className="ml-1 text-xs text-muted">{t("pending.devices.selfReported")}</span>
        </p>
        <p className="text-xs text-muted" title={absoluteTime(v.created_at)}>
          {t("pending.devices.requested", { tools: toolsText(v.tools), time: relativeTime(v.created_at) })}
        </p>
        <p className="text-xs">
          {t("pending.devices.usedFor", {
            workspaces: workspaceListText(v.workspaces, t("pending.devices.noWorkspaces")),
          })}
        </p>
      </div>
      <DecideButtons
        name={t("pending.devices.rejectName", {
          account: v.account,
          device: v.device_name || t("pending.devices.deviceFallback"),
        })}
        onDecide={onDecide}
      />
    </li>
  );
}

function PendingTab({ ws }: { ws: string }) {
  const { t } = useTranslation("admin");
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["admin", ws, "pending"], queryFn: () => api.pending(ws), refetchInterval: 10000 });
  const refresh = () => qc.invalidateQueries({ queryKey: ["admin", ws] });

  return (
    <div className="space-y-6">
      <section>
        <h2 className="mb-2 font-semibold">{t("pending.signups.title")}</h2>
        <p className="mb-2 text-sm text-muted">{t("pending.signups.description")}</p>
        <Loading q={q} empty={t("pending.signups.empty")} isEmpty={(d) => d.signups.length === 0}>
          {(d) => (
            <ul className={listClass}>
              {d.signups.map((s) => (
                <li key={s.id} className={rowClass}>
                  <div className="min-w-0 flex-1">
                    <p className="flex flex-wrap items-center gap-x-2">
                      <span className="font-mono font-medium">{s.name}</span>
                      {s.display_name && <span>{s.display_name}</span>}
                      <span
                        className={`rounded px-1.5 py-px text-xs ${
                          s.kind === "existing" ? "bg-teal-soft text-teal" : "bg-sunken text-muted"
                        }`}
                      >
                        {signupKindLabel(s.kind)}
                      </span>
                    </p>
                    <p className="text-xs text-muted" title={absoluteTime(s.created_at)}>
                      {t("pending.signups.requested", { time: relativeTime(s.created_at) })}
                    </p>
                  </div>
                  <DecideButtons
                    name={s.display_name || s.name}
                    onDecide={async (approve) => {
                      await api.decideSignup(ws, s.id, approve);
                      await refresh();
                    }}
                  />
                </li>
              ))}
            </ul>
          )}
        </Loading>
      </section>
      <section>
        <h2 className="mb-2 font-semibold">{t("pending.devices.title")}</h2>
        <p className="mb-2 text-sm text-muted">{t("pending.devices.description")}</p>
        <Loading q={q} empty={t("pending.devices.empty")} isEmpty={(d) => d.devices.length === 0}>
          {(d) => (
            <ul className={listClass}>
              {d.devices.map((v) => (
                <PendingDeviceRow
                  key={v.id}
                  v={v}
                  onDecide={async (approve) => {
                    await api.decideDevice(ws, v.id, approve);
                    await refresh();
                  }}
                />
              ))}
            </ul>
          )}
        </Loading>
      </section>
    </div>
  );
}

// 在调用时取当前语言；用到它的组件都订阅了语言变化
const memberLabel = (m: { account: string; display_name?: string }) =>
  m.display_name && m.display_name !== m.account
    ? i18n.t("admin:member.label", { display: m.display_name, account: m.account })
    : m.account;

function MembersTab({ ws, self }: { ws: string; self: string }) {
  const { t } = useTranslation("admin");
  const qc = useQueryClient();
  const [view, setView] = useState<"active" | "removed">("active");
  const [adding, setAdding] = useState(false);
  const [handover, setHandover] = useState<{ member: AdminMember; remove: boolean } | null>(null);
  const [notice, setNotice] = useState("");
  const active = useQuery({ queryKey: ["admin", ws, "members"], queryFn: () => api.members(ws) });
  const removed = useQuery({
    queryKey: ["admin", ws, "members", "removed"],
    queryFn: () => api.members(ws, "removed"),
    enabled: view === "removed",
  });
  const refresh = async () => {
    await qc.invalidateQueries({ queryKey: ["admin", ws] });
    await qc.invalidateQueries({ queryKey: ["me"] });
  };

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <button type="button" onClick={() => setAdding(true)} className={btn.primary}>
          {t("members.add")}
        </button>
        <button
          type="button"
          onClick={() => setView(view === "active" ? "removed" : "active")}
          className={`${btn.secondary} ml-auto`}
        >
          {view === "active" ? t("members.viewRemoved") : t("members.viewActive")}
        </button>
      </div>
      <SuccessText>{notice}</SuccessText>

      {view === "active" ? (
        <Loading q={active} empty={t("members.empty")} isEmpty={(d) => d.items.length === 0}>
          {(d) => (
            <ul className={listClass}>
              {d.items.map((m) => (
                <MemberRow
                  key={m.member_id}
                  ws={ws}
                  m={m}
                  isSelf={m.account === self}
                  onChanged={refresh}
                  onHandover={(remove) => setHandover({ member: m, remove })}
                />
              ))}
            </ul>
          )}
        </Loading>
      ) : (
        <Loading q={removed} empty={t("members.emptyRemoved")} isEmpty={(d) => d.items.length === 0}>
          {(d) => (
            <ul className={listClass}>
              {d.items.map((m) => (
                <li key={m.member_id} className={rowClass}>
                  <div className="min-w-0 flex-1">
                    <p className="flex flex-wrap items-center gap-x-2">
                      <span className="font-mono font-medium">{m.account}</span>
                      {m.display_name && <span>{m.display_name}</span>}
                      <span className="rounded bg-sunken px-1.5 py-px text-xs text-muted">{t("members.removedTag")}</span>
                      {m.disabled && (
                        <span className="rounded border-[1.5px] border-danger bg-danger font-bold text-on-status px-1.5 py-px text-xs">{t("members.disabledTag")}</span>
                      )}
                    </p>
                  </div>
                  {!m.disabled && (
                    <ConfirmButton
                      label={t("members.rejoin")}
                      title={t("members.rejoinTitle")}
                      message={t("members.rejoinMessage", { name: memberLabel(m) })}
                      confirmLabel={t("members.rejoinConfirm")}
                      danger={false}
                      action={async () => {
                        await api.addMember(ws, m.account, "member");
                        await refresh();
                      }}
                    />
                  )}
                </li>
              ))}
            </ul>
          )}
        </Loading>
      )}

      <AddMemberDialog
        ws={ws}
        open={adding}
        onClose={() => setAdding(false)}
        onAdded={async (name) => {
          setAdding(false);
          setNotice(t("members.added", { name }));
          await refresh();
        }}
      />
      {handover && (
        <HandoverDialog
          ws={ws}
          member={handover.member}
          remove={handover.remove}
          candidates={handoverCandidates(active.data?.items ?? [], handover.member.member_id)}
          onClose={() => setHandover(null)}
          onDone={async () => {
            const m = handover.member;
            setHandover(null);
            setNotice(
              handover.remove
                ? t("members.removed", { name: memberLabel(m) })
                : t("members.reassigned", { name: memberLabel(m) }),
            );
            await refresh();
          }}
        />
      )}
    </div>
  );
}

function MemberRow({
  ws,
  m,
  isSelf,
  onChanged,
  onHandover,
}: {
  ws: string;
  m: AdminMember;
  isSelf: boolean;
  onChanged: () => Promise<void>;
  onHandover: (remove: boolean) => void;
}) {
  const { t } = useTranslation("admin");
  const nextRole = m.role === "admin" ? "member" : "admin";
  const name = m.display_name || m.account;
  return (
    <li className={rowClass}>
      <div className="min-w-0 flex-1">
        <p className="flex flex-wrap items-center gap-x-2">
          <span className="font-mono font-medium">{m.account}</span>
          {m.display_name && <span>{m.display_name}</span>}
          {isSelf && <span className="text-xs text-muted">{t("members.you")}</span>}
          <span
            className={`rounded px-1.5 py-px text-xs ${
              m.role === "admin" ? "border-[1.5px] border-ink bg-ink font-bold text-surface" : "bg-sunken text-muted"
            }`}
          >
            {roleLabel(m.role)}
          </span>
          {m.disabled && (
            <span className="rounded border-[1.5px] border-danger bg-danger font-bold text-on-status px-1.5 py-px text-xs" title={t("members.disabledTitle")}>
              {t("members.disabledShort")}
            </span>
          )}
        </p>
        <p className="text-xs text-muted">
          {m.telegram_linked
            ? m.telegram_username
              ? t("members.telegramLinkedAs", { username: m.telegram_username })
              : t("members.telegramLinked")
            : t("members.telegramNotLinked")}
          {" · "}
          {t("members.devices", { count: m.devices })}
          {" · "}
          {m.has_password ? t("members.passwordSet") : t("members.passwordNotSet")}
          {" · "}
          <span className={m.pending_issues > 0 ? "text-ink" : undefined}>
            {t("members.pendingIssues", { count: m.pending_issues })}
          </span>
          {" · "}
          <span className={m.owned_services > 0 ? "text-ink" : undefined}>
            {t("members.ownedServices", { count: m.owned_services })}
          </span>
        </p>
        {m.disabled && (m.pending_issues > 0 || m.owned_services > 0) && (
          <p className="mt-1 text-xs text-wait">{t("members.disabledWarning")}</p>
        )}
      </div>
      <div className="flex flex-wrap gap-2">
        <ConfirmButton
          label={nextRole === "admin" ? t("members.makeAdmin") : t("members.makeMember")}
          danger={nextRole === "member"}
          title={nextRole === "admin" ? t("members.makeAdminTitle") : t("members.makeMemberTitle")}
          message={
            nextRole === "admin"
              ? t("members.makeAdminMessage", { name })
              : isSelf
                ? t("members.demoteSelfMessage")
                : t("members.demoteMessage", { name })
          }
          confirmLabel={t("members.confirm")}
          action={async () => {
            await api.setMemberRole(ws, m.member_id, nextRole);
            await onChanged();
          }}
        />
        {m.disabled && (
          <button type="button" onClick={() => onHandover(false)} className={btn.secondary}>
            {t("members.reassign")}
          </button>
        )}
        <button type="button" onClick={() => onHandover(true)} className={btn.danger}>
          {t("members.remove")}
        </button>
      </div>
    </li>
  );
}

// 添加已有账号：按登录名精确查找 → 确认显示名与状态 → 选角色 → 加入
function AddMemberDialog({
  ws,
  open,
  onClose,
  onAdded,
}: {
  ws: string;
  open: boolean;
  onClose: () => void;
  onAdded: (name: string) => Promise<void>;
}) {
  const { t } = useTranslation("admin");
  const [name, setName] = useState("");
  const [found, setFound] = useState<AccountLookup | null>(null);
  const [role, setRole] = useState<"member" | "admin">("member");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!open) {
      setName("");
      setFound(null);
      setRole("member");
      setError("");
    }
  }, [open]);

  const trimmed = name.trim();
  const lookup = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!memberNamePattern.test(trimmed)) {
      setError(t("addMember.badUsername"));
      return;
    }
    setBusy(true);
    setError("");
    setFound(null);
    try {
      setFound(await api.lookupAccount(ws, trimmed));
    } catch (err) {
      setError(
        err instanceof ApiError && err.status === 404
          ? t("addMember.notFound", { name: trimmed })
          : errMsg(err),
      );
    } finally {
      setBusy(false);
    }
  };

  const add = async () => {
    if (!found) return;
    setBusy(true);
    setError("");
    try {
      await api.addMember(ws, found.name, role);
      await onAdded(memberLabel({ account: found.name, display_name: found.display_name }));
    } catch (err) {
      setError(errMsg(err));
    } finally {
      setBusy(false);
    }
  };

  const canAdd = !!found && found.member_status !== "active" && !found.disabled;

  return (
    <Dialog open={open} onClose={() => !busy && onClose()} title={t("addMember.title")} dismissible={!busy}>
      <form onSubmit={lookup} className="space-y-3">
        <Field label={t("addMember.username")} htmlFor="add-account" hint={t("addMember.usernameHint")}>
          <div className="flex gap-2">
            <input
              id="add-account"
              autoCapitalize="none"
              spellCheck={false}
              maxLength={32}
              value={name}
              onChange={(e) => {
                setName(e.target.value);
                setFound(null);
              }}
              className={`${inputClass} min-w-0 flex-1 font-mono`}
            />
            <button type="submit" disabled={busy || !trimmed} className={`${btn.secondary} shrink-0`}>
              {t("addMember.find")}
            </button>
          </div>
        </Field>
      </form>
      {found && (
        <div className="mt-3 space-y-3 rounded border border-line-strong bg-bg px-3 py-2 text-sm">
          <p>
            <span className="font-mono font-medium">{found.name}</span>
            {found.display_name && <span className="ml-2">{found.display_name}</span>}
          </p>
          {found.disabled ? (
            <p className="text-danger">{t("addMember.disabled")}</p>
          ) : found.member_status === "active" ? (
            <p className="text-muted">{t("addMember.alreadyMember")}</p>
          ) : (
            <>
              {found.member_status === "removed" && (
                <p className="text-muted">{t("addMember.wasRemoved")}</p>
              )}
              <label className="flex items-center gap-2">
                <span>{t("addMember.role")}</span>
                <select
                  value={role}
                  onChange={(e) => setRole(e.target.value as "member" | "admin")}
                  className="rounded border border-line-strong bg-surface px-2 py-1 text-sm"
                >
                  <option value="member">{t("addMember.roleMember")}</option>
                  <option value="admin">{t("addMember.roleAdmin")}</option>
                </select>
              </label>
            </>
          )}
        </div>
      )}
      <ErrorText>{error}</ErrorText>
      <div className="mt-5 flex justify-end gap-2">
        <button type="button" onClick={onClose} disabled={busy} className={btn.secondary}>
          {t("addMember.cancel")}
        </button>
        <button type="button" onClick={add} disabled={busy || !canAdd} className={btn.primary}>
          {busy && found ? t("addMember.joining") : t("addMember.join")}
        </button>
      </div>
    </Dialog>
  );
}

// 移出（remove）或只改派（给已停用的人）：他手上未结束的单子与登记的服务，每一项选一个接手人
export function HandoverDialog({
  ws,
  member,
  remove,
  candidates,
  onClose,
  onDone,
}: {
  ws: string;
  member: AdminMember;
  remove: boolean;
  candidates: AdminMember[];
  onClose: () => void;
  onDone: () => Promise<void>;
}) {
  const { t } = useTranslation("admin");
  const [h, setH] = useState<Handover | null>(null);
  const [loadError, setLoadError] = useState("");
  const [assign, setAssign] = useState<Assignments>({});
  const [allTo, setAllTo] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const name = memberLabel(member);

  useEffect(() => {
    let stopped = false;
    api.handover(ws, member.member_id).then(
      (res) => {
        if (stopped) return;
        setH(res);
        setAssign(keepAssignments(res, {}));
      },
      (err) => !stopped && setLoadError(errMsg(err)),
    );
    return () => {
      stopped = true;
    };
  }, [ws, member.member_id]);

  const count = h ? h.issues.length + h.services.length : 0;

  const submit = async () => {
    if (!h) return;
    const r = buildHandoverPlan(h, assign);
    if ("missing" in r) {
      setError(t("handover.missing", { count: r.missing }));
      return;
    }
    setBusy(true);
    setError("");
    try {
      await api.applyHandover(ws, member.member_id, r.plan, remove);
      await onDone();
    } catch (err) {
      const latest = err instanceof ApiError && err.status === 409 ? handoverFromConflict(err.body) : null;
      if (latest) {
        setH(latest);
        setAssign(keepAssignments(latest, assign));
        setAllTo("");
        setError(t("handover.refreshed", { message: errMsg(err).replace(/[\u3002.]$/, "") }));
      } else {
        setError(errMsg(err));
      }
    } finally {
      setBusy(false);
    }
  };

  const picker = (key: string, label: string) => (
    <select
      aria-label={label}
      value={assign[key] ?? ""}
      onChange={(e) => setAssign({ ...assign, [key]: e.target.value })}
      className="w-full rounded border border-line-strong bg-surface px-2 py-1 text-sm sm:w-48"
    >
      <option value="">{t("handover.choose")}</option>
      {candidates.map((c) => (
        <option key={c.member_id} value={String(c.member_id)}>
          {memberLabel(c)}
        </option>
      ))}
    </select>
  );

  return (
    <Dialog
      open
      wide
      onClose={() => !busy && onClose()}
      title={remove ? t("handover.removeTitle", { name }) : t("handover.reassignTitle", { name })}
      dismissible={!busy}
    >
      <p className="text-sm text-muted">
        {remove ? t("handover.removeDescription") : t("handover.reassignDescription")}
      </p>
      {loadError ? (
        <ErrorText>{t("handover.loadFailed", { message: loadError })}</ErrorText>
      ) : !h ? (
        <p className="mt-3 text-sm text-muted">{t("handover.loading")}</p>
      ) : count === 0 ? (
        <p className="mt-3 text-sm">{remove ? t("handover.nothingToRemove") : t("handover.nothingToReassign")}</p>
      ) : candidates.length === 0 ? (
        <Callout>{remove ? t("handover.noCandidatesRemove") : t("handover.noCandidatesReassign")}</Callout>
      ) : (
        <div className="mt-3 space-y-3">
          <label className="flex flex-wrap items-center gap-2 text-sm">
            <span className="font-medium">{t("handover.allTo")}</span>
            <select
              value={allTo}
              onChange={(e) => {
                setAllTo(e.target.value);
                if (e.target.value) setAssign(assignAll(h, e.target.value));
              }}
              className="rounded border border-line-strong bg-surface px-2 py-1 text-sm"
            >
              <option value="">{t("handover.oneByOne")}</option>
              {candidates.map((c) => (
                <option key={c.member_id} value={String(c.member_id)}>
                  {memberLabel(c)}
                </option>
              ))}
            </select>
          </label>
          <ul className="max-h-[50vh] divide-y divide-line overflow-y-auto rounded border border-line">
            {h.issues.map((it) => (
              <li key={issueKey(it)} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2 text-sm">
                <div className="min-w-0 flex-1">
                  <p className="break-words">
                    <span className="text-muted">#{it.id}</span> {it.title}
                  </p>
                  <p className="text-xs text-muted">
                    {it.status_label} · {it.as === "owner" ? t("handover.ownerOf") : t("handover.reporterOf")}
                  </p>
                </div>
                {picker(issueKey(it), t("handover.issueNewOwner", { id: it.id }))}
              </li>
            ))}
            {h.services.map((sv) => (
              <li key={serviceKey(sv)} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2 text-sm">
                <div className="min-w-0 flex-1">
                  <p className="break-words">
                    {t("handover.service")} <span className="font-mono">{sv.name}</span>
                  </p>
                  <p className="text-xs text-muted">{t("handover.serviceOwnerNote")}</p>
                </div>
                {picker(serviceKey(sv), t("handover.serviceNewOwner", { name: sv.name }))}
              </li>
            ))}
          </ul>
        </div>
      )}
      <ErrorText>{error}</ErrorText>
      <div className="mt-5 flex justify-end gap-2">
        <button type="button" onClick={onClose} disabled={busy} className={btn.secondary}>
          {t("handover.cancel")}
        </button>
        <button
          type="button"
          onClick={submit}
          disabled={busy || !h || (count > 0 && candidates.length === 0) || (!remove && count === 0)}
          className={remove ? btn.dangerSolid : btn.primary}
        >
          {busy
            ? t("handover.working")
            : remove
              ? count > 0
                ? t("handover.transferAndRemove")
                : t("handover.confirmRemove")
              : t("handover.confirmReassign")}
        </button>
      </div>
    </Dialog>
  );
}

function DevicesTab({ ws }: { ws: string }) {
  const { t } = useTranslation("admin");
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["admin", ws, "devices"], queryFn: () => api.devices(ws) });

  return (
    <div className="space-y-2">
      <p className="text-sm text-muted">{t("devices.description")}</p>
      <Loading q={q} empty={t("devices.empty")} isEmpty={(d) => d.items.length === 0}>
        {(d) => (
          <ul className={listClass}>
            {d.items.map((v) => (
              <li key={v.id} className={rowClass}>
                <div className="min-w-0 flex-1">
                  <p className="break-words">
                    <span className="font-mono font-medium">{v.account}</span>
                    <span className="ml-2">{v.name || t("devices.unnamed")}</span>
                  </p>
                  <DeviceTokens tokens={v.tokens} revoked={!!v.revoked_at} />
                  <p className="text-xs text-muted">
                    {t("devices.added", { time: absoluteTime(v.created_at) })}
                    {v.last_used_at && ` · ${t("devices.lastUsed", { time: absoluteTime(v.last_used_at) })}`}
                    {v.revoked_at && ` · ${t("devices.revokedAt", { time: absoluteTime(v.revoked_at) })}`}
                  </p>
                </div>
                {v.revoked_at ? (
                  <span className="rounded bg-sunken px-1.5 py-px text-xs text-muted">{t("devices.revokedTag")}</span>
                ) : (
                  <>
                    <span className="rounded bg-teal-soft px-1.5 py-px text-xs text-teal">{t("devices.active")}</span>
                    <ConfirmButton
                      label={t("devices.revoke")}
                      title={t("devices.revokeTitle")}
                      message={t("devices.revokeMessage", { account: v.account, name: v.name || t("devices.unnamed") })}
                      confirmLabel={t("devices.revokeConfirm")}
                      action={async () => {
                        await api.revokeDevice(ws, v.id);
                        await qc.invalidateQueries({ queryKey: ["admin", ws] });
                      }}
                    />
                  </>
                )}
              </li>
            ))}
          </ul>
        )}
      </Loading>
    </div>
  );
}

type InviteState = "revoked" | "expired" | "usedUp" | "active";

function inviteState(inv: Invite): InviteState {
  if (inv.revoked_at) return "revoked";
  if (new Date(inv.expires_at).getTime() <= Date.now()) return "expired";
  if (inv.max_uses && inv.uses >= inv.max_uses) return "usedUp";
  return "active";
}

function InvitesTab({ ws }: { ws: string }) {
  const { t } = useTranslation("admin");
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["admin", ws, "invites"], queryFn: () => api.invites(ws) });
  const [created, setCreated] = useState<CreatedInvite | null>(null);
  const stateLabels: Record<InviteState, string> = {
    revoked: t("invites.state.revoked"),
    expired: t("invites.state.expired"),
    usedUp: t("invites.state.usedUp"),
    active: t("invites.state.active"),
  };

  return (
    <div className="space-y-6">
      <NewInviteForm
        ws={ws}
        onCreated={async (inv) => {
          setCreated(inv);
          await qc.invalidateQueries({ queryKey: ["admin", ws, "invites"] });
        }}
      />
      <section>
        <h2 className="mb-2 font-semibold">{t("invites.title")}</h2>
        <Loading q={q} empty={t("invites.empty")} isEmpty={(d) => d.items.length === 0}>
          {(d) => (
            <ul className={listClass}>
              {d.items.map((inv) => {
                const st = inviteState(inv);
                const active = st === "active";
                return (
                  <li key={inv.id} className={rowClass}>
                    <div className="min-w-0 flex-1">
                      <p className="flex flex-wrap items-center gap-x-2">
                        <span className="font-medium">{inviteKindLabel(inv.kind)}</span>
                        <span className="text-muted">{t("invites.joinsAs", { role: roleLabel(inv.role) })}</span>
                        <span
                          className={`rounded px-1.5 py-px text-xs ${
                            active ? "border-[1.5px] border-teal bg-teal font-bold text-on-status" : "bg-sunken text-muted"
                          }`}
                        >
                          {stateLabels[st]}
                        </span>
                      </p>
                      <p className="text-xs text-muted">
                        {t("invites.usage", {
                          uses: inv.uses,
                          max: inv.max_uses ? inv.max_uses : t("invites.unlimited"),
                          expires: absoluteTime(inv.expires_at),
                          created: absoluteTime(inv.created_at),
                        })}
                      </p>
                    </div>
                    {active && (
                      <ConfirmButton
                        label={t("invites.revoke")}
                        title={t("invites.revokeTitle")}
                        message={t("invites.revokeMessage")}
                        confirmLabel={t("invites.revokeConfirm")}
                        action={async () => {
                          await api.revokeInvite(ws, inv.id);
                          await qc.invalidateQueries({ queryKey: ["admin", ws, "invites"] });
                        }}
                      />
                    )}
                  </li>
                );
              })}
            </ul>
          )}
        </Loading>
      </section>
      <InviteLinkDialog url={created?.url ?? ""} onDone={() => setCreated(null)} />
    </div>
  );
}

// 新生成的邀请链接只显示这一次：不允许按 Esc 关掉，必须点“我已复制”。
export function InviteLinkDialog({ url, onDone }: { url: string; onDone: () => void }) {
  const { t } = useTranslation("admin");
  return (
    <Dialog open={!!url} onClose={onDone} title={t("invites.linkTitle")} dismissible={false}>
      {url && (
        <div className="space-y-3">
          <CopyField label={t("invites.linkLabel")} value={url} />
          <Callout>{t("invites.linkWarning")}</Callout>
          <div className="flex justify-end">
            <button type="button" onClick={onDone} className={btn.primary}>
              {t("invites.copied")}
            </button>
          </div>
        </div>
      )}
    </Dialog>
  );
}

function NewInviteForm({ ws, onCreated }: { ws: string; onCreated: (inv: CreatedInvite) => Promise<void> }) {
  const { t } = useTranslation("admin");
  const [kind, setKind] = useState<"personal" | "team">("personal");
  const [asAdmin, setAsAdmin] = useState(false);
  const [maxUses, setMaxUses] = useState("10");
  const [ttl, setTtl] = useState("168");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const uses = Number(maxUses);
  const hours = Number(ttl);
  const usesOk = kind === "personal" || (Number.isInteger(uses) && uses >= 1 && uses <= 100);
  const ttlOk = Number.isInteger(hours) && hours >= 1 && hours <= 24 * 30;

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!usesOk || !ttlOk) return;
    setBusy(true);
    setError("");
    try {
      const inv = await api.createInvite(
        ws,
        kind === "personal"
          ? { kind, role: asAdmin ? "admin" : "member", max_uses: 1, ttl_hours: hours }
          : { kind, role: "member", max_uses: uses, ttl_hours: hours },
      );
      await onCreated(inv);
    } catch (err) {
      setError(errMsg(err));
    } finally {
      setBusy(false);
    }
  };

  const radio = (value: "personal" | "team", title: string, desc: string) => (
    <label
      className={`flex flex-1 cursor-pointer gap-2 rounded border px-3 py-2 text-sm ${
        kind === value ? "border-accent bg-accent-soft ring-1 ring-accent ring-inset" : "border-line-strong bg-surface"
      }`}
    >
      <input
        type="radio"
        name="invite-kind"
        value={value}
        checked={kind === value}
        onChange={() => setKind(value)}
        className="mt-1 accent-[var(--accent)]"
      />
      <span>
        <span className="block font-medium">{title}</span>
        <span className="block text-xs text-muted">{desc}</span>
      </span>
    </label>
  );

  return (
    <Section
      title={t("newInvite.title")}
      description={t("newInvite.description")}
    >
      <form onSubmit={submit} className="space-y-4">
        <fieldset>
          <legend className="mb-1 text-sm">{t("newInvite.kind")}</legend>
          <div className="flex flex-col gap-2 sm:flex-row">
            {radio("personal", t("newInvite.personal"), t("newInvite.personalDesc"))}
            {radio("team", t("newInvite.team"), t("newInvite.teamDesc"))}
          </div>
        </fieldset>
        <div className="grid gap-4 sm:grid-cols-2">
          {kind === "personal" ? (
            <label className="flex items-center gap-2 self-end text-sm">
              <input
                type="checkbox"
                checked={asAdmin}
                onChange={(e) => setAsAdmin(e.target.checked)}
                className="accent-[var(--accent)]"
              />
              {t("newInvite.asAdmin")}
            </label>
          ) : (
            <Field label={t("newInvite.maxUses")} htmlFor="max-uses" hint={usesOk ? t("newInvite.maxUsesHint") : undefined}>
              <input
                id="max-uses"
                type="number"
                inputMode="numeric"
                min={1}
                max={100}
                value={maxUses}
                onChange={(e) => setMaxUses(e.target.value)}
                className={inputClass}
              />
              {!usesOk && <p className="mt-1 text-xs text-danger">{t("newInvite.maxUsesError")}</p>}
            </Field>
          )}
          <Field label={t("newInvite.ttl")} htmlFor="ttl" hint={ttlOk ? t("newInvite.ttlHint") : undefined}>
            <input
              id="ttl"
              type="number"
              inputMode="numeric"
              min={1}
              max={24 * 30}
              value={ttl}
              onChange={(e) => setTtl(e.target.value)}
              className={inputClass}
            />
            {!ttlOk && <p className="mt-1 text-xs text-danger">{t("newInvite.ttlError")}</p>}
          </Field>
        </div>
        <ErrorText>{error}</ErrorText>
        <button type="submit" disabled={busy || !usesOk || !ttlOk} className={btn.primary}>
          {busy ? t("newInvite.submitting") : t("newInvite.submit")}
        </button>
      </form>
    </Section>
  );
}

function WorkspaceTab({ ws, serverAdmin }: { ws: string; serverAdmin: boolean }) {
  const { t } = useTranslation("admin");
  const q = useQuery({ queryKey: ["admin", ws, "workspace"], queryFn: () => api.workspaceSettings(ws) });
  if (q.isPending) return <Notice>{t("loading")}</Notice>;
  if (q.isError) return <Notice tone="danger">{t("loadFailed", { message: q.error.message })}</Notice>;
  return (
    <div className="space-y-4">
      {!q.data.telegram_chat && !q.data.chat && (
        <Callout>
          {q.data.channels?.some((c) => c.provider === "dingtalk")
            ? t("workspace.nextStepDingtalk")
            : q.data.channels?.some((c) => c.provider === "slack")
              ? t("workspace.nextStepSlack")
              : t("workspace.nextStepTelegram")}
        </Callout>
      )}
      {q.data.archived && <Callout>{t("workspace.readOnly")}</Callout>}
      <WorkspaceForm key={ws} ws={ws} initial={q.data} serverAdmin={serverAdmin} />
      <ArchiveSection key={`archive-${ws}`} ws={ws} initial={q.data} />
    </div>
  );
}

export function WorkspaceForm({
  ws,
  initial,
  serverAdmin,
}: {
  ws: string;
  initial: WorkspaceSettings;
  serverAdmin: boolean;
}) {
  const { t } = useTranslation("admin");
  const qc = useQueryClient();
  const [display, setDisplay] = useState(initial.display_name === initial.path ? "" : initial.display_name ?? "");
  const [chat, setChat] = useState(initial.telegram_chat == null ? "" : String(initial.telegram_chat));
  const [showTitle, setShowTitle] = useState(initial.show_title);
  const [showBrief, setShowBrief] = useState(initial.show_brief);
  const [remindOpen, setRemindOpen] = useState(String(initial.remind_open_hours ?? 4));
  const [remindVerify, setRemindVerify] = useState(String(initial.remind_verify_hours ?? 24));
  // 群消息语言：空串表示跟随服务器缺省语言。旧版服务端不返回 languages，此时不显示也不提交
  const [language, setLanguage] = useState(initial.language ?? "");
  const hoursOk = (v: string) => /^\d{1,3}$/.test(v.trim()) && Number(v) <= 720;
  const remindOk = hoursOk(remindOpen) && hoursOk(remindVerify);
  const [error, setError] = useState("");
  const [ok, setOk] = useState("");
  const [busy, setBusy] = useState(false);
  const displayOk = [...display.trim()].length <= 40;
  const providers = initial.channels?.map((c) => c.provider);
  // 用码绑定的通讯工具：服务器启用了，或本工作空间已绑了它的群（停用后仍可解绑）
  const codeShown = codeProviders.filter((p) => !!providers?.includes(p) || initial.chat?.provider === p);
  // 旧版服务端不返回 channels；只启用了用码绑定的通讯工具时不显示 Telegram 群编号（已绑 Telegram 群的照样显示）
  const tgShown = !providers || providers.includes("telegram") || !!initial.telegram_chat || codeShown.length === 0;
  const codeBound = codeProviders.find((p) => initial.chat?.provider === p);
  const languages = initial.languages ?? [];
  const defaultLanguageName =
    languages.find((l) => l.code === initial.default_language)?.name ?? initial.default_language ?? "";
  const refresh = useCallback(() => {
    void qc.invalidateQueries({ queryKey: ["admin", ws, "workspace"] });
  }, [qc, ws]);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!displayOk || !remindOk) return;
    setBusy(true);
    setError("");
    setOk("");
    try {
      await api.saveWorkspaceSettings(ws, {
        telegram_chat: chat.trim(),
        show_title: showTitle,
        show_brief: showBrief,
        display_name: display.trim(),
        remind_open_hours: Number(remindOpen),
        remind_verify_hours: Number(remindVerify),
        language: languages.length > 0 ? language : undefined,
      });
      setOk(t("workspace.saved"));
      await qc.invalidateQueries({ queryKey: ["admin", ws, "workspace"] });
      await qc.invalidateQueries({ queryKey: ["me"] });
    } catch (err) {
      setError(errMsg(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Section title={t("workspace.title", { name: initial.display_name || initial.path })}>
      <form onSubmit={submit} className="space-y-4">
        <fieldset disabled={!!initial.archived} className="min-w-0 space-y-4">
        <Field
          label={t("workspace.displayName")}
          htmlFor="display-name"
          hint={
            displayOk ? (
              <Trans
                {...safeTrans}
                t={t}
                i18nKey="workspace.displayNameHint"
                values={{ path: initial.path }}
                components={{ path: <span className="font-mono" /> }}
              />
            ) : undefined
          }
        >
          <input
            id="display-name"
            value={display}
            onChange={(e) => setDisplay(e.target.value)}
            placeholder={t("workspace.displayNamePlaceholder", { path: initial.path })}
            className={inputClass}
          />
          {!displayOk && <p className="mt-1 text-xs text-danger">{t("workspace.displayNameError")}</p>}
        </Field>
        {languages.length > 0 && (
          <Field label={t("workspace.language.label")} htmlFor="ws-language" hint={t("workspace.language.hint")}>
            <select
              id="ws-language"
              value={language}
              onChange={(e) => setLanguage(e.target.value)}
              className={inputClass}
            >
              <option value="">{t("workspace.language.follow", { name: defaultLanguageName })}</option>
              {languages.map((l) => (
                <option key={l.code} value={l.code}>
                  {l.name}
                </option>
              ))}
            </select>
          </Field>
        )}
        <div className="text-sm">
          <p className="mb-1">{t("workspace.issueLink")}</p>
          {initial.external_url_set ? (
            <p className="break-all font-mono text-xs text-muted">{initial.issue_link_example}</p>
          ) : (
            <Callout>
              {serverAdmin ? (
                <Trans
                  t={t}
                  i18nKey="workspace.noExternalUrlAdmin"
                  components={{
                    l: <Link to="/server" search={{ tab: "settings" }} className="text-accent hover:underline" />,
                  }}
                />
              ) : (
                t("workspace.noExternalUrlOther")
              )}
            </Callout>
          )}
          <p className="mt-1 text-xs text-muted">{t("workspace.externalUrlNote")}</p>
        </div>
        {!initial.bot_enabled && codeShown.length === 0 && <Callout>{t("workspace.botDisabled")}</Callout>}
        {codeShown.map((p) => (
          <CodeBind key={p} ws={ws} chat={initial.chat} provider={p} onChange={refresh} />
        ))}
        {tgShown && (
          <Field
            label={t("workspace.telegramChat")}
            htmlFor="telegram-chat"
            hint={
              codeBound === "dingtalk"
                ? t("workspace.telegramChatBoundDingtalk")
                : codeBound === "slack"
                  ? t("workspace.telegramChatBoundSlack")
                  : t("workspace.telegramChatHint")
            }
          >
            <input
              id="telegram-chat"
              inputMode="numeric"
              spellCheck={false}
              value={chat}
              disabled={codeBound != null}
              onChange={(e) => setChat(e.target.value)}
              className={`${inputClass} font-mono`}
            />
          </Field>
        )}
        <fieldset className="space-y-2 text-sm">
          <legend className="mb-1">{t("workspace.showInGroup")}</legend>
          <label className="flex items-center gap-2">
            <input
              type="checkbox"
              checked={showTitle}
              onChange={(e) => setShowTitle(e.target.checked)}
              className="accent-[var(--accent)]"
            />
            {t("workspace.showTitle")}
          </label>
          <label className="flex items-center gap-2">
            <input
              type="checkbox"
              checked={showBrief}
              onChange={(e) => setShowBrief(e.target.checked)}
              className="accent-[var(--accent)]"
            />
            {t("workspace.showBrief")}
          </label>
          <p className="text-xs text-muted">{t("workspace.showHint")}</p>
        </fieldset>
        <fieldset className="space-y-2 text-sm">
          <legend className="mb-1">{t("workspace.remind.title")}</legend>
          <div className="flex flex-wrap gap-4">
            <label className="flex items-center gap-2">
              <Trans
                t={t}
                i18nKey="workspace.remind.open"
                components={{
                  input: (
                    <input
                      aria-label={t("workspace.remind.openLabel")}
                      inputMode="numeric"
                      value={remindOpen}
                      onChange={(e) => setRemindOpen(e.target.value)}
                      className={`${inputClass} w-20`}
                    />
                  ),
                }}
              />
            </label>
            <label className="flex items-center gap-2">
              <Trans
                t={t}
                i18nKey="workspace.remind.verify"
                components={{
                  input: (
                    <input
                      aria-label={t("workspace.remind.verifyLabel")}
                      inputMode="numeric"
                      value={remindVerify}
                      onChange={(e) => setRemindVerify(e.target.value)}
                      className={`${inputClass} w-20`}
                    />
                  ),
                }}
              />
            </label>
          </div>
          {!remindOk && <p className="text-xs text-danger">{t("workspace.remind.error")}</p>}
          <p className="text-xs text-muted">{t("workspace.remind.hint")}</p>
        </fieldset>
        <ErrorText>{error}</ErrorText>
        <SuccessText>{ok}</SuccessText>
        <button type="submit" disabled={busy || !displayOk} className={btn.primary}>
          {busy ? t("workspace.saving") : t("workspace.save")}
        </button>
        </fieldset>
      </form>
    </Section>
  );
}

// 归档 / 取消归档。归档时若还有未结束的问题单，须勾选确认，提交带上看到的数字；
// 后端发现数字变了（open_issues_unconfirmed）就用返回的数字刷新，要求重新勾选。
export function ArchiveSection({ ws, initial }: { ws: string; initial: WorkspaceSettings }) {
  const { t } = useTranslation("admin");
  const { t: tc } = useTranslation();
  const qc = useQueryClient();
  const archived = !!initial.archived;
  const [open, setOpen] = useState(false);
  const [count, setCount] = useState(initial.open_issues ?? 0);
  const [checked, setChecked] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const title = initial.display_name || initial.path;
  const needCheck = !archived && count > 0;

  const show = () => {
    setCount(initial.open_issues ?? 0);
    setChecked(false);
    setError("");
    setOpen(true);
  };
  const close = () => {
    if (busy) return;
    setOpen(false);
    setError("");
  };
  const run = async () => {
    setBusy(true);
    setError("");
    try {
      if (archived) await api.unarchiveWorkspace(ws);
      else await api.archiveWorkspace(ws, count);
      setOpen(false);
      await qc.invalidateQueries({ queryKey: ["admin", ws, "workspace"] });
      await qc.invalidateQueries({ queryKey: ["me"] });
    } catch (err) {
      if (err instanceof ApiError && err.status === 409 && err.body.code === "open_issues_unconfirmed") {
        const n = Number(err.body.open_issues);
        if (Number.isFinite(n)) {
          setCount(n);
          setChecked(false);
          setError(t("workspace.archive.recount", { count: n }));
          return;
        }
      }
      setError(errMsg(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Section title={t("workspace.archive.title")} description={t("workspace.archive.description")}>
      {archived && initial.archived_at && (
        <p className="mb-3 text-sm text-muted">{t("workspace.archive.status", { time: absoluteTime(initial.archived_at) })}</p>
      )}
      <button type="button" onClick={show} className={archived ? btn.secondary : btn.danger}>
        {archived ? t("workspace.archive.unarchive") : t("workspace.archive.button")}
      </button>
      <Dialog
        open={open}
        onClose={close}
        dismissible={!busy}
        title={
          archived
            ? t("workspace.archive.unarchiveTitle", { name: title })
            : t("workspace.archive.confirmTitle", { name: title })
        }
      >
        <div className="space-y-3 text-sm text-muted">
          <p>{archived ? t("workspace.archive.unarchiveMessage") : t("workspace.archive.plain")}</p>
          {needCheck && (
            <>
              <p className="text-danger">{t("workspace.archive.openIssues", { count })}</p>
              <label className="flex items-start gap-2 text-ink">
                <input
                  type="checkbox"
                  checked={checked}
                  onChange={(e) => setChecked(e.target.checked)}
                  className="mt-0.5 accent-[var(--accent)]"
                />
                {t("workspace.archive.openIssuesCheck", { count })}
              </label>
            </>
          )}
        </div>
        <ErrorText>{error}</ErrorText>
        <div className="mt-5 flex justify-end gap-2">
          <button type="button" onClick={close} disabled={busy} className={btn.secondary}>
            {tc("ui.cancel")}
          </button>
          <button
            type="button"
            onClick={run}
            disabled={busy || (needCheck && !checked)}
            className={archived ? btn.primary : btn.dangerSolid}
          >
            {busy
              ? tc("ui.processing")
              : archived
                ? t("workspace.archive.unarchiveConfirm")
                : t("workspace.archive.confirm")}
          </button>
        </div>
      </Dialog>
    </Section>
  );
}
