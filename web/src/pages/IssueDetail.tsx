import { Link, useParams } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { api, ApiError, type IssueEvent } from "../api";
import { Notice, StatusTag, ToolMark } from "../components";
import { absoluteTime, actionLabel, relativeTime, severityLabel, statusLabel, toolLabel, workspaceLabel } from "../format";
import { WorkspaceLoadError } from "./WorkspaceShell";

export function IssueDetailPage() {
  const { ws, id } = useParams({ from: "/layout/w/$ws/issues/$id" });
  const me = useQuery({ queryKey: ["me"], queryFn: api.me });
  const detail = useQuery({
    queryKey: ["issue", ws, id],
    queryFn: () => api.issue(ws, Number(id)),
    refetchInterval: 5000,
  });

  if (detail.isPending) return <Notice>正在读取问题单…</Notice>;
  if (detail.isError) {
    const member = !!me.data?.workspaces.some((w) => w.name === ws);
    // 是成员但查不到：多半是这张单属于别的工作空间（例如旧链接跳到了上次用的工作空间）
    if (!(detail.error instanceof ApiError && detail.error.status === 404) || !member) {
      return <WorkspaceLoadError error={detail.error} what="问题单" />;
    }
    const others = (me.data?.workspaces ?? []).filter((w) => w.name !== ws);
    return (
      <div className="space-y-3">
        <Notice tone="danger">
          工作空间“{ws}”里没有问题单 #{id}。
        </Notice>
        {others.length > 0 && (
          <p className="text-sm text-muted">
            它可能在你所在的其他工作空间里：
            {others.map((w, i) => (
              <span key={w.name}>
                {i > 0 && "、"}
                <Link to="/w/$ws/issues/$id" params={{ ws: w.name, id }} className="text-teal hover:underline">
                  {workspaceLabel(w)}
                </Link>
              </span>
            ))}
          </p>
        )}
        <Link to="/w/$ws" params={{ ws }} className="text-sm text-teal">
          回到看板
        </Link>
      </div>
    );
  }

  const { issue: is, events } = detail.data;
  const meta: [string, React.ReactNode][] = [
    ["发起人", <span className="inline-flex items-center gap-1.5">{is.source}<ToolMark tool={is.source_tool} /></span>],
    ["负责人", is.owner || <span className="text-muted">未指定</span>],
    ["服务", is.target_service || <span className="text-muted">未指定</span>],
    ["严重程度", severityLabel[is.severity] ?? is.severity],
    ["代码位置", [is.repo, is.branch, is.commit].filter(Boolean).join(" / ") || <span className="text-muted">未提供</span>],
    ["版本", `第 ${is.version} 版`],
    ["重开次数", String(is.reopen_count)],
    ["创建", absoluteTime(is.created_at)],
  ];

  return (
    <article>
      <Link to="/w/$ws" params={{ ws }} className="text-sm text-muted hover:text-ink">
        回到看板
      </Link>
      <header className="mt-2 mb-6">
        <div className="flex flex-wrap items-center gap-2 text-sm text-muted">
          <span>#{is.id}</span>
          <StatusTag status={is.status} label={is.status_label} />
          {is.escalated && <span className="text-danger">需要人介入：{is.escalation_reason}</span>}
        </div>
        <h1 className="mt-1 text-2xl leading-snug font-semibold">{is.title}</h1>
      </header>

      <div className="grid gap-8 lg:grid-cols-[minmax(0,1fr)_380px]">
        <div className="min-w-0 space-y-6">
          <dl className="grid grid-cols-[6em_1fr] gap-x-4 gap-y-1.5 rounded-lg border border-line bg-surface p-4 text-sm sm:grid-cols-[6em_1fr_6em_1fr]">
            {meta.map(([k, v]) => (
              <div key={k} className="contents">
                <dt className="text-muted">{k}</dt>
                <dd className="min-w-0 break-words">{v}</dd>
              </div>
            ))}
          </dl>

          <Field title="概述" text={is.summary} />
          <div className="grid gap-4 sm:grid-cols-2">
            <Field title="期望结果" text={is.expected} />
            <Field title="实际结果" text={is.actual} />
          </div>

          <section>
            <h2 className="mb-2 font-semibold">复现脚本</h2>
            {is.repro.script ? (
              <div className="rounded-lg border border-line bg-surface">
                <pre className="overflow-x-auto p-4 font-mono text-sm leading-relaxed whitespace-pre-wrap">{is.repro.script}</pre>
                <div className="flex flex-wrap gap-x-6 gap-y-1 border-t border-line px-4 py-2 text-sm text-muted">
                  <span>运行环境：{is.repro.env || "未说明"}</span>
                  <span>
                    修复后应返回：{is.repro.expected_exit_code ?? "未说明"}
                  </span>
                </div>
              </div>
            ) : (
              <p className="text-sm text-muted">发起人没有提供复现脚本，验证只能靠人工确认。</p>
            )}
          </section>

          <Field title="证据" text={is.evidence} mono />
        </div>

        <section aria-label="动态">
          <h2 className="mb-3 font-semibold">动态</h2>
          <ol className="relative space-y-4 border-l border-line pl-5">
            {events.map((e, i) => (
              <Event key={i} e={e} />
            ))}
          </ol>
        </section>
      </div>
    </article>
  );
}

function Field({ title, text, mono }: { title: string; text?: string; mono?: boolean }) {
  if (!text) return null;
  return (
    <section>
      <h2 className="mb-1 font-semibold">{title}</h2>
      <p className={`max-w-[72ch] whitespace-pre-wrap break-words ${mono ? "font-mono text-sm" : ""}`}>{text}</p>
    </section>
  );
}

function Event({ e }: { e: IssueEvent }) {
  const dot = e.tool === "claude-code" ? "bg-tool-claude" : e.tool === "codex" ? "bg-tool-codex" : "bg-muted";
  const assignee = typeof e.payload?.assignee === "string" ? e.payload.assignee : undefined;
  // 管理员移出成员时的改派：负责人或发起人换成谁
  const ownerTo = typeof e.payload?.owner_to_name === "string" ? e.payload.owner_to_name : undefined;
  const sourceTo = typeof e.payload?.source_to_name === "string" ? e.payload.source_to_name : undefined;
  return (
    <li className="relative">
      <span className={`absolute top-1.5 -left-[25px] h-2.5 w-2.5 rounded-full ring-4 ring-bg ${dot}`} aria-hidden="true" />
      <p className="text-sm">
        <span className="font-medium">{e.actor}</span>
        <span className="text-muted">（{toolLabel(e.tool)}）</span>
        {actionLabel[e.action] ?? e.action}
        {assignee && <>给 {assignee}</>}
        {ownerTo && <>，负责人改为 {ownerTo}</>}
        {sourceTo && <>，发起人改为 {sourceTo}</>}
        {e.to && e.from && e.from !== e.to && <span className="text-muted">，状态变为{statusLabel[e.to] ?? e.to}</span>}
        {e.payload?.escalated === true && <span className="text-danger">，已标记需要人介入</span>}
      </p>
      {e.note && <p className="mt-1 rounded bg-surface px-3 py-2 text-sm whitespace-pre-wrap break-words">{e.note}</p>}
      <time className="mt-0.5 block text-xs text-muted" dateTime={e.at} title={absoluteTime(e.at)}>
        {relativeTime(e.at)}
      </time>
    </li>
  );
}
