import { useEffect, useState } from "react";
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
} from "../api";
import { DeviceTokens, NeedPasswordLogin, Notice } from "../components";
import {
  absoluteTime,
  inviteKindLabel,
  memberNamePattern,
  relativeTime,
  roleLabel,
  signupKindLabel,
  toolsText,
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

export const adminTabs = [
  { id: "pending", label: "待审" },
  { id: "members", label: "成员" },
  { id: "devices", label: "设备" },
  { id: "invites", label: "邀请" },
  { id: "workspace", label: "工作空间" },
] as const;

export type AdminTab = (typeof adminTabs)[number]["id"];

export function AdminPage() {
  const ws = useWs();
  const { tab = "pending" } = useSearch({ from: "/layout/w/$ws/admin" });
  const me = useQuery({ queryKey: ["me"], queryFn: api.me });

  if (me.isPending) return <Notice>正在读取…</Notice>;
  if (me.isError) return <Notice tone="danger">读取失败：{me.error.message}</Notice>;
  if (!canAdminWorkspace(me.data, ws)) {
    return <Notice tone="danger">你没有这个工作空间的管理权限。成员、设备与邀请由本工作空间的管理员管理。</Notice>;
  }
  if (adminNeedsPasswordLogin(me.data)) {
    return (
      <NeedPasswordLogin
        redirectTo={`/w/${encodeURIComponent(ws)}/admin`}
        message="管理操作请在网页上用账号密码登录后进行。你现在是用已配对电脑免密打开的网页（cohive web），为安全起见不能做管理操作。"
      />
    );
  }
  const member = me.data.workspaces.some((w) => w.name === ws);
  const self = me.data.account.name;

  return (
    <div className="mx-auto max-w-4xl">
      <h1 className="mb-3 text-lg font-semibold">管理 · {ws}</h1>
      {!member && <JoinSelf ws={ws} self={self} />}
      <nav aria-label="管理分类" className="mb-5 flex flex-wrap gap-1 border-b border-line">
        {adminTabs.map((t) => (
          <Link
            key={t.id}
            to="/w/$ws/admin"
            params={{ ws }}
            search={{ tab: t.id }}
            aria-current={tab === t.id ? "page" : undefined}
            className={`-mb-px border-b-2 px-3 py-2 text-sm ${
              tab === t.id ? "border-teal text-ink" : "border-transparent text-muted hover:text-ink"
            }`}
          >
            {t.label}
          </Link>
        ))}
      </nav>
      {tab === "pending" && <PendingTab ws={ws} />}
      {tab === "members" && <MembersTab ws={ws} self={self} />}
      {tab === "devices" && <DevicesTab ws={ws} />}
      {tab === "invites" && <InvitesTab ws={ws} />}
      {tab === "workspace" && <WorkspaceTab ws={ws} />}
    </div>
  );
}

// 服务器管理员管理自己不是成员的工作空间：看板与问题单要先把自己加进来（留痕）才能看
function JoinSelf({ ws, self }: { ws: string; self: string }) {
  const qc = useQueryClient();
  return (
    <div className="mb-4">
      <Callout>
        <p>你不是这个工作空间的成员，现在是以服务器管理员身份管理它。要查看它的看板与问题单，先把自己加为成员。</p>
        <div className="mt-2">
          <ConfirmButton
            label="把自己加为管理员"
            title="把自己加入这个工作空间？"
            message={`以管理员身份加入工作空间“${ws}”。加入后会出现在它的成员列表里，群里也会收到加入通知。`}
            confirmLabel="确认加入"
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
  if (q.isPending) return <Notice>正在读取…</Notice>;
  if (q.isError) return <Notice tone="danger">读取失败：{q.error?.message}</Notice>;
  const d = q.data as T;
  if (isEmpty(d)) return <Notice>{empty}</Notice>;
  return <>{children(d)}</>;
}

// 批准 / 拒绝：批准直接执行，拒绝需二次确认
function DecideButtons({ name, onDecide }: { name: string; onDecide: (approve: boolean) => Promise<void> }) {
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
        {busy ? "正在批准…" : "批准"}
      </button>
      <ConfirmButton
        label="拒绝"
        title="拒绝这条申请？"
        message={`拒绝“${name}”的申请。拒绝后对方需要重新申请。`}
        confirmLabel="确认拒绝"
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
  return (
    <li className={rowClass}>
      <div className="min-w-0 flex-1">
        <p className="break-words">
          <span className="font-mono font-medium">{v.account}</span>
          {v.display_name && v.display_name !== v.account && <span className="ml-2">{v.display_name}</span>}
          <span className="ml-2">{v.device_name || "未命名"}</span>
          <span className="ml-1 text-xs text-muted">（终端自报）</span>
        </p>
        <p className="text-xs text-muted" title={absoluteTime(v.created_at)}>
          {toolsText(v.tools)} · {relativeTime(v.created_at)}申请
        </p>
        <p className="text-xs">
          批准后用于：{workspaceListText(v.workspaces, "（他目前不在任何工作空间）")}
        </p>
      </div>
      <DecideButtons name={`${v.account} 的 ${v.device_name || "设备"}`} onDecide={onDecide} />
    </li>
  );
}

function PendingTab({ ws }: { ws: string }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["admin", ws, "pending"], queryFn: () => api.pending(ws), refetchInterval: 10000 });
  const refresh = () => qc.invalidateQueries({ queryKey: ["admin", ws] });

  return (
    <div className="space-y-6">
      <section>
        <h2 className="mb-2 font-semibold">注册与加入申请</h2>
        <p className="mb-2 text-sm text-muted">通过团队邀请提交的申请：新人注册会新建账号；已有账号的人只是加入本工作空间。</p>
        <Loading q={q} empty="没有待审的注册或加入申请。" isEmpty={(d) => d.signups.length === 0}>
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
                      {relativeTime(s.created_at)}申请 · 通过团队邀请
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
        <h2 className="mb-2 font-semibold">设备申请</h2>
        <p className="mb-2 text-sm text-muted">
          成员已在浏览器里授权、但不是第一台设备的电脑，需要管理员批准后才能使用。设备属于账号，批准后可用于他所在的全部工作空间。
        </p>
        <Loading q={q} empty="没有待审的设备申请。" isEmpty={(d) => d.devices.length === 0}>
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

const memberLabel = (m: { account: string; display_name?: string }) =>
  m.display_name && m.display_name !== m.account ? `${m.display_name}（${m.account}）` : m.account;

function MembersTab({ ws, self }: { ws: string; self: string }) {
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
          添加已有账号
        </button>
        <button
          type="button"
          onClick={() => setView(view === "active" ? "removed" : "active")}
          className={`${btn.secondary} ml-auto`}
        >
          {view === "active" ? "查看已移出" : "返回在册成员"}
        </button>
      </div>
      <SuccessText>{notice}</SuccessText>

      {view === "active" ? (
        <Loading q={active} empty="还没有成员。添加已有账号，或到“邀请”里生成邀请链接。" isEmpty={(d) => d.items.length === 0}>
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
        <Loading q={removed} empty="没有被移出的成员。" isEmpty={(d) => d.items.length === 0}>
          {(d) => (
            <ul className={listClass}>
              {d.items.map((m) => (
                <li key={m.member_id} className={rowClass}>
                  <div className="min-w-0 flex-1">
                    <p className="flex flex-wrap items-center gap-x-2">
                      <span className="font-mono font-medium">{m.account}</span>
                      {m.display_name && <span>{m.display_name}</span>}
                      <span className="rounded bg-sunken px-1.5 py-px text-xs text-muted">已移出</span>
                      {m.disabled && <span className="rounded bg-sunken px-1.5 py-px text-xs text-danger">账号已停用</span>}
                    </p>
                  </div>
                  {!m.disabled && (
                    <ConfirmButton
                      label="加回"
                      title="把他加回这个工作空间？"
                      message={`“${memberLabel(m)}”将重新成为本工作空间的成员（角色：成员），恢复原来的成员资格。`}
                      confirmLabel="确认加回"
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
          setNotice(`已把 ${name} 加入本工作空间。`);
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
            setNotice(handover.remove ? `已移出 ${memberLabel(m)}。` : `已把 ${memberLabel(m)} 的单子和服务改派出去。`);
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
  const nextRole = m.role === "admin" ? "member" : "admin";
  const name = m.display_name || m.account;
  return (
    <li className={rowClass}>
      <div className="min-w-0 flex-1">
        <p className="flex flex-wrap items-center gap-x-2">
          <span className="font-mono font-medium">{m.account}</span>
          {m.display_name && <span>{m.display_name}</span>}
          {isSelf && <span className="text-xs text-muted">（你）</span>}
          <span
            className={`rounded px-1.5 py-px text-xs ${
              m.role === "admin" ? "bg-amber-soft text-amber" : "bg-sunken text-muted"
            }`}
          >
            {roleLabel(m.role)}
          </span>
          {m.disabled && (
            <span className="rounded bg-sunken px-1.5 py-px text-xs text-danger" title="账号已被服务器管理员停用">
              已停用
            </span>
          )}
        </p>
        <p className="text-xs text-muted">
          Telegram {m.telegram_linked ? `已连接${m.telegram_username ? ` @${m.telegram_username}` : ""}` : "未连接"}
          {" · "}
          {m.devices} 台设备
          {" · "}
          {m.has_password ? "已设密码" : "未设密码"}
          {" · "}
          <span className={m.pending_issues > 0 ? "text-ink" : undefined}>待处理单 {m.pending_issues} 张</span>
          {" · "}
          <span className={m.owned_services > 0 ? "text-ink" : undefined}>负责服务 {m.owned_services} 个</span>
        </p>
        {m.disabled && (m.pending_issues > 0 || m.owned_services > 0) && (
          <p className="mt-1 text-xs text-amber">
            账号已停用，但手上还有单子或登记为服务负责人，新问题单仍会路由给他。可以点“改派”交给别人。
          </p>
        )}
      </div>
      <div className="flex flex-wrap gap-2">
        <ConfirmButton
          label={nextRole === "admin" ? "设为管理员" : "设为成员"}
          danger={nextRole === "member"}
          title={nextRole === "admin" ? "设为管理员？" : "取消管理员身份？"}
          message={
            nextRole === "admin"
              ? `“${name}”将可以审批注册与设备、管理成员和邀请。`
              : isSelf
                ? "你将失去这个工作空间的管理权限（服务器管理员除外），之后无法再打开它的管理页。确定要取消自己的管理员身份吗？"
                : `“${name}”将不再能管理这个工作空间。`
          }
          confirmLabel="确认"
          action={async () => {
            await api.setMemberRole(ws, m.member_id, nextRole);
            await onChanged();
          }}
        />
        {m.disabled && (
          <button type="button" onClick={() => onHandover(false)} className={btn.secondary}>
            改派
          </button>
        )}
        <button type="button" onClick={() => onHandover(true)} className={btn.danger}>
          移出
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
      setError("登录名只能是小写字母、数字、下划线和横线，1 到 32 位。");
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
          ? `没有登录名为 ${trimmed} 的账号。登录名要完全一致；还没有账号的新人请用“邀请”里的链接注册。`
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
      await onAdded(found.display_name ? `${found.display_name}（${found.name}）` : found.name);
    } catch (err) {
      setError(errMsg(err));
    } finally {
      setBusy(false);
    }
  };

  const canAdd = !!found && found.member_status !== "active" && !found.disabled;

  return (
    <Dialog open={open} onClose={() => !busy && onClose()} title="添加已有账号" dismissible={!busy}>
      <form onSubmit={lookup} className="space-y-3">
        <Field label="登录名" htmlFor="add-account" hint="问对方要他的登录名，必须完全一致。">
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
              查找
            </button>
          </div>
        </Field>
      </form>
      {found && (
        <div className="mt-3 space-y-3 rounded border border-line bg-bg px-3 py-2 text-sm">
          <p>
            <span className="font-mono font-medium">{found.name}</span>
            {found.display_name && <span className="ml-2">{found.display_name}</span>}
          </p>
          {found.disabled ? (
            <p className="text-danger">这个账号已被停用，不能加入。</p>
          ) : found.member_status === "active" ? (
            <p className="text-muted">他已经是本工作空间的成员。</p>
          ) : (
            <>
              {found.member_status === "removed" && (
                <p className="text-muted">他以前被移出过本工作空间，加入后恢复原来的成员资格。</p>
              )}
              <label className="flex items-center gap-2">
                <span>角色</span>
                <select
                  value={role}
                  onChange={(e) => setRole(e.target.value as "member" | "admin")}
                  className="rounded border border-line bg-surface px-2 py-1 text-sm"
                >
                  <option value="member">成员</option>
                  <option value="admin">管理员</option>
                </select>
              </label>
            </>
          )}
        </div>
      )}
      <ErrorText>{error}</ErrorText>
      <div className="mt-5 flex justify-end gap-2">
        <button type="button" onClick={onClose} disabled={busy} className={btn.secondary}>
          取消
        </button>
        <button type="button" onClick={add} disabled={busy || !canAdd} className={btn.primary}>
          {busy && found ? "正在加入…" : "加入"}
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
      setError(`还有 ${r.missing} 项没有选接手人。`);
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
        setError(`${errMsg(err).replace(/[。.]$/, "")}。清单已按最新情况刷新，请检查后再提交。`);
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
      className="w-full rounded border border-line bg-surface px-2 py-1 text-sm sm:w-48"
    >
      <option value="">选择接手人</option>
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
      title={remove ? `移出 ${name}` : `改派 ${name} 的单子和服务`}
      dismissible={!busy}
    >
      <p className="text-sm text-muted">
        {remove
          ? "移出后，他在本工作空间的编程工具令牌立即失效，也看不到本工作空间的看板和问题单；他的账号、设备和其他工作空间不受影响。他手上未结束的单子和登记的服务必须先转给别人。"
          : "只改派、不移出：把他手上未结束的单子和登记的服务交给别人，他仍是本工作空间的成员。"}
      </p>
      {loadError ? (
        <ErrorText>读取转交清单失败：{loadError}</ErrorText>
      ) : !h ? (
        <p className="mt-3 text-sm text-muted">正在读取转交清单…</p>
      ) : count === 0 ? (
        <p className="mt-3 text-sm">{remove ? "他手上没有需要转交的单子和服务。" : "他手上没有需要改派的单子和服务。"}</p>
      ) : candidates.length === 0 ? (
        <Callout>本工作空间没有其他可以接手的成员（在册且未停用）。请先添加成员再{remove ? "移出" : "改派"}。</Callout>
      ) : (
        <div className="mt-3 space-y-3">
          <label className="flex flex-wrap items-center gap-2 text-sm">
            <span className="font-medium">全部转给</span>
            <select
              value={allTo}
              onChange={(e) => {
                setAllTo(e.target.value);
                if (e.target.value) setAssign(assignAll(h, e.target.value));
              }}
              className="rounded border border-line bg-surface px-2 py-1 text-sm"
            >
              <option value="">逐项选择</option>
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
                    {it.status_label} · {it.as === "owner" ? "他是负责人" : "他是发起人（之后的验证与关闭要有人接手）"}
                  </p>
                </div>
                {picker(issueKey(it), `#${it.id} 的接手人`)}
              </li>
            ))}
            {h.services.map((sv) => (
              <li key={serviceKey(sv)} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2 text-sm">
                <div className="min-w-0 flex-1">
                  <p className="break-words">
                    服务 <span className="font-mono">{sv.name}</span>
                  </p>
                  <p className="text-xs text-muted">他是登记的负责人，新问题单会路由给接手人</p>
                </div>
                {picker(serviceKey(sv), `服务 ${sv.name} 的接手人`)}
              </li>
            ))}
          </ul>
        </div>
      )}
      <ErrorText>{error}</ErrorText>
      <div className="mt-5 flex justify-end gap-2">
        <button type="button" onClick={onClose} disabled={busy} className={btn.secondary}>
          取消
        </button>
        <button
          type="button"
          onClick={submit}
          disabled={busy || !h || (count > 0 && candidates.length === 0) || (!remove && count === 0)}
          className={remove ? btn.dangerSolid : btn.primary}
        >
          {busy ? "正在处理…" : remove ? (count > 0 ? "转交并移出" : "确认移出") : "确认改派"}
        </button>
      </div>
    </Dialog>
  );
}

function DevicesTab({ ws }: { ws: string }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["admin", ws, "devices"], queryFn: () => api.devices(ws) });

  return (
    <div className="space-y-2">
      <p className="text-sm text-muted">本工作空间在册成员的电脑。设备属于账号，吊销会让这台电脑在他所在的全部工作空间里都失去访问权限。</p>
      <Loading q={q} empty="还没有成员绑定电脑。" isEmpty={(d) => d.items.length === 0}>
        {(d) => (
          <ul className={listClass}>
            {d.items.map((v) => (
              <li key={v.id} className={rowClass}>
                <div className="min-w-0 flex-1">
                  <p className="break-words">
                    <span className="font-mono font-medium">{v.account}</span>
                    <span className="ml-2">{v.name || "未命名"}</span>
                  </p>
                  <DeviceTokens tokens={v.tokens} revoked={!!v.revoked_at} />
                  <p className="text-xs text-muted">
                    {absoluteTime(v.created_at)} 添加
                    {v.last_used_at && ` · 最近使用 ${absoluteTime(v.last_used_at)}`}
                    {v.revoked_at && ` · ${absoluteTime(v.revoked_at)} 吊销`}
                  </p>
                </div>
                {v.revoked_at ? (
                  <span className="rounded bg-sunken px-1.5 py-px text-xs text-muted">已吊销</span>
                ) : (
                  <>
                    <span className="rounded bg-teal-soft px-1.5 py-px text-xs text-teal">使用中</span>
                    <ConfirmButton
                      label="吊销"
                      title="吊销这台设备？"
                      message={`吊销后，${v.account} 的“${v.name || "未命名"}”会立即失去访问权限（包括他所在的其他工作空间），需要重新执行 cohive login 并经过审批。`}
                      confirmLabel="确认吊销"
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

function inviteState(inv: Invite): { label: string; active: boolean } {
  if (inv.revoked_at) return { label: "已作废", active: false };
  if (new Date(inv.expires_at).getTime() <= Date.now()) return { label: "已过期", active: false };
  if (inv.max_uses && inv.uses >= inv.max_uses) return { label: "已用完", active: false };
  return { label: "有效", active: true };
}

function InvitesTab({ ws }: { ws: string }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["admin", ws, "invites"], queryFn: () => api.invites(ws) });
  const [created, setCreated] = useState<CreatedInvite | null>(null);

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
        <h2 className="mb-2 font-semibold">已生成的邀请</h2>
        <Loading q={q} empty="还没有生成过邀请。" isEmpty={(d) => d.items.length === 0}>
          {(d) => (
            <ul className={listClass}>
              {d.items.map((inv) => {
                const st = inviteState(inv);
                return (
                  <li key={inv.id} className={rowClass}>
                    <div className="min-w-0 flex-1">
                      <p className="flex flex-wrap items-center gap-x-2">
                        <span className="font-medium">{inviteKindLabel(inv.kind)}</span>
                        <span className="text-muted">加入后为{roleLabel(inv.role)}</span>
                        <span
                          className={`rounded px-1.5 py-px text-xs ${
                            st.active ? "bg-teal-soft text-teal" : "bg-sunken text-muted"
                          }`}
                        >
                          {st.label}
                        </span>
                      </p>
                      <p className="text-xs text-muted">
                        已用 {inv.uses} / {inv.max_uses ? inv.max_uses : "不限"} · {absoluteTime(inv.expires_at)} 过期 ·{" "}
                        {absoluteTime(inv.created_at)} 生成
                      </p>
                    </div>
                    {st.active && (
                      <ConfirmButton
                        label="作废"
                        title="作废这个邀请？"
                        message="作废后链接立即失效，已经用它加入的成员不受影响。"
                        confirmLabel="确认作废"
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
  return (
    <Dialog open={!!url} onClose={onDone} title="邀请链接已生成" dismissible={false}>
      {url && (
        <div className="space-y-3">
          <CopyField label="邀请链接" value={url} />
          <Callout>链接只显示这一次，关闭后无法再查看。请现在复制并发给对方；丢了就作废后重新生成。</Callout>
          <div className="flex justify-end">
            <button type="button" onClick={onDone} className={btn.primary}>
              我已复制
            </button>
          </div>
        </div>
      )}
    </Dialog>
  );
}

function NewInviteForm({ ws, onCreated }: { ws: string; onCreated: (inv: CreatedInvite) => Promise<void> }) {
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
        kind === value ? "border-teal bg-teal-soft" : "border-line bg-bg"
      }`}
    >
      <input
        type="radio"
        name="invite-kind"
        value={value}
        checked={kind === value}
        onChange={() => setKind(value)}
        className="mt-1 accent-[var(--teal)]"
      />
      <span>
        <span className="block font-medium">{title}</span>
        <span className="block text-xs text-muted">{desc}</span>
      </span>
    </label>
  );

  return (
    <Section
      title="生成邀请"
      description="把链接发给要加入的人：新人打开后自己设登录名和密码；已有账号的人登录后可以直接以自己的账号加入。已有账号的人也可以在“成员”里直接添加。"
    >
      <form onSubmit={submit} className="space-y-4">
        <fieldset>
          <legend className="mb-1 text-sm">邀请类型</legend>
          <div className="flex flex-col gap-2 sm:flex-row">
            {radio("personal", "个人邀请", "一次性，加入后直接可用，无需审批。")}
            {radio("team", "团队邀请", "多人可用，每个注册或加入都需要管理员批准。")}
          </div>
        </fieldset>
        <div className="grid gap-4 sm:grid-cols-2">
          {kind === "personal" ? (
            <label className="flex items-center gap-2 self-end text-sm">
              <input
                type="checkbox"
                checked={asAdmin}
                onChange={(e) => setAsAdmin(e.target.checked)}
                className="accent-[var(--teal)]"
              />
              加入后设为管理员
            </label>
          ) : (
            <Field label="可用次数" htmlFor="max-uses" hint={usesOk ? "1 到 100。" : undefined}>
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
              {!usesOk && <p className="mt-1 text-xs text-danger">请填 1 到 100 之间的整数。</p>}
            </Field>
          )}
          <Field label="有效期（小时）" htmlFor="ttl" hint={ttlOk ? "默认 168 小时，即 7 天。" : undefined}>
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
            {!ttlOk && <p className="mt-1 text-xs text-danger">请填 1 到 720 之间的整数（最长 30 天）。</p>}
          </Field>
        </div>
        <ErrorText>{error}</ErrorText>
        <button type="submit" disabled={busy || !usesOk || !ttlOk} className={btn.primary}>
          {busy ? "正在生成…" : "生成邀请链接"}
        </button>
      </form>
    </Section>
  );
}

function WorkspaceTab({ ws }: { ws: string }) {
  const q = useQuery({ queryKey: ["admin", ws, "workspace"], queryFn: () => api.workspaceSettings(ws) });
  if (q.isPending) return <Notice>正在读取…</Notice>;
  if (q.isError) return <Notice tone="danger">读取失败：{q.error.message}</Notice>;
  return (
    <div className="space-y-4">
      {!q.data.telegram_chat && (
        <Callout>
          下一步：把 Telegram 机器人拉进项目群，在群里发 /chatid，把得到的群编号填到下面并保存；然后到“成员”里添加已有账号，或到“邀请”里生成邀请链接。
        </Callout>
      )}
      <WorkspaceForm key={ws} ws={ws} initial={q.data} />
    </div>
  );
}

function WorkspaceForm({ ws, initial }: { ws: string; initial: WorkspaceSettings }) {
  const qc = useQueryClient();
  const [publicUrl, setPublicUrl] = useState(initial.public_url ?? "");
  const [chat, setChat] = useState(initial.telegram_chat == null ? "" : String(initial.telegram_chat));
  const [showTitle, setShowTitle] = useState(initial.show_title);
  const [showBrief, setShowBrief] = useState(initial.show_brief);
  const [error, setError] = useState("");
  const [ok, setOk] = useState("");
  const [busy, setBusy] = useState(false);

  const trimmedUrl = publicUrl.trim().replace(/\/+$/, "");
  const urlOk = trimmedUrl === "" || /^https?:\/\/[^\s/]+/.test(trimmedUrl);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!urlOk) return;
    setBusy(true);
    setError("");
    setOk("");
    try {
      await api.saveWorkspaceSettings(ws, {
        public_url: trimmedUrl,
        telegram_chat: chat.trim(),
        show_title: showTitle,
        show_brief: showBrief,
      });
      setPublicUrl(trimmedUrl);
      setOk("已保存。");
      await qc.invalidateQueries({ queryKey: ["admin", ws, "workspace"] });
    } catch (err) {
      setError(errMsg(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Section title={`工作空间：${initial.name}`}>
      <form onSubmit={submit} className="space-y-4">
        <Field
          label="公网地址"
          htmlFor="public-url"
          hint={urlOk ? "同事和群消息里的链接都用这个地址，例如 https://cohive.example.com。留空则不在群消息里放链接。" : undefined}
        >
          <input
            id="public-url"
            type="url"
            inputMode="url"
            autoCapitalize="none"
            spellCheck={false}
            value={publicUrl}
            onChange={(e) => setPublicUrl(e.target.value)}
            placeholder={window.location.origin}
            className={inputClass}
          />
          {!urlOk && <p className="mt-1 text-xs text-danger">请填以 http:// 或 https:// 开头的地址。</p>}
        </Field>
        {!initial.bot_enabled && (
          <Callout>
            协作服务没有启用 Telegram 机器人，下面的群设置暂时不会生效。请在服务器上配置机器人后重启协作服务。
          </Callout>
        )}
        <Field
          label="Telegram 群编号"
          htmlFor="telegram-chat"
          hint="把机器人拉进群后，在群里发 /chatid 获取，形如 -1001234567890。留空则不发群消息。"
        >
          <input
            id="telegram-chat"
            inputMode="numeric"
            spellCheck={false}
            value={chat}
            onChange={(e) => setChat(e.target.value)}
            className={`${inputClass} font-mono`}
          />
        </Field>
        <fieldset className="space-y-2 text-sm">
          <legend className="mb-1">群消息里显示</legend>
          <label className="flex items-center gap-2">
            <input
              type="checkbox"
              checked={showTitle}
              onChange={(e) => setShowTitle(e.target.checked)}
              className="accent-[var(--teal)]"
            />
            问题单标题
          </label>
          <label className="flex items-center gap-2">
            <input
              type="checkbox"
              checked={showBrief}
              onChange={(e) => setShowBrief(e.target.checked)}
              className="accent-[var(--teal)]"
            />
            问题单简述
          </label>
          <p className="text-xs text-muted">群里有外部人员时可以关掉，只发编号与链接。</p>
        </fieldset>
        <ErrorText>{error}</ErrorText>
        <SuccessText>{ok}</SuccessText>
        <button type="submit" disabled={busy || !urlOk} className={btn.primary}>
          {busy ? "正在保存…" : "保存"}
        </button>
      </form>
    </Section>
  );
}
