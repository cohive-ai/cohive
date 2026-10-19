import { useEffect, useMemo, useRef, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { api, type BoardIssue, type Member } from "../api";
import { Notice, StatusTag, ToolMark } from "../components";
import { relativeTime, statusLabel } from "../format";
import { useWs, WorkspaceLoadError } from "./WorkspaceShell";

const DONE = "__done__";

// 轮到自己时，用第二人称说明要做什么；其他人持有时用第三人称（服务端也带了一份，这里按当前语言取）
const needsYouKeys = {
  DRAFT: "board.needsYou.DRAFT",
  OPEN: "board.needsYou.OPEN",
  ACCEPTED: "board.needsYou.ACCEPTED",
  FIXING: "board.needsYou.FIXING",
  REOPENED: "board.needsYou.REOPENED",
  READY_FOR_VERIFICATION: "board.needsYou.READY_FOR_VERIFICATION",
  DECLINED: "board.needsYou.DECLINED",
} as const;

const needsKeys = {
  DRAFT: "board.needs.DRAFT",
  OPEN: "board.needs.OPEN",
  ACCEPTED: "board.needs.ACCEPTED",
  FIXING: "board.needs.FIXING",
  REOPENED: "board.needs.REOPENED",
  READY_FOR_VERIFICATION: "board.needs.READY_FOR_VERIFICATION",
  DECLINED: "board.needs.DECLINED",
  CLOSED: "board.needs.CLOSED",
  CANCELLED: "board.needs.CANCELLED",
} as const;

function needsText(t: TFunction<"issues">, issue: BoardIssue, forMe: boolean): string {
  const key = forMe
    ? needsYouKeys[issue.status as keyof typeof needsYouKeys]
    : needsKeys[issue.status as keyof typeof needsKeys];
  return key ? t(key) : issue.needs;
}

export function BoardPage() {
  const { t } = useTranslation("issues");
  const ws = useWs();
  const board = useQuery({ queryKey: ["board", ws], queryFn: () => api.board(ws), refetchInterval: 5000 });
  const moved = useHandoffs(board.data?.issues);
  // 已作废的单默认隐藏，可以打开开关查看（#12）
  const [showCancelled, setShowCancelled] = useState(false);
  const [, tick] = useState(0);
  useEffect(() => {
    const t = setInterval(() => tick((n) => n + 1), 1000);
    return () => clearInterval(t);
  }, []);

  if (board.isPending) return <Notice>{t("board.loading")}</Notice>;
  if (board.isError) return <WorkspaceLoadError error={board.error} what="board" />;

  const { me, members } = board.data;
  const cancelled = board.data.issues.filter((i) => i.status === "CANCELLED").length;
  const issues = showCancelled ? board.data.issues : board.data.issues.filter((i) => i.status !== "CANCELLED");
  const columns = orderColumns(t, me, members, issues);
  const active = issues.filter((i) => i.holder);
  const mine = active.filter((i) => i.holder === me).length;

  return (
    <div>
      <div className="mb-5 flex flex-wrap items-baseline justify-between gap-2">
        <p className="text-base">
          {active.length === 0 ? (
            t("board.empty")
          ) : mine > 0 ? (
            <Trans
              t={t}
              i18nKey="board.summaryMine"
              count={active.length}
              values={{ mine }}
              components={{ hl: <span className="rounded bg-wait-soft px-1 font-semibold text-wait" /> }}
            />
          ) : (
            t("board.summaryNone", { count: active.length })
          )}
        </p>
        <div className="flex flex-wrap items-baseline gap-4">
          {cancelled > 0 && (
            <label className="inline-flex items-center gap-1.5 text-sm text-muted">
              <input type="checkbox" checked={showCancelled} onChange={(e) => setShowCancelled(e.target.checked)} />
              {t("board.showCancelled", { count: cancelled })}
            </label>
          )}
          <p className="text-sm text-muted">{t("board.refresh", { time: relativeTime(new Date(board.dataUpdatedAt).toISOString()) })}</p>
        </div>
      </div>

      <div className="grid gap-4 md:grid-flow-col md:auto-cols-[minmax(280px,1fr)] md:overflow-x-auto md:pb-2">
        {columns.map((col) => (
          <section key={col.key} aria-label={col.title} className="min-w-0">
            <h2 className="mb-2 flex items-baseline gap-2 px-1">
              <span className={col.key === me ? "font-semibold text-ink" : "font-semibold"}>{col.title}</span>
              {col.subtitle && <span className="text-sm text-muted">{col.subtitle}</span>}
              {col.disabled && (
                <span
                  className="rounded border-[1.5px] border-danger bg-danger px-1.5 py-px text-xs font-bold text-on-status"
                  title={t("board.deactivatedTitle")}
                >
                  {t("board.deactivated")}
                </span>
              )}
              <span className="ml-auto text-sm text-muted">{col.items.length}</span>
            </h2>
            <div className={`flex min-h-24 flex-col gap-2 rounded-lg p-2 ${col.key === DONE ? "bg-sunken/60" : "bg-sunken"}`}>
              {col.items.length === 0 ? (
                <p className="px-2 py-6 text-center text-sm text-muted">
                  {col.key === DONE ? t("board.emptyFinished") : t("board.emptyColumn")}
                </p>
              ) : (
                col.items.map((is) => <Card key={is.id} ws={ws} issue={is} me={me} moved={moved.has(is.id)} />)
              )}
            </div>
          </section>
        ))}
      </div>
    </div>
  );
}

function Card({ ws, issue, me, moved }: { ws: string; issue: BoardIssue; me: string; moved: boolean }) {
  const { t } = useTranslation("issues");
  const forMe = issue.holder === me;
  const done = !issue.holder;
  return (
    <Link
      to="/w/$ws/issues/$id"
      params={{ ws, id: String(issue.id) }}
      className={`block rounded-md border bg-surface px-3 py-2.5 hover:border-accent hover:shadow-hard ${
        forMe ? "border-line border-l-4 border-l-wait-mark" : "border-line"
      } ${moved ? "handoff" : ""}`}
    >
      <div className="flex items-center gap-2 text-sm text-muted">
        <span>#{issue.id}</span>
        <StatusTag compact status={issue.status} label={statusLabel(issue.status, issue.status_label)} />
        {issue.escalated && (
          <span className="rounded border-[1.5px] border-danger bg-danger px-1 text-2xs font-bold text-on-status">
            {t("board.escalated")}
          </span>
        )}
        {issue.target_service && <span className="ml-auto truncate">{issue.target_service}</span>}
      </div>
      {/* 已结束的卡片只淡化标题和底部信息，状态标签不淡化 */}
      <p className={`mt-1 leading-snug text-ink ${done ? "opacity-75" : ""}`}>{issue.title}</p>
      {!done && (
        <p className={`mt-1 text-sm ${forMe ? "text-wait" : "text-muted"}`}>
          {needsText(t, issue, forMe)}
        </p>
      )}
      <div className={`mt-2 flex items-center gap-1.5 text-xs text-muted ${done ? "opacity-75" : ""}`}>
        <ToolMark tool={issue.source_tool} withLabel={false} />
        <span>{issue.source}</span>
        {issue.owner && issue.owner !== issue.source && (
          <>
            <span aria-hidden="true">{t("board.handedTo")}</span>
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
  disabled?: boolean;
  items: BoardIssue[];
}

// 列的顺序：自己在最前，其他成员按名字，最后是已结束
function orderColumns(t: TFunction<"issues">, me: string, members: Member[], issues: BoardIssue[]): Column[] {
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
    title: m.name === me ? t("board.you") : m.display_name || m.name,
    subtitle: m.name === me ? m.display_name || m.name : m.display_name ? m.name : undefined,
    disabled: !!m.disabled,
    items: byHolder.get(m.name) ?? [],
  }));
  cols.push({ key: DONE, title: t("board.finished"), subtitle: t("board.finishedSubtitle"), items: byHolder.get(DONE) ?? [] });
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
