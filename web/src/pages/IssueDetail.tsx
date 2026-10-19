import { useId, useState } from "react";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { safeTrans } from "../i18n/safeTrans";
import { Trans, useTranslation } from "react-i18next";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError, type Issue, type IssueEvent } from "../api";
import { btn, ConfirmButton, Dialog, ErrorText, errMsg, Field as FormField, inputClass } from "../ui";
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
                  <Link to="/w/$ws/issues/$id" params={{ ws: w.name, id }} className="text-accent hover:underline">
                    {workspaceLabel(w)}
                  </Link>
                </span>
              ))}
            </span>
          </p>
        )}
        <Link to="/w/$ws" params={{ ws }} className="text-sm text-accent hover:underline">
          {t("detail.backToBoard")}
        </Link>
      </div>
    );
  }

  const { issue: is, events, can_cancel, can_delete } = detail.data;
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
        {(can_cancel || can_delete) && (
          <div className="mt-3 flex flex-wrap gap-2">
            {can_cancel && <CancelButton ws={ws} issue={is} />}
            {can_delete && <DeleteDraftButton ws={ws} id={is.id} />}
          </div>
        )}
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
        {e.action === "cancel" && e.payload?.by_admin === true && <span className="text-muted">{t("event.byAdmin")}</span>}
      </p>
      {e.note && <p className="mt-1 rounded bg-surface px-3 py-2 text-sm whitespace-pre-wrap break-words">{e.note}</p>}
      <time className="mt-0.5 block text-xs text-muted" dateTime={e.at} title={absoluteTime(e.at)}>
        {relativeTime(e.at)}
      </time>
    </li>
  );
}

// 作废：必须写理由。发起人作废自己还没被接单的单；管理员可以作废任何进行中的单（服务端判定，can_cancel 只决定显不显示）
function CancelButton({ ws, issue }: { ws: string; issue: Issue }) {
  const { t } = useTranslation("issues");
  const { t: tc } = useTranslation();
  const qc = useQueryClient();
  const id = useId();
  const [open, setOpen] = useState(false);
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const asReporter = issue.your_actions.includes("cancel");
  const close = () => {
    if (busy) return;
    setOpen(false);
    setError("");
  };
  const run = async () => {
    if (!reason.trim()) {
      setError(t("detail.cancelReasonRequired"));
      return;
    }
    setBusy(true);
    setError("");
    try {
      await api.cancelIssue(ws, issue.id, { expected_version: issue.version, reason });
      setOpen(false);
      setReason("");
      await qc.invalidateQueries({ queryKey: ["issue", ws] });
      await qc.invalidateQueries({ queryKey: ["board", ws] });
    } catch (err) {
      setError(errMsg(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <>
      <button type="button" onClick={() => setOpen(true)} className={btn.danger}>
        {t("detail.cancel")}
      </button>
      <Dialog open={open} onClose={close} title={t("detail.cancelTitle", { id: issue.id })} dismissible={!busy}>
        <p className="mb-3 text-sm text-muted">{asReporter ? t("detail.cancelMessage") : t("detail.cancelMessageAdmin")}</p>
        <FormField label={t("detail.cancelReason")} htmlFor={id}>
          <textarea
            id={id}
            rows={3}
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            placeholder={t("detail.cancelReasonPlaceholder")}
            className={inputClass}
            disabled={busy}
          />
        </FormField>
        <ErrorText>{error}</ErrorText>
        <div className="mt-5 flex justify-end gap-2">
          <button type="button" onClick={close} disabled={busy} className={btn.secondary}>
            {tc("ui.cancel")}
          </button>
          <button type="button" onClick={run} disabled={busy} className={btn.dangerSolid}>
            {busy ? tc("ui.processing") : t("detail.cancelConfirm")}
          </button>
        </div>
      </Dialog>
    </>
  );
}

// 删除自己的草稿：删掉后回到看板
function DeleteDraftButton({ ws, id }: { ws: string; id: number }) {
  const { t } = useTranslation("issues");
  const qc = useQueryClient();
  const navigate = useNavigate();
  return (
    <ConfirmButton
      label={t("detail.deleteDraft")}
      title={t("detail.deleteDraftTitle", { id })}
      message={t("detail.deleteDraftMessage")}
      confirmLabel={t("detail.deleteDraftConfirm")}
      action={async () => {
        await api.deleteDraft(ws, id);
        await qc.invalidateQueries({ queryKey: ["board", ws] });
        await navigate({ to: "/w/$ws", params: { ws } });
      }}
    />
  );
}
