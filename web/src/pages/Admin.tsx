import { useState } from "react";
import { Link, useSearch } from "@tanstack/react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type CreatedInvite, type Invite, type WorkspaceSettings } from "../api";
import { Notice } from "../components";
import { absoluteTime, inviteKindLabel, relativeTime, roleLabel, toolsText } from "../format";
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

export const adminTabs = [
  { id: "pending", label: "待审" },
  { id: "members", label: "成员" },
  { id: "devices", label: "设备" },
  { id: "invites", label: "邀请" },
  { id: "workspace", label: "工作空间" },
] as const;

export type AdminTab = (typeof adminTabs)[number]["id"];

export function AdminPage() {
  const { tab = "pending" } = useSearch({ from: "/layout/admin" });
  const me = useQuery({ queryKey: ["me"], queryFn: api.me });

  if (me.isPending) return <Notice>正在读取…</Notice>;
  if (me.isError) return <Notice tone="danger">读取失败：{me.error.message}</Notice>;
  if (me.data.role !== "admin") {
    return <Notice tone="danger">你没有管理权限。成员、设备与邀请由本工作空间的管理员管理。</Notice>;
  }

  return (
    <div className="mx-auto max-w-4xl">
      <h1 className="mb-3 text-lg font-semibold">管理 · {me.data.workspace}</h1>
      <nav aria-label="管理分类" className="mb-5 flex flex-wrap gap-1 border-b border-line">
        {adminTabs.map((t) => (
          <Link
            key={t.id}
            to="/admin"
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
      {tab === "pending" && <PendingTab />}
      {tab === "members" && <MembersTab self={me.data.member} />}
      {tab === "devices" && <DevicesTab />}
      {tab === "invites" && <InvitesTab />}
      {tab === "workspace" && <WorkspaceTab />}
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

function PendingTab() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["admin", "pending"], queryFn: api.pending, refetchInterval: 10000 });
  const refresh = () => qc.invalidateQueries({ queryKey: ["admin"] });

  return (
    <div className="space-y-6">
      <section>
        <h2 className="mb-2 font-semibold">注册申请</h2>
        <Loading q={q} empty="没有待审的注册申请。" isEmpty={(d) => d.signups.length === 0}>
          {(d) => (
            <ul className={listClass}>
              {d.signups.map((s) => (
                <li key={s.id} className={rowClass}>
                  <div className="min-w-0 flex-1">
                    <p>
                      <span className="font-mono font-medium">{s.name}</span>
                      {s.display_name && <span className="ml-2">{s.display_name}</span>}
                    </p>
                    <p className="text-xs text-muted" title={absoluteTime(s.created_at)}>
                      {relativeTime(s.created_at)}申请 · 通过团队邀请注册
                    </p>
                  </div>
                  <DecideButtons
                    name={s.display_name || s.name}
                    onDecide={async (approve) => {
                      await api.decideSignup(s.id, approve);
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
        <p className="mb-2 text-sm text-muted">成员已在浏览器里授权、但不是第一台设备的电脑，需要你批准后才能使用。</p>
        <Loading q={q} empty="没有待审的设备申请。" isEmpty={(d) => d.devices.length === 0}>
          {(d) => (
            <ul className={listClass}>
              {d.devices.map((v) => (
                <li key={v.id} className={rowClass}>
                  <div className="min-w-0 flex-1">
                    <p className="break-words">
                      <span className="font-mono font-medium">{v.member}</span>
                      <span className="ml-2">{v.device_name || "未命名"}</span>
                      <span className="ml-1 text-xs text-muted">（终端自报）</span>
                    </p>
                    <p className="text-xs text-muted" title={absoluteTime(v.created_at)}>
                      {toolsText(v.tools)} · {relativeTime(v.created_at)}申请
                    </p>
                  </div>
                  <DecideButtons
                    name={`${v.member} 的 ${v.device_name || "设备"}`}
                    onDecide={async (approve) => {
                      await api.decideDevice(v.id, approve);
                      await refresh();
                    }}
                  />
                </li>
              ))}
            </ul>
          )}
        </Loading>
      </section>
    </div>
  );
}

function MembersTab({ self }: { self: string }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["admin", "members"], queryFn: api.members });
  const refresh = () => qc.invalidateQueries({ queryKey: ["admin", "members"] });

  return (
    <Loading q={q} empty="还没有成员。到“邀请”里生成邀请链接。" isEmpty={(d) => d.items.length === 0}>
      {(d) => (
        <ul className={listClass}>
          {d.items.map((m) => {
            const isSelf = m.name === self;
            const nextRole = m.role === "admin" ? "member" : "admin";
            return (
              <li key={m.id} className={rowClass}>
                <div className="min-w-0 flex-1">
                  <p className="flex flex-wrap items-center gap-x-2">
                    <span className="font-mono font-medium">{m.name}</span>
                    {m.display_name && <span>{m.display_name}</span>}
                    {isSelf && <span className="text-xs text-muted">（你）</span>}
                    <span
                      className={`rounded px-1.5 py-px text-xs ${
                        m.role === "admin" ? "bg-amber-soft text-amber" : "bg-sunken text-muted"
                      }`}
                    >
                      {roleLabel(m.role)}
                    </span>
                  </p>
                  <p className="text-xs text-muted">
                    Telegram {m.telegram_linked ? `已连接${m.telegram_username ? ` @${m.telegram_username}` : ""}` : "未连接"}
                    {" · "}
                    {m.devices} 台设备
                    {" · "}
                    {m.has_password ? "已设密码" : "未设密码"}
                  </p>
                </div>
                <div className="flex flex-wrap gap-2">
                  <ConfirmButton
                    label={nextRole === "admin" ? "设为管理员" : "设为成员"}
                    danger={nextRole === "member"}
                    title={nextRole === "admin" ? "设为管理员？" : "取消管理员身份？"}
                    message={
                      nextRole === "admin"
                        ? `“${m.display_name || m.name}”将可以审批注册与设备、管理成员和邀请。`
                        : isSelf
                          ? "你将失去管理权限，之后无法再打开管理页。确定要取消自己的管理员身份吗？"
                          : `“${m.display_name || m.name}”将不再能管理这个工作空间。`
                    }
                    confirmLabel="确认"
                    action={async () => {
                      await api.setMemberRole(m.id, nextRole);
                      await refresh();
                      if (isSelf) await qc.invalidateQueries({ queryKey: ["me"] });
                    }}
                  />
                  {m.telegram_linked && (
                    <ConfirmButton
                      label="解除 Telegram"
                      title="解除 Telegram 连接？"
                      message={`解除后群里的通知不会再 @ 到“${m.display_name || m.name}”，对方可以在个人中心重新连接。`}
                      confirmLabel="确认解除"
                      action={async () => {
                        await api.unlinkMemberTelegram(m.id);
                        await refresh();
                      }}
                    />
                  )}
                </div>
              </li>
            );
          })}
        </ul>
      )}
    </Loading>
  );
}

function DevicesTab() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["admin", "devices"], queryFn: api.devices });

  return (
    <Loading q={q} empty="还没有成员绑定电脑。" isEmpty={(d) => d.items.length === 0}>
      {(d) => (
        <ul className={listClass}>
          {d.items.map((v) => (
            <li key={v.id} className={rowClass}>
              <div className="min-w-0 flex-1">
                <p className="break-words">
                  <span className="font-mono font-medium">{v.member}</span>
                  <span className="ml-2">{v.name || "未命名"}</span>
                </p>
                <p className="text-xs text-muted">
                  {toolsText(v.tools)} · {absoluteTime(v.created_at)} 添加
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
                    message={`吊销后，${v.member} 的“${v.name || "未命名"}”会立即失去访问权限，需要重新执行 cohive login 并经过审批。`}
                    confirmLabel="确认吊销"
                    action={async () => {
                      await api.revokeDevice(v.id);
                      await qc.invalidateQueries({ queryKey: ["admin"] });
                    }}
                  />
                </>
              )}
            </li>
          ))}
        </ul>
      )}
    </Loading>
  );
}

function inviteState(inv: Invite): { label: string; active: boolean } {
  if (inv.revoked_at) return { label: "已作废", active: false };
  if (new Date(inv.expires_at).getTime() <= Date.now()) return { label: "已过期", active: false };
  if (inv.max_uses && inv.uses >= inv.max_uses) return { label: "已用完", active: false };
  return { label: "有效", active: true };
}

function InvitesTab() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["admin", "invites"], queryFn: api.invites });
  const [created, setCreated] = useState<CreatedInvite | null>(null);

  return (
    <div className="space-y-6">
      <NewInviteForm
        onCreated={async (inv) => {
          setCreated(inv);
          await qc.invalidateQueries({ queryKey: ["admin", "invites"] });
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
                        <span className="text-muted">注册为{roleLabel(inv.role)}</span>
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
                        message="作废后链接立即失效，已经用它注册的成员不受影响。"
                        confirmLabel="确认作废"
                        action={async () => {
                          await api.revokeInvite(inv.id);
                          await qc.invalidateQueries({ queryKey: ["admin", "invites"] });
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
      <Dialog open={!!created} onClose={() => setCreated(null)} title="邀请链接已生成">
        {created && (
          <div className="space-y-3">
            <CopyField label="邀请链接" value={created.url} />
            <Callout>链接只显示这一次，关闭后无法再查看。请现在复制并发给对方；丢了就作废后重新生成。</Callout>
            <div className="flex justify-end">
              <button type="button" onClick={() => setCreated(null)} className={btn.primary}>
                我已复制
              </button>
            </div>
          </div>
        )}
      </Dialog>
    </div>
  );
}

function NewInviteForm({ onCreated }: { onCreated: (inv: CreatedInvite) => Promise<void> }) {
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
    <Section title="生成邀请" description="把链接发给要加入的人，对方打开后自己设成员名和密码。">
      <form onSubmit={submit} className="space-y-4">
        <fieldset>
          <legend className="mb-1 text-sm">邀请类型</legend>
          <div className="flex flex-col gap-2 sm:flex-row">
            {radio("personal", "个人邀请", "一次性，注册后直接可用，无需审批。")}
            {radio("team", "团队邀请", "多人可用，每个注册都需要管理员批准。")}
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
              注册后设为管理员
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

function WorkspaceTab() {
  const q = useQuery({ queryKey: ["admin", "workspace"], queryFn: api.workspace });
  if (q.isPending) return <Notice>正在读取…</Notice>;
  if (q.isError) return <Notice tone="danger">读取失败：{q.error.message}</Notice>;
  return <WorkspaceForm initial={q.data} />;
}

function WorkspaceForm({ initial }: { initial: WorkspaceSettings }) {
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
      await api.saveWorkspace({
        public_url: trimmedUrl,
        telegram_chat: chat.trim(),
        show_title: showTitle,
        show_brief: showBrief,
      });
      setPublicUrl(trimmedUrl);
      setOk("已保存。");
      await qc.invalidateQueries({ queryKey: ["admin", "workspace"] });
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
