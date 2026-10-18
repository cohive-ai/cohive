import { useEffect, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { Link, Outlet, useNavigate, useRouter, useRouterState } from "@tanstack/react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type DeviceToken, type WorkspaceRef } from "./api";
import { joinList, roleLabel, tokensByWorkspace, toolLabel, workspaceLabel } from "./format";
import { applyAccountLanguage } from "./i18n";
import { btn } from "./ui";
import {
  canAdminWorkspace,
  pickWorkspace,
  readLastWorkspace,
  switchWorkspacePath,
  workspaceFromPath,
} from "./workspace";

// 工具标识：哪个工具做的，是协作记录里的关键信息
export function ToolMark({ tool, withLabel = true }: { tool: string; withLabel?: boolean }) {
  useTranslation(); // 切换语言时重绘（“网页”的名称随语言变）
  const color =
    tool === "claude-code" ? "text-tool-claude" : tool === "codex" ? "text-tool-codex" : "text-muted";
  return (
    <span className={`inline-flex items-center gap-1 ${color}`} title={toolLabel(tool)}>
      <svg viewBox="0 0 12 12" className="h-2.5 w-2.5 shrink-0" aria-hidden="true">
        <path d="M6 0.5l5 2.9v5.2L6 11.5 1 8.6V3.4z" fill="currentColor" />
      </svg>
      {withLabel && <span className="text-xs">{toolLabel(tool)}</span>}
    </span>
  );
}

const statusTone: Record<string, string> = {
  DRAFT: "border border-dashed border-line text-muted",
  OPEN: "bg-teal-soft text-teal",
  ACCEPTED: "bg-teal-soft text-teal",
  FIXING: "bg-teal-soft text-teal",
  REOPENED: "bg-amber-soft text-amber",
  READY_FOR_VERIFICATION: "bg-amber-soft text-amber",
  CLOSED: "bg-sunken text-muted",
  DECLINED: "bg-sunken text-danger",
};

export function StatusTag({ status, label }: { status: string; label: string }) {
  return (
    <span className={`inline-block rounded px-1.5 py-px text-xs whitespace-nowrap ${statusTone[status] ?? ""}`}>
      {label}
    </span>
  );
}

export function Layout() {
  const { t } = useTranslation();
  const me = useQuery({ queryKey: ["me"], queryFn: api.me });
  const qc = useQueryClient();
  const navigate = useNavigate();
  const router = useRouter();
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const [leaving, setLeaving] = useState(false);
  // 账号设了语言就以它为准（写本机）；只在账号语言本身变化时比对
  const accountLanguage = me.data?.account.language;
  useEffect(() => applyAccountLanguage(accountLanguage), [accountLanguage]);
  const signOut = async () => {
    setLeaving(true);
    try {
      await api.logout();
    } catch {
      // 退出失败（例如会话早已失效）也照样回到登录页
    }
    qc.clear();
    navigate({ to: "/login" });
  };
  const nav = "px-1 py-0.5 text-muted hover:text-ink";
  const active = { className: "px-1 py-0.5 text-ink border-b-2 border-teal" };

  const workspaces = me.data?.workspaces ?? [];
  const current = workspaceFromPath(pathname);
  // 导航里看板、问题单指向的工作空间：地址里的，否则上次用的或第一个
  const navWs = current ?? pickWorkspace(workspaces, readLastWorkspace());
  const showAdmin = !!navWs && canAdminWorkspace(me.data, navWs);
  const switchTo = (ws: string) => {
    if (!ws || ws === current) return;
    router.history.push(switchWorkspacePath(pathname, ws, canAdminWorkspace(me.data, ws)));
  };

  return (
    <div className="min-h-dvh">
      <header className="border-b border-line bg-surface">
        <div className="mx-auto flex max-w-[1400px] flex-wrap items-center gap-x-6 gap-y-2 px-4 py-3">
          <Link to="/" className="flex items-center gap-2 text-ink">
            <svg viewBox="0 0 32 32" className="h-5 w-5 text-teal" aria-hidden="true">
              <path d="M16 3l11 6.5v13L16 29 5 22.5v-13z" fill="currentColor" />
            </svg>
            <span className="font-semibold tracking-wide">Cohive</span>
          </Link>
          {workspaces.length > 0 || current ? (
            <WorkspaceSwitcher workspaces={workspaces} current={current} onChange={switchTo} />
          ) : null}
          <nav className="flex flex-wrap gap-x-4 gap-y-1 text-sm">
            {navWs && (
              <>
                <Link
                  to="/w/$ws"
                  params={{ ws: navWs }}
                  className={nav}
                  activeProps={active}
                  activeOptions={{ exact: true }}
                >
                  {t("nav.board")}
                </Link>
                <Link to="/w/$ws/issues" params={{ ws: navWs }} className={nav} activeProps={active}>
                  {t("nav.issues")}
                </Link>
                {showAdmin && (
                  <Link to="/w/$ws/admin" params={{ ws: navWs }} className={nav} activeProps={active}>
                    {t("nav.admin")}
                  </Link>
                )}
              </>
            )}
            <Link to="/me" className={nav} activeProps={active}>
              {t("nav.profile")}
            </Link>
            {me.data?.account.server_admin && (
              <Link to="/server" className={nav} activeProps={active}>
                {t("nav.serverAdmin")}
              </Link>
            )}
          </nav>
          <div className="ml-auto flex items-center gap-3 text-sm">
            {me.data && (
              <span className="flex min-w-0 items-center gap-2 text-muted">
                <span className="truncate text-ink">{me.data.account.display_name || me.data.account.name}</span>
                <ToolMark tool={me.data.tool} withLabel={false} />
              </span>
            )}
            <button
              onClick={signOut}
              disabled={leaving}
              className="rounded px-2 py-1 text-muted hover:bg-sunken hover:text-ink disabled:opacity-50"
            >
              {leaving ? t("session.signingOut") : t("session.signOut")}
            </button>
          </div>
        </div>
      </header>
      <main className="mx-auto max-w-[1400px] px-4 py-6">
        <Outlet />
      </main>
    </div>
  );
}

// 顶栏的工作空间切换：列出所在的工作空间并标出角色
function WorkspaceSwitcher({
  workspaces,
  current,
  onChange,
}: {
  workspaces: WorkspaceRef[];
  current: string | null;
  onChange: (ws: string) => void;
}) {
  const { t } = useTranslation();
  const known = !current || workspaces.some((w) => w.name === current);
  return (
    <label className="flex items-center gap-1 text-sm">
      <span className="sr-only">{t("workspace.switch")}</span>
      <select
        value={current ?? ""}
        onChange={(e) => onChange(e.target.value)}
        className="max-w-[14rem] rounded border border-line bg-bg px-2 py-1 text-sm text-ink"
      >
        {!current && (
          <option value="" disabled>
            {t("workspace.choose")}
          </option>
        )}
        {!known && current && <option value={current}>{t("workspace.notMember", { name: current })}</option>}
        {workspaces.map((w) => (
          <option key={w.name} value={w.name}>
            {workspaceLabel(w)} · {roleLabel(w.role)}
          </option>
        ))}
      </select>
    </label>
  );
}

// 一个工作空间都没有时的提示页
export function NoWorkspace() {
  const { t } = useTranslation();
  const me = useQuery({ queryKey: ["me"], queryFn: api.me });
  return (
    <div className="mx-auto max-w-xl space-y-3">
      <Notice>{t("workspace.noneJoined")}</Notice>
      {me.data?.account.server_admin && (
        <p className="text-sm">
          <Trans
            t={t}
            i18nKey="workspace.createHint"
            components={{ create: <Link to="/server" className="text-teal hover:underline" /> }}
          />
        </p>
      )}
    </div>
  );
}

// 需要账号密码登录的网页会话：说明原因，并提供退出后用账号密码登录（登录后回到 redirectTo）的入口。
export function NeedPasswordLogin({ message, redirectTo }: { message: React.ReactNode; redirectTo: string }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [busy, setBusy] = useState(false);
  const relogin = async () => {
    setBusy(true);
    try {
      await api.logout();
    } catch {
      // 退出失败也照样去登录页
    }
    qc.clear();
    navigate({ to: "/login", search: { redirect: redirectTo } });
  };
  return (
    <div className="mx-auto max-w-xl space-y-3">
      <Notice tone="danger">{message}</Notice>
      <button type="button" onClick={relogin} disabled={busy} className={btn.primary}>
        {busy ? t("session.signingOut") : t("session.signOutAndUsePassword")}
      </button>
    </div>
  );
}

export function Notice({ children, tone = "muted" }: { children: React.ReactNode; tone?: "muted" | "danger" }) {
  return (
    <p className={`rounded border border-line bg-surface px-4 py-3 text-sm ${tone === "danger" ? "text-danger" : "text-muted"}`}>
      {children}
    </p>
  );
}

// 设备上的令牌按工作空间列出：pay：Claude Code、Codex
export function DeviceTokens({ tokens, revoked }: { tokens: DeviceToken[] | null | undefined; revoked: boolean }) {
  const { t } = useTranslation();
  const groups = tokensByWorkspace(tokens);
  if (groups.length === 0) {
    return <p className="text-xs text-muted">{revoked ? t("deviceTokens.allRevoked") : t("deviceTokens.none")}</p>;
  }
  return (
    <ul className="mt-0.5 space-y-0.5 text-xs">
      {groups.map((g) => (
        <li key={g.workspace}>
          <span className="font-medium">{g.workspace}</span>
          <span className="text-muted">{t("deviceTokens.tools", { tools: joinList(g.tools) })}</span>
        </li>
      ))}
    </ul>
  );
}
