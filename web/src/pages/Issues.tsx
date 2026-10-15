import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { api } from "../api";
import { Notice, StatusTag, ToolMark } from "../components";
import { relativeTime, statusOptions } from "../format";

export function IssuesPage() {
  const [q, setQ] = useState("");
  const [status, setStatus] = useState("");
  const list = useQuery({
    queryKey: ["issues", q, status],
    queryFn: () => api.issues({ q, status }),
    placeholderData: keepPreviousData,
    refetchInterval: 5000,
  });

  return (
    <div>
      <div className="mb-4 flex flex-wrap gap-3">
        <label className="sr-only" htmlFor="q">
          搜索标题、概述或实际结果
        </label>
        <input
          id="q"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="搜索标题、概述或实际结果"
          className="min-w-0 flex-1 rounded border border-line bg-surface px-3 py-2 text-sm placeholder:text-muted sm:max-w-sm"
        />
        <label className="sr-only" htmlFor="status">
          按状态筛选
        </label>
        <select
          id="status"
          value={status}
          onChange={(e) => setStatus(e.target.value)}
          className="rounded border border-line bg-surface px-3 py-2 text-sm"
        >
          {statusOptions.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
      </div>

      {list.isPending ? (
        <Notice>正在读取…</Notice>
      ) : list.isError ? (
        <Notice tone="danger">读取失败：{list.error.message}</Notice>
      ) : list.data.items.length === 0 ? (
        <Notice>{q || status ? "没有符合条件的问题单，换个关键词或状态试试。" : "还没有问题单。在编程工具里让智能体起草第一张。"}</Notice>
      ) : (
        <div className="overflow-hidden rounded-lg border border-line bg-surface">
          <table className="w-full text-sm">
            <thead className="hidden bg-sunken text-left text-muted sm:table-header-group">
              <tr>
                <th className="px-3 py-2 font-normal">编号</th>
                <th className="px-3 py-2 font-normal">标题</th>
                <th className="px-3 py-2 font-normal">状态</th>
                <th className="px-3 py-2 font-normal">服务</th>
                <th className="px-3 py-2 font-normal">发起人</th>
                <th className="px-3 py-2 font-normal">负责人</th>
                <th className="px-3 py-2 font-normal">更新</th>
              </tr>
            </thead>
            <tbody>
              {list.data.items.map((is) => (
                <tr key={is.id} className="border-t border-line first:border-t-0 hover:bg-bg">
                  <td className="hidden px-3 py-2 align-top text-muted sm:table-cell">#{is.id}</td>
                  <td className="px-3 py-2">
                    <Link to="/issues/$id" params={{ id: String(is.id) }} className="text-ink hover:text-teal">
                      <span className="text-muted sm:hidden">#{is.id} </span>
                      {is.title}
                    </Link>
                    {/* 窄屏时把其余列合成一行带说明的摘要 */}
                    <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted sm:hidden">
                      <StatusTag status={is.status} label={is.status_label} />
                      <span className="inline-flex items-center gap-1">
                        <ToolMark tool={is.source_tool} withLabel={false} />
                        {is.source} 发起
                      </span>
                      <span>负责人 {is.owner || "未指定"}</span>
                      {is.target_service && <span>{is.target_service}</span>}
                      <span>{relativeTime(is.updated_at)}</span>
                    </div>
                  </td>
                  <td className="hidden px-3 py-2 sm:table-cell">
                    <StatusTag status={is.status} label={is.status_label} />
                  </td>
                  <td className="hidden px-3 py-2 text-muted sm:table-cell">{is.target_service || "未指定"}</td>
                  <td className="hidden px-3 py-2 sm:table-cell">
                    <span className="inline-flex items-center gap-1.5">
                      <ToolMark tool={is.source_tool} withLabel={false} />
                      {is.source}
                    </span>
                  </td>
                  <td className="hidden px-3 py-2 sm:table-cell">{is.owner || <span className="text-muted">未指定</span>}</td>
                  <td className="hidden px-3 py-2 text-muted sm:table-cell">{relativeTime(is.updated_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          {list.data.items.length >= 50 && (
            <p className="border-t border-line px-3 py-2 text-sm text-muted">只显示最近更新的 50 张，用关键词或状态缩小范围。</p>
          )}
        </div>
      )}
    </div>
  );
}
