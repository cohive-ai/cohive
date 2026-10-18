import { Link, useParams } from "@tanstack/react-router";
import { safeTrans } from "../i18n/safeTrans";
import { Trans, useTranslation } from "react-i18next";
import { useQuery } from "@tanstack/react-query";
import { api, ApiError, type IssueEvent } from "../api";
import { Notice, StatusTag, ToolMark } from "../components";
import {
  absoluteTime,
  actionLabel,
  listSeparator,
  relativeTime,
  severityLabel,
  statusLabel,
  toolLabel,
  workspaceLabel,
} from "../format";
import { WorkspaceLoadError } from "./WorkspaceShell";

export function IssueDetailPage() {
  const { t } = useTranslation("issues");
  const { ws, id } = useParams({ from: "/layout/w/$ws/issues/$id" });
  const me = useQuery({ queryKey: ["me"], queryFn: api.me });
  const detail = useQuery({
    queryKey: ["issue", ws, id],
    queryFn: () => api.issue(ws, Number(id)),
    refetchInterval: 5000,
  });

  if (detail.isPending) return <Notice>{t("detail.loading")}</Notice>;
  if (detail.isError) {
    const member = !!me.data?.workspaces.some((w) => w.name === ws);
    // 是成员但查不到：多半是这张单属于别的工作空间（例如旧链接跳到了上次用的工作空间）
    if (!(detail.error instanceof ApiError && detail.error.status === 404) || !member) {
      return <WorkspaceLoadError error={detail.error} what="issues" />;
    }
    const others = (me.data?.workspaces ?? []).filter((w) => w.name !== ws);
    return (
      <div className="space-y-3">
        <Notice tone="danger">{t("detail.notInWorkspace", { ws, id })}</Notice>
        {others.length > 0 && (
          <p className="text-sm text-muted">
            {t("detail.maybeElsewhere")}
            <span className="ml-1">
              {others.map((w, i) => (
                <span key={w.name}>
                  {i > 0 && listSeparator()}
                  <Link to="/w/$ws/issues/$id" params={{ ws: w.name, id }} className="text-teal hover:underline">
                    {workspaceLabel(w)}
                  </Link>
                </span>
              ))}
            </span>
          </p>
        )}
        <Link to="/w/$ws" params={{ ws }} className="text-sm text-teal">
          {t("detail.backToBoard")}
        </Link>
      </div>
    );
  }

  const { issue: is, events } = detail.data;
  const unset = <span className="text-muted">{t("unset")}</span>;
  const meta: [string, React.ReactNode][] = [
    [t("detail.reporter"), <span className="inline-flex items-center gap-1.5">{is.source}<ToolMark tool={is.source_tool} /></span>],
    [t("detail.owner"), is.owner || unset],
    [t("detail.service"), is.target_service || unset],
    [t("detail.severity"), severityLabel(is.severity)],
    [
      t("detail.codeLocation"),
      [is.repo, is.branch, is.commit].filter(Boolean).join(" / ") || <span className="text-muted">{t("detail.notProvided")}</span>,
    ],
    [t("detail.version"), t("detail.versionValue", { version: is.version })],
    [t("detail.reopenCount"), String(is.reopen_count)],
    [t("detail.created"), absoluteTime(is.created_at)],
  ];

  return (
    <article>
      <Link to="/w/$ws" params={{ ws }} className="text-sm text-muted hover:text-ink">
        {t("detail.backToBoard")}
      </Link>
      <header className="mt-2 mb-6">
        <div className="flex flex-wrap items-center gap-2 text-sm text-muted">
          <span>#{is.id}</span>
          <StatusTag status={is.status} label={statusLabel(is.status, is.status_label)} />
          {is.escalated && <span className="text-danger">{t("detail.escalatedWhy", { reason: is.escalation_reason })}</span>}
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

          <Field title={t("detail.summary")} text={is.summary} />
          <div className="grid gap-4 sm:grid-cols-2">
            <Field title={t("detail.expected")} text={is.expected} />
            <Field title={t("detail.actual")} text={is.actual} />
          </div>

          <section>
            <h2 className="mb-2 font-semibold">{t("detail.repro")}</h2>
            {is.repro.script ? (
              <div className="rounded-lg border border-line bg-surface">
                <pre className="overflow-x-auto p-4 font-mono text-sm leading-relaxed whitespace-pre-wrap">{is.repro.script}</pre>
                <div className="flex flex-wrap gap-x-6 gap-y-1 border-t border-line px-4 py-2 text-sm text-muted">
                  <span>{t("detail.reproEnv", { env: is.repro.env || t("detail.notStated") })}</span>
                  <span>{t("detail.reproExit", { code: is.repro.expected_exit_code ?? t("detail.notStated") })}</span>
                </div>
              </div>
            ) : (
              <p className="text-sm text-muted">{t("detail.noRepro")}</p>
            )}
          </section>

          <Field title={t("detail.evidence")} text={is.evidence} mono />
        </div>

        <section aria-label={t("detail.activity")}>
          <h2 className="mb-3 font-semibold">{t("detail.activity")}</h2>
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
  const { t } = useTranslation("issues");
  const dot = e.tool === "claude-code" ? "bg-tool-claude" : e.tool === "codex" ? "bg-tool-codex" : "bg-muted";
  const assignee = typeof e.payload?.assignee === "string" ? e.payload.assignee : undefined;
  // 管理员移出成员时的改派：负责人或发起人换成谁
  const ownerTo = typeof e.payload?.owner_to_name === "string" ? e.payload.owner_to_name : undefined;
  const sourceTo = typeof e.payload?.source_to_name === "string" ? e.payload.source_to_name : undefined;
  const action = actionLabel(e.action);
  return (
    <li className="relative">
      <span className={`absolute top-1.5 -left-[25px] h-2.5 w-2.5 rounded-full ring-4 ring-bg ${dot}`} aria-hidden="true" />
      <p className="text-sm">
        <Trans
          {...safeTrans}
          t={t}
          i18nKey="event.head"
          values={{ actor: e.actor, tool: toolLabel(e.tool), action: assignee ? t("event.actionTo", { action, name: assignee }) : action }}
          components={{ actor: <span className="font-medium" />, tool: <span className="text-muted" /> }}
        />
        {ownerTo && t("event.ownerTo", { name: ownerTo })}
        {sourceTo && t("event.sourceTo", { name: sourceTo })}
        {e.to && e.from && e.from !== e.to && (
          <span className="text-muted">{t("event.statusTo", { status: statusLabel(e.to) })}</span>
        )}
        {e.payload?.escalated === true && <span className="text-danger">{t("event.escalated")}</span>}
      </p>
      {e.note && <p className="mt-1 rounded bg-surface px-3 py-2 text-sm whitespace-pre-wrap break-words">{e.note}</p>}
      <time className="mt-0.5 block text-xs text-muted" dateTime={e.at} title={absoluteTime(e.at)}>
        {relativeTime(e.at)}
      </time>
    </li>
  );
}
