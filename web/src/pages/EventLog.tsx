import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useInfiniteQuery } from "@tanstack/react-query";
import { api, type AdminEvent, type AdminEventPage, type EventFilter, type Option } from "../api";
import { Notice } from "../components";
import { absoluteTime, relativeTime } from "../format";
import { btn, inputClass } from "../ui";

const listClass = "divide-y divide-line rounded-lg border border-line bg-surface";
const tag = "rounded px-1.5 py-px text-xs";

// 管理操作记录：工作空间管理页（scope.ws）只看本空间，服务器管理页看全部并可按工作空间筛选。
// 按时间倒序，每页 50 条；人话由服务端生成，这里只当纯文本显示。
export function EventLog({ scope }: { scope: { ws: string } | { server: true } }) {
  const { t } = useTranslation("issues");
  const server = "server" in scope;
  const [draft, setDraft] = useState<EventFilter>({});
  const [filter, setFilter] = useState<EventFilter>({});
  const q = useInfiniteQuery<AdminEventPage>({
    queryKey: ["events", server ? "server" : scope.ws, filter],
    queryFn: ({ pageParam }) => {
      const f = { ...filter, before: (pageParam as number | undefined) || undefined };
      return server ? api.serverEvents(f) : api.workspaceEvents(scope.ws, f);
    },
    initialPageParam: undefined,
    getNextPageParam: (last) => last.next_before ?? undefined,
  });
  const first = q.data?.pages[0];
  const items = q.data?.pages.flatMap((p) => p.items) ?? [];
  const apply = (f: EventFilter) => {
    setDraft(f);
    setFilter(f);
  };

  return (
    <div className="space-y-4">
      <p className="text-sm text-muted">
        {server ? t("eventLog.introServer") : t("eventLog.introWorkspace")}
      </p>
      <form
        aria-label={t("eventLog.filterForm")}
        className="flex flex-wrap items-end gap-3"
        onSubmit={(e) => {
          e.preventDefault();
          apply(draft);
        }}
      >
        <label className="text-sm">
          <span className="mb-1 block text-muted">{t("eventLog.type")}</span>
          <select
            aria-label={t("eventLog.type")}
            className={inputClass}
            value={draft.type ?? ""}
            onChange={(e) => apply({ ...draft, type: e.target.value || undefined })}
          >
            <option value="">{t("eventLog.allTypes")}</option>
            {(first?.types ?? []).map((o: Option) => (
              <option key={o.id} value={o.id}>
                {o.label}
              </option>
            ))}
          </select>
        </label>
        <label className="text-sm">
          <span className="mb-1 block text-muted">{t("eventLog.via")}</span>
          <select
            aria-label={t("eventLog.via")}
            className={inputClass}
            value={draft.via ?? ""}
            onChange={(e) => apply({ ...draft, via: e.target.value || undefined })}
          >
            <option value="">{t("eventLog.allVias")}</option>
            {(first?.vias ?? []).map((o: Option) => (
              <option key={o.id} value={o.id}>
                {o.label}
              </option>
            ))}
          </select>
        </label>
        <label className="text-sm">
          <span className="mb-1 block text-muted">{t("eventLog.actor")}</span>
          <input
            aria-label={t("eventLog.actor")}
            className={inputClass}
            placeholder={t("eventLog.actorPlaceholder")}
            value={draft.actor ?? ""}
            onChange={(e) => setDraft({ ...draft, actor: e.target.value })}
          />
        </label>
        {server && (
          <label className="text-sm">
            <span className="mb-1 block text-muted">{t("eventLog.workspace")}</span>
            <input
              aria-label={t("eventLog.workspace")}
              className={inputClass}
              placeholder={t("eventLog.workspacePlaceholder")}
              value={draft.workspace ?? ""}
              onChange={(e) => setDraft({ ...draft, workspace: e.target.value })}
            />
          </label>
        )}
        <button type="submit" className={btn.secondary}>
          {t("eventLog.filter")}
        </button>
      </form>
      {q.isPending ? (
        <Notice>{t("loading")}</Notice>
      ) : q.isError ? (
        <Notice tone="danger">{t("eventLog.loadFailed", { message: q.error.message })}</Notice>
      ) : items.length === 0 ? (
        <Notice>{t("eventLog.empty")}</Notice>
      ) : (
        <>
          <ul className={listClass}>
            {items.map((e) => (
              <EventRow key={e.id} e={e} showWorkspace={server} />
            ))}
          </ul>
          {q.hasNextPage && (
            <button type="button" className={btn.secondary} disabled={q.isFetchingNextPage} onClick={() => q.fetchNextPage()}>
              {q.isFetchingNextPage ? t("loading") : t("eventLog.loadMore")}
            </button>
          )}
        </>
      )}
    </div>
  );
}

function EventRow({ e, showWorkspace }: { e: AdminEvent; showWorkspace: boolean }) {
  const { t } = useTranslation("issues");
  const detail = e.detail ?? [];
  return (
    <li className="px-3 py-3 text-sm">
      <p className="break-words">{e.text}</p>
      <p className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted">
        <span className={`${tag} bg-sunken`}>{e.via_label}</span>
        <span className={`${tag} bg-sunken`}>{e.action_label}</span>
        {showWorkspace && e.workspace && <span className="font-mono">{e.workspace}</span>}
        <span title={relativeTime(e.at)}>{absoluteTime(e.at)}</span>
        <span>#{e.id}</span>
      </p>
      {detail.length > 0 && (
        <details className="mt-1 text-xs">
          <summary className="cursor-pointer text-muted">{t("eventLog.details")}</summary>
          <dl className="mt-1 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1">
            {detail.map((d) => (
              <div key={d.label} className="contents">
                <dt className="text-muted">{d.label}</dt>
                <dd className="min-w-0 break-words">{d.value}</dd>
              </div>
            ))}
          </dl>
        </details>
      )}
    </li>
  );
}
