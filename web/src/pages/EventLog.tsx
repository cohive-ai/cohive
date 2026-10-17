import { useState } from "react";
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
        {server
          ? "服务器上全部管理操作：审批、角色、邀请、设备、成员、账号、工作空间，以及被拒的管理请求。只追加，不能修改或删除。"
          : "本工作空间的管理操作：审批、角色、邀请、设备、成员与设置。停用账号、在服务器管理页批准设备等服务器级操作不在这里，请问服务器管理员。只追加，不能修改或删除。"}
      </p>
      <form
        aria-label="筛选记录"
        className="flex flex-wrap items-end gap-3"
        onSubmit={(e) => {
          e.preventDefault();
          apply(draft);
        }}
      >
        <label className="text-sm">
          <span className="mb-1 block text-muted">类型</span>
          <select
            aria-label="类型"
            className={inputClass}
            value={draft.type ?? ""}
            onChange={(e) => apply({ ...draft, type: e.target.value || undefined })}
          >
            <option value="">全部类型</option>
            {(first?.types ?? []).map((o: Option) => (
              <option key={o.id} value={o.id}>
                {o.label}
              </option>
            ))}
          </select>
        </label>
        <label className="text-sm">
          <span className="mb-1 block text-muted">入口</span>
          <select
            aria-label="入口"
            className={inputClass}
            value={draft.via ?? ""}
            onChange={(e) => apply({ ...draft, via: e.target.value || undefined })}
          >
            <option value="">全部入口</option>
            {(first?.vias ?? []).map((o: Option) => (
              <option key={o.id} value={o.id}>
                {o.label}
              </option>
            ))}
          </select>
        </label>
        <label className="text-sm">
          <span className="mb-1 block text-muted">操作人</span>
          <input
            aria-label="操作人"
            className={inputClass}
            placeholder="登录名"
            value={draft.actor ?? ""}
            onChange={(e) => setDraft({ ...draft, actor: e.target.value })}
          />
        </label>
        {server && (
          <label className="text-sm">
            <span className="mb-1 block text-muted">工作空间</span>
            <input
              aria-label="工作空间"
              className={inputClass}
              placeholder="名称（含已删除的）"
              value={draft.workspace ?? ""}
              onChange={(e) => setDraft({ ...draft, workspace: e.target.value })}
            />
          </label>
        )}
        <button type="submit" className={btn.secondary}>
          筛选
        </button>
      </form>
      {q.isPending ? (
        <Notice>正在读取…</Notice>
      ) : q.isError ? (
        <Notice tone="danger">读取失败：{q.error.message}</Notice>
      ) : items.length === 0 ? (
        <Notice>还没有记录。</Notice>
      ) : (
        <>
          <ul className={listClass}>
            {items.map((e) => (
              <EventRow key={e.id} e={e} showWorkspace={server} />
            ))}
          </ul>
          {q.hasNextPage && (
            <button type="button" className={btn.secondary} disabled={q.isFetchingNextPage} onClick={() => q.fetchNextPage()}>
              {q.isFetchingNextPage ? "正在读取…" : "加载更早的记录"}
            </button>
          )}
        </>
      )}
    </div>
  );
}

function EventRow({ e, showWorkspace }: { e: AdminEvent; showWorkspace: boolean }) {
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
          <summary className="cursor-pointer text-muted">详情</summary>
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
