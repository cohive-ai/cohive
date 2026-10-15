import { useEffect, useMemo, useRef, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { api, type BoardIssue, type Member } from "../api";
import { Notice, StatusTag, ToolMark } from "../components";
import { relativeTime } from "../format";

const DONE = "__done__";

// 轮到自己时，用第二人称说明要做什么
const needsYou: Record<string, string> = {
  DRAFT: "草稿，等你确认发出",
  OPEN: "等你接单、退回或转交",
  ACCEPTED: "你已接单，尚未开始修复",
  FIXING: "你正在修复",
  REOPENED: "验证未通过，等你继续修复",
  READY_FOR_VERIFICATION: "已修复，等你验证",
  DECLINED: "被退回了，等你决定下一步",
};

export function BoardPage() {
  const board = useQuery({ queryKey: ["board"], queryFn: api.board, refetchInterval: 5000 });
  const moved = useHandoffs(board.data?.issues);
  const [, tick] = useState(0);
  useEffect(() => {
    const t = setInterval(() => tick((n) => n + 1), 1000);
    return () => clearInterval(t);
  }, []);

  if (board.isPending) return <Notice>正在读取看板…</Notice>;
  if (board.isError) return <Notice tone="danger">读取看板失败：{board.error.message}</Notice>;

  const { me, members, issues } = board.data;
  const columns = orderColumns(me, members, issues);
  const active = issues.filter((i) => i.holder);
  const mine = active.filter((i) => i.holder === me).length;

  return (
    <div>
      <div className="mb-5 flex flex-wrap items-baseline justify-between gap-2">
        <p className="text-base">
          {active.length === 0 ? (
            "现在没有待处理的问题单。"
          ) : (
            <>
              {active.length} 张待处理，
              {mine > 0 ? <span className="text-amber">其中 {mine} 张等你处理</span> : "没有等你处理的"}
            </>
          )}
        </p>
        <p className="text-sm text-muted">每 5 秒自动刷新，上次 {relativeTime(new Date(board.dataUpdatedAt).toISOString())}</p>
      </div>

      <div className="grid gap-4 md:grid-flow-col md:auto-cols-[minmax(280px,1fr)] md:overflow-x-auto md:pb-2">
        {columns.map((col) => (
          <section key={col.key} aria-label={col.title} className="min-w-0">
            <h2 className="mb-2 flex items-baseline gap-2 px-1">
              <span className={col.key === me ? "font-semibold text-ink" : "font-semibold"}>{col.title}</span>
              {col.subtitle && <span className="text-sm text-muted">{col.subtitle}</span>}
              <span className="ml-auto text-sm text-muted">{col.items.length}</span>
            </h2>
            <div className={`flex min-h-24 flex-col gap-2 rounded-lg p-2 ${col.key === DONE ? "bg-sunken/60" : "bg-sunken"}`}>
              {col.items.length === 0 ? (
                <p className="px-2 py-6 text-center text-sm text-muted">
                  {col.key === DONE ? "最近 7 天没有结束的单子" : "手上没有待处理的单子"}
                </p>
              ) : (
                col.items.map((is) => <Card key={is.id} issue={is} me={me} moved={moved.has(is.id)} />)
              )}
            </div>
          </section>
        ))}
      </div>
    </div>
  );
}

function Card({ issue, me, moved }: { issue: BoardIssue; me: string; moved: boolean }) {
  const forMe = issue.holder === me;
  const done = !issue.holder;
  return (
    <Link
      to="/issues/$id"
      params={{ id: String(issue.id) }}
      className={`block rounded-md border bg-surface px-3 py-2.5 hover:border-teal ${
        forMe ? "border-line border-l-4 border-l-amber" : "border-line"
      } ${moved ? "handoff" : ""} ${done ? "opacity-75" : ""}`}
    >
      <div className="flex items-center gap-2 text-sm text-muted">
        <span>#{issue.id}</span>
        <StatusTag status={issue.status} label={issue.status_label} />
        {issue.escalated && <span className="text-xs text-danger">需要人介入</span>}
        {issue.target_service && <span className="ml-auto truncate">{issue.target_service}</span>}
      </div>
      <p className="mt-1 leading-snug text-ink">{issue.title}</p>
      {!done && (
        <p className={`mt-1 text-sm ${forMe ? "text-amber" : "text-muted"}`}>
          {forMe ? (needsYou[issue.status] ?? issue.needs) : issue.needs}
        </p>
      )}
      <div className="mt-2 flex items-center gap-1.5 text-xs text-muted">
        <ToolMark tool={issue.source_tool} withLabel={false} />
        <span>{issue.source}</span>
        {issue.owner && issue.owner !== issue.source && (
          <>
            <span aria-hidden="true">交给</span>
            <span>{issue.owner}</span>
          </>
        )}
        <span className="ml-auto">{relativeTime(issue.updated_at)}</span>
      </div>
    </Link>
  );
}

interface Column {
  key: string;
  title: string;
  subtitle?: string;
  items: BoardIssue[];
}

// 列的顺序：自己在最前，其他成员按名字，最后是已结束
function orderColumns(me: string, members: Member[], issues: BoardIssue[]): Column[] {
  const byHolder = new Map<string, BoardIssue[]>();
  for (const is of issues) {
    const key = is.holder ?? DONE;
    byHolder.set(key, [...(byHolder.get(key) ?? []), is]);
  }
  const sorted = [...members].sort((a, b) =>
    a.name === me ? -1 : b.name === me ? 1 : a.name.localeCompare(b.name),
  );
  const cols: Column[] = sorted.map((m) => ({
    key: m.name,
    title: m.name === me ? "你" : m.display_name || m.name,
    subtitle: m.name === me ? m.display_name || m.name : m.display_name ? m.name : undefined,
    items: byHolder.get(m.name) ?? [],
  }));
  cols.push({ key: DONE, title: "已结束", subtitle: "最近 7 天", items: byHolder.get(DONE) ?? [] });
  return cols;
}

// 记住每张单上一次的处理人；换人时返回该单编号，约 2.5 秒后清除
function useHandoffs(issues: BoardIssue[] | undefined): Set<number> {
  const prev = useRef<Map<number, string> | null>(null);
  const [moved, setMoved] = useState<Set<number>>(new Set());
  const snapshot = useMemo(() => new Map((issues ?? []).map((i) => [i.id, i.holder ?? DONE])), [issues]);

  useEffect(() => {
    if (!issues) return;
    const before = prev.current;
    prev.current = snapshot;
    if (!before) return;
    const changed = new Set<number>();
    for (const [id, holder] of snapshot) {
      const old = before.get(id);
      if (old !== undefined && old !== holder) changed.add(id);
    }
    if (changed.size === 0) return;
    setMoved(changed);
    const t = setTimeout(() => setMoved(new Set()), 2500);
    return () => clearTimeout(t);
  }, [issues, snapshot]);

  return moved;
}
