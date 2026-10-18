import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Link } from "@tanstack/react-router";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { api } from "../api";
import { Notice, StatusTag, ToolMark } from "../components";
import { relativeTime, statusLabel, statusOptions } from "../format";
import { useWs, WorkspaceLoadError } from "./WorkspaceShell";

export function IssuesPage() {
  const { t } = useTranslation("issues");
  const ws = useWs();
  const [q, setQ] = useState("");
  const [status, setStatus] = useState("");
  const list = useQuery({
    queryKey: ["issues", ws, q, status],
    queryFn: () => api.issues(ws, { q, status }),
    placeholderData: keepPreviousData,
    refetchInterval: 5000,
  });

  return (
    <div>
      <div className="mb-4 flex flex-wrap gap-3">
        <label className="sr-only" htmlFor="q">
          {t("list.searchLabel")}
        </label>
        <input
          id="q"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder={t("list.searchLabel")}
          className="min-w-0 flex-1 rounded border border-line bg-surface px-3 py-2 text-sm placeholder:text-muted sm:max-w-sm"
        />
        <label className="sr-only" htmlFor="status">
          {t("list.filterStatus")}
        </label>
        <select
          id="status"
          value={status}
          onChange={(e) => setStatus(e.target.value)}
          className="rounded border border-line bg-surface px-3 py-2 text-sm"
        >
          {/* “全部状态”不含已作废，选“已作废”才列出 */}
          {statusOptions().map((o) => (
            <option key={o.value} value={o.value}>
              {o.value === "" ? t("list.allStatuses") : o.label}
            </option>
          ))}
        </select>
      </div>

      {list.isPending ? (
        <Notice>{t("loading")}</Notice>
      ) : list.isError ? (
        <WorkspaceLoadError error={list.error} what="issues" />
      ) : list.data.items.length === 0 ? (
        <Notice>{q || status ? t("list.emptyFiltered") : t("list.empty")}</Notice>
      ) : (
        <div className="overflow-hidden rounded-lg border border-line bg-surface">
          <table className="w-full text-sm">
            <thead className="hidden bg-sunken text-left text-muted sm:table-header-group">
              <tr>
                <th className="px-3 py-2 font-normal">{t("list.col.id")}</th>
                <th className="px-3 py-2 font-normal">{t("list.col.title")}</th>
                <th className="px-3 py-2 font-normal">{t("list.col.status")}</th>
                <th className="px-3 py-2 font-normal">{t("list.col.service")}</th>
                <th className="px-3 py-2 font-normal">{t("list.col.reporter")}</th>
                <th className="px-3 py-2 font-normal">{t("list.col.owner")}</th>
                <th className="px-3 py-2 font-normal">{t("list.col.updated")}</th>
              </tr>
            </thead>
            <tbody>
              {list.data.items.map((is) => (
                <tr key={is.id} className="border-t border-line first:border-t-0 hover:bg-bg">
                  <td className="hidden px-3 py-2 align-top text-muted sm:table-cell">#{is.id}</td>
                  <td className="px-3 py-2">
                    <Link to="/w/$ws/issues/$id" params={{ ws, id: String(is.id) }} className="text-ink hover:text-teal">
                      <span className="text-muted sm:hidden">#{is.id} </span>
                      {is.title}
                    </Link>
                    {/* 窄屏时把其余列合成一行带说明的摘要 */}
                    <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted sm:hidden">
                      <StatusTag status={is.status} label={statusLabel(is.status, is.status_label)} />
                      <span className="inline-flex items-center gap-1">
                        <ToolMark tool={is.source_tool} withLabel={false} />
                        {t("list.reportedBy", { name: is.source })}
                      </span>
                      <span>{t("list.ownerLine", { name: is.owner || t("unset") })}</span>
                      {is.target_service && <span>{is.target_service}</span>}
                      <span>{relativeTime(is.updated_at)}</span>
                    </div>
                  </td>
                  <td className="hidden px-3 py-2 sm:table-cell">
                    <StatusTag status={is.status} label={statusLabel(is.status, is.status_label)} />
                  </td>
                  <td className="hidden px-3 py-2 text-muted sm:table-cell">{is.target_service || t("unset")}</td>
                  <td className="hidden px-3 py-2 sm:table-cell">
                    <span className="inline-flex items-center gap-1.5">
                      <ToolMark tool={is.source_tool} withLabel={false} />
                      {is.source}
                    </span>
                  </td>
                  <td className="hidden px-3 py-2 sm:table-cell">{is.owner || <span className="text-muted">{t("unset")}</span>}</td>
                  <td className="hidden px-3 py-2 text-muted sm:table-cell">{relativeTime(is.updated_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          {list.data.items.length >= 50 && (
            <p className="border-t border-line px-3 py-2 text-sm text-muted">{t("list.limit")}</p>
          )}
        </div>
      )}
    </div>
  );
}
