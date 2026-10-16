import { useState } from "react";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { keepPreviousData, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type ServerAccount, type ServerWorkspace } from "../api";
import { NeedPasswordLogin, Notice } from "../components";
import { absoluteTime, memberNamePattern, roleLabel } from "../format";
import { btn, ConfirmButton, ErrorText, errMsg, Field, inputClass, Section } from "../ui";
import { adminNeedsPasswordLogin } from "../rules";
import { PendingDeviceRow } from "./Admin";

export const serverTabs = [
  { id: "workspaces", label: "工作空间" },
  { id: "accounts", label: "账号" },
  { id: "devices", label: "待批设备" },
] as const;

export type ServerTab = (typeof serverTabs)[number]["id"];

const listClass = "divide-y divide-line rounded-lg border border-line bg-surface";
const rowClass = "flex flex-wrap items-center gap-x-4 gap-y-2 px-3 py-3 text-sm";
const tag = "rounded px-1.5 py-px text-xs";

// 服务器管理：工作空间、账号、待批设备。只有服务器管理员能用，且要求账号密码登录。
export function ServerPage() {
  const { tab = "workspaces" } = useSearch({ from: "/layout/server" });
  const me = useQuery({ queryKey: ["me"], queryFn: api.me });

  if (me.isPending) return <Notice>正在读取…</Notice>;
  if (me.isError) return <Notice tone="danger">读取失败：{me.error.message}</Notice>;
  if (!me.data.account.server_admin) {
    return <Notice tone="danger">你没有服务器管理权限。工作空间与账号由服务器管理员管理。</Notice>;
  }
  if (adminNeedsPasswordLogin(me.data)) {
    return (
      <NeedPasswordLogin
        redirectTo="/server"
        message="服务器管理请在网页上用账号密码登录后进行。你现在是用已配对电脑免密打开的网页（cohive web），为安全起见不能做管理操作。"
      />
    );
  }

  return (
    <div className="mx-auto max-w-4xl">
      <h1 className="mb-3 text-lg font-semibold">服务器管理</h1>
      <nav aria-label="服务器管理分类" className="mb-5 flex flex-wrap gap-1 border-b border-line">
        {serverTabs.map((t) => (
          <Link
            key={t.id}
            to="/server"
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
      {tab === "workspaces" && <WorkspacesTab self={me.data.account.name} />}
      {tab === "accounts" && <AccountsTab self={me.data.account.name} />}
      {tab === "devices" && <DevicesTab />}
    </div>
  );
}

function WorkspacesTab({ self }: { self: string }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["server", "workspaces"], queryFn: api.serverWorkspaces });

  return (
    <div className="space-y-6">
      <NewWorkspaceForm self={self} />
      <section>
        <h2 className="mb-2 font-semibold">全部工作空间</h2>
        {q.isPending ? (
          <Notice>正在读取…</Notice>
        ) : q.isError ? (
          <Notice tone="danger">读取失败：{q.error.message}</Notice>
        ) : q.data.items.length === 0 ? (
          <Notice>还没有工作空间。用上面的表单新建一个。</Notice>
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
  const admins = w.admins ?? [];
  return (
    <li className={rowClass}>
      <div className="min-w-0 flex-1">
        <p className="flex flex-wrap items-center gap-x-2">
          <span className="font-mono font-medium">{w.name}</span>
          {w.i_am_member && <span className={`${tag} bg-teal-soft text-teal`}>你是成员</span>}
          <span className={`${tag} ${w.telegram_bound ? "bg-sunken text-muted" : "bg-amber-soft text-amber"}`}>
            {w.telegram_bound ? "已绑定群" : "未绑定群"}
          </span>
        </p>
        <p className="text-xs text-muted">
          {w.members} 名成员 · 管理员 {admins.length ? admins.join("、") : "无"} · 未结束的问题单 {w.open_issues} 张 ·{" "}
          {absoluteTime(w.created_at)} 创建
        </p>
      </div>
      <div className="flex flex-wrap gap-2">
        {w.i_am_member && (
          <Link to="/w/$ws" params={{ ws: w.name }} className={btn.secondary}>
            看板
          </Link>
        )}
        <Link to="/w/$ws/admin" params={{ ws: w.name }} className={btn.secondary}>
          进入管理
        </Link>
        <ConfirmButton
          label="删除"
          title={`删除工作空间 ${w.name}？`}
          message={
            <>
              <p>只能删除没有任何问题单（含草稿）的工作空间，例如建错了名字。</p>
              <p className="mt-2">
                会一并删除它的邀请、待审申请、服务、成员资格及令牌、群绑定。成员的账号与设备不受影响。删除后不能恢复。
              </p>
            </>
          }
          confirmLabel="确认删除"
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
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [name, setName] = useState("");
  const [admin, setAdmin] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const trimmed = name.trim();
  const adminName = admin.trim();
  const nameError = trimmed && !memberNamePattern.test(trimmed) ? "只能用小写字母、数字、下划线和横线，1 到 32 位，第一个字符须是字母或数字。" : "";
  const adminError = adminName && !memberNamePattern.test(adminName) ? "请填一个登录名。" : "";
  const ready = memberNamePattern.test(trimmed) && !adminError && !busy;

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!ready) return;
    setBusy(true);
    setError("");
    try {
      const res = await api.createWorkspace(adminName ? { name: trimmed, admin: adminName } : { name: trimmed });
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
    <Section title="新建工作空间" description="一个项目组一个工作空间。名字建好后不能修改，会出现在链接和项目配置里。">
      <form onSubmit={submit} className="space-y-4">
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="名字" htmlFor="ws-name" hint={nameError ? undefined : "规则同登录名，例如 payments。"}>
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
          <Field
            label="首位管理员（可选）"
            htmlFor="ws-admin"
            hint={adminError ? undefined : `填已有账号的登录名；留空则是你自己（${self}）。`}
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
          {busy ? "正在新建…" : "新建"}
        </button>
      </form>
    </Section>
  );
}

function AccountsTab({ self }: { self: string }) {
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
        按登录名前缀搜索
      </label>
      <input
        id="account-q"
        autoCapitalize="none"
        spellCheck={false}
        value={q}
        onChange={(e) => setQ(e.target.value)}
        placeholder="按登录名前缀搜索"
        className={`${inputClass} sm:max-w-sm`}
      />
      {list.isPending ? (
        <Notice>正在读取…</Notice>
      ) : list.isError ? (
        <Notice tone="danger">读取失败：{list.error.message}</Notice>
      ) : list.data.items.length === 0 ? (
        <Notice>{query ? "没有以这个前缀开头的账号。" : "还没有账号。"}</Notice>
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
  const label = a.display_name && a.display_name !== a.name ? `${a.display_name}（${a.name}）` : a.name;
  const memberships = a.workspaces ?? [];
  return (
    <li className={rowClass}>
      <div className="min-w-0 flex-1">
        <p className="flex flex-wrap items-center gap-x-2">
          <span className="font-mono font-medium">{a.name}</span>
          {a.display_name && <span>{a.display_name}</span>}
          {isSelf && <span className="text-xs text-muted">（你）</span>}
          {a.server_admin && <span className={`${tag} bg-amber-soft text-amber`}>服务器管理员</span>}
          {a.disabled && <span className={`${tag} bg-sunken text-danger`}>已停用</span>}
        </p>
        <p className="text-xs text-muted">
          Telegram {a.telegram_linked ? "已连接" : "未连接"} · {a.devices} 台设备
        </p>
        <p className="text-xs text-muted">
          工作空间：
          {memberships.length === 0
            ? "无"
            : memberships.map((m, i) => (
                <span key={m.name}>
                  {i > 0 && "、"}
                  <span className={m.status === "removed" ? "line-through" : "text-ink"}>{m.name}</span>
                  （{m.status === "removed" ? "已移出" : roleLabel(m.role)}）
                </span>
              ))}
        </p>
      </div>
      <div className="flex flex-wrap gap-2">
        <ConfirmButton
          label={a.server_admin ? "取消服务器管理员" : "设为服务器管理员"}
          danger={a.server_admin}
          title={a.server_admin ? "取消服务器管理员？" : "设为服务器管理员？"}
          message={
            a.server_admin
              ? isSelf
                ? "你将失去服务器管理权限，之后无法再打开这个页面。服务器上至少要保留一名服务器管理员。"
                : `“${label}”将不再能管理全部工作空间与账号。`
              : `“${label}”将可以新建、删除工作空间，管理全部工作空间与账号，停用任何人。`
          }
          confirmLabel="确认"
          action={async () => {
            await api.setServerAdmin(a.name, !a.server_admin);
            await onChanged();
          }}
        />
        {a.disabled ? (
          <ConfirmButton
            label="启用"
            title="启用这个账号？"
            message={`“${label}”可以重新登录。他之前的设备不会恢复：要重新执行 cohive login 并等管理员批准；Telegram 也要重新连接。`}
            confirmLabel="确认启用"
            danger={false}
            action={async () => {
              await api.enableAccount(a.name);
              await onChanged();
            }}
          />
        ) : (
          !isSelf && (
            <ConfirmButton
              label="停用"
              title="停用这个账号？"
              message={`停用作用于“${label}”所在的全部工作空间：立即吊销他的全部设备与令牌、注销网页登录、断开 Telegram，待批的设备申请一并拒绝。成员资格保留，手上的单子不会自动转交，可以到各工作空间的管理页改派。`}
              confirmLabel="确认停用"
              action={async () => {
                await api.disableAccount(a.name);
                await onChanged();
              }}
            />
          )
        )}
        {a.telegram_linked && (
          <ConfirmButton
            label="断开 Telegram"
            title="断开他的 Telegram 连接？"
            message={`断开后群里的通知不会再 @ 到“${label}”，他也不能再在群里点按钮审批。他可以在个人中心重新连接。`}
            confirmLabel="确认断开"
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
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["server", "pending-devices"], queryFn: api.serverPendingDevices, refetchInterval: 10000 });

  return (
    <div className="space-y-2">
      <p className="text-sm text-muted">
        全部待批的设备申请。一般由申请人所在工作空间的管理员批准；申请人不在任何工作空间时只能在这里批。
      </p>
      {q.isPending ? (
        <Notice>正在读取…</Notice>
      ) : q.isError ? (
        <Notice tone="danger">读取失败：{q.error.message}</Notice>
      ) : q.data.items.length === 0 ? (
        <Notice>没有待批的设备申请。</Notice>
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
