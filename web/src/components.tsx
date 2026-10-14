import { Link, Outlet, useNavigate } from "@tanstack/react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, clearToken } from "./api";
import { toolLabel } from "./format";

// 工具标识：哪个工具做的，是协作记录里的关键信息
export function ToolMark({ tool, withLabel = true }: { tool: string; withLabel?: boolean }) {
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
  const me = useQuery({ queryKey: ["me"], queryFn: () => api.me() });
  const qc = useQueryClient();
  const navigate = useNavigate();
  const signOut = () => {
    clearToken();
    qc.clear();
    navigate({ to: "/login" });
  };
  const nav = "px-1 py-0.5 text-muted hover:text-ink";
  const active = { className: "px-1 py-0.5 text-ink border-b-2 border-teal" };

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
          <nav className="flex gap-4 text-sm">
            <Link to="/" className={nav} activeProps={active} activeOptions={{ exact: true }}>
              谁手上有球
            </Link>
            <Link to="/issues" className={nav} activeProps={active} activeOptions={{ exact: true }}>
              全部问题单
            </Link>
          </nav>
          <div className="ml-auto flex items-center gap-3 text-sm">
            {me.data && (
              <span className="flex items-center gap-2 text-muted">
                以 <span className="text-ink">{me.data.member}</span> 身份查看
                <ToolMark tool={me.data.tool} withLabel={false} />
              </span>
            )}
            <button onClick={signOut} className="rounded px-2 py-1 text-muted hover:bg-sunken hover:text-ink">
              退出
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

export function Notice({ children, tone = "muted" }: { children: React.ReactNode; tone?: "muted" | "danger" }) {
  return (
    <p className={`rounded border border-line bg-surface px-4 py-3 text-sm ${tone === "danger" ? "text-danger" : "text-muted"}`}>
      {children}
    </p>
  );
}
