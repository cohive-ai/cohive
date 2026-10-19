import { useEffect } from "react";
import { Trans, useTranslation } from "react-i18next";
import { Link, Outlet, useParams } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { api, ApiError } from "../api";
import { Notice } from "../components";
import { listSeparator, workspaceLabel } from "../format";
import { canAdminWorkspace, rememberWorkspace } from "../workspace";

// 当前页面所在的工作空间名（来自地址 /w/{工作空间}/...）
export function useWs(): string {
  return useParams({ from: "/layout/w/$ws" }).ws;
}

// 工作空间内页面的外壳：记住“上次用的工作空间”（只记自己是成员的），供首页与旧地址跳转使用
export function WorkspaceShell() {
  const ws = useWs();
  const me = useQuery({ queryKey: ["me"], queryFn: api.me });
  const member = !!me.data?.workspaces.some((w) => w.name === ws);
  useEffect(() => {
    if (member) rememberWorkspace(ws);
  }, [ws, member]);
  return <Outlet />;
}

// 看板与问题单读取失败：不是成员（或工作空间不存在）时说明清楚，服务器管理员额外给出去管理页把自己加进来的入口
export function WorkspaceLoadError({ error, what }: { error: Error; what: "board" | "issues" }) {
  const { t } = useTranslation("issues");
  const ws = useWs();
  const me = useQuery({ queryKey: ["me"], queryFn: api.me });
  if (error instanceof ApiError && error.status === 404) {
    const others = (me.data?.workspaces ?? []).filter((w) => w.name !== ws);
    return (
      <div className="mx-auto max-w-xl space-y-3">
        <Notice tone="danger">{t("workspaceError.notFound", { ws })}</Notice>
        {me.data && canAdminWorkspace(me.data, ws) && (
          <p className="text-sm">
            <Trans
              t={t}
              i18nKey="workspaceError.adminHint"
              components={{
                link: (
                  <Link to="/w/$ws/admin" params={{ ws }} search={{ tab: "members" }} className="text-accent hover:underline" />
                ),
              }}
            />
          </p>
        )}
        {others.length > 0 && (
          <p className="text-sm text-muted">
            {t("workspaceError.yours")}
            <span className="ml-1">
              {others.map((w, i) => (
                <span key={w.name}>
                  {i > 0 && listSeparator()}
                  <Link to="/w/$ws" params={{ ws: w.name }} className="text-accent hover:underline">
                    {workspaceLabel(w)}
                  </Link>
                </span>
              ))}
            </span>
          </p>
        )}
      </div>
    );
  }
  return (
    <Notice tone="danger">
      {what === "board"
        ? t("workspaceError.loadBoardFailed", { message: error.message })
        : t("workspaceError.loadIssuesFailed", { message: error.message })}
    </Notice>
  );
}
