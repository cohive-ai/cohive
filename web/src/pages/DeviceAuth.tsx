import { useState } from "react";
import { Link, useParams } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, ApiError, needsPasswordLogin } from "../api";
import { NeedPasswordLogin, Notice } from "../components";
import { absoluteTime, relativeTime, toolsText } from "../format";
import i18n from "../i18n";
import { btn, Callout, ErrorText, errMsg } from "../ui";
import { authorizeAsText, deviceScopeText, joinNames } from "../rules";

// 终端执行 cohive login 后打开的授权页
export function DeviceAuthPage() {
  const { t } = useTranslation("auth");
  const { nonce } = useParams({ from: "/layout/device/$nonce" });
  const auth = useQuery({ queryKey: ["device-auth", nonce], queryFn: () => api.deviceAuth(nonce), retry: false });
  const me = useQuery({ queryKey: ["me"], queryFn: api.me });
  const [result, setResult] = useState<string>("");
  const [busy, setBusy] = useState<"" | "approve" | "deny">("");
  const [error, setError] = useState("");
  const [needPassword, setNeedPassword] = useState(false);

  const decide = async (approve: boolean) => {
    setBusy(approve ? "approve" : "deny");
    setError("");
    try {
      const res = await api.decideDeviceAuth(nonce, approve);
      setResult(res.status);
    } catch (err) {
      if (needsPasswordLogin(err)) setNeedPassword(true);
      else setError(errMsg(err));
    } finally {
      setBusy("");
    }
  };

  let body: React.ReactNode;
  if (needPassword || (auth.isError && needsPasswordLogin(auth.error))) {
    body = (
      <NeedPasswordLogin
        redirectTo={`/device/${encodeURIComponent(nonce)}`}
        message={t("device.needPassword")}
      />
    );
  } else if (auth.isPending) {
    body = <Notice>{t("device.loading")}</Notice>;
  } else if (auth.isError) {
    const gone = auth.error instanceof ApiError && [400, 404, 410].includes(auth.error.status);
    body = gone ? (
      <Notice tone="danger">{t("device.gone")}</Notice>
    ) : (
      <div>
        <Notice tone="danger">{t("device.loadFailed", { message: auth.error.message })}</Notice>
        <button type="button" onClick={() => auth.refetch()} className={`${btn.secondary} mt-3`}>
          {t("device.retry")}
        </button>
      </div>
    );
  } else {
    const d = auth.data;
    const status = result || d.status;
    body = (
      <div className="space-y-4">
        <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 rounded-lg border border-line bg-surface p-4 text-sm">
          <dt className="text-muted">{t("device.name")}</dt>
          <dd className="min-w-0 break-words">
            <span className="font-medium">{d.device_name || t("device.unnamed")}</span>
            <span className="ml-2 text-xs text-muted">{t("device.selfReported")}</span>
          </dd>
          <dt className="text-muted">{t("device.tools")}</dt>
          <dd>{toolsText(d.tools)}</dd>
          <dt className="text-muted">{t("device.scope")}</dt>
          <dd>
            {d.workspaces && d.workspaces.length ? (
              joinNames(d.workspaces)
            ) : (
              <span className="text-muted">{deviceScopeText(d.workspaces)}</span>
            )}
          </dd>
          <dt className="text-muted">{t("device.created")}</dt>
          <dd>
            {absoluteTime(d.created_at)}
            <span className="ml-2 text-muted">{relativeTime(d.created_at)}</span>
          </dd>
        </dl>
        {status === "pending" ? (
          <>
            <Callout>
              <p className="font-medium">{t("device.warnTitle")}</p>
              <p className="mt-1">{t("device.warnBody")}</p>
            </Callout>
            <p className="text-sm text-muted">
              {d.first_device ? t("device.firstDevice") : t("device.needsAdmin")}
            </p>
            {me.data && <p className="text-sm font-medium">{authorizeAsText(me.data.account)}</p>}
            <ErrorText>{error}</ErrorText>
            <div className="flex flex-wrap gap-3">
              <button type="button" onClick={() => decide(true)} disabled={!!busy} className={btn.primary}>
                {busy === "approve" ? t("device.approving") : t("device.approve")}
              </button>
              <button type="button" onClick={() => decide(false)} disabled={!!busy} className={btn.danger}>
                {busy === "deny" ? t("device.denying") : t("device.deny")}
              </button>
            </div>
          </>
        ) : (
          <ResultMessage status={status} />
        )}
      </div>
    );
  }

  return (
    <div className="mx-auto max-w-xl">
      <h1 className="mb-4 text-lg font-semibold">{t("device.title")}</h1>
      {body}
    </div>
  );
}

// 授权请求的状态名（服务端给出）对应的说明；没见过的状态原样显示
const statusLabels: Record<string, () => string> = {
  pending: () => i18n.t("auth:device.status.pending"),
  awaiting_admin: () => i18n.t("auth:device.status.awaiting_admin"),
  approved: () => i18n.t("auth:device.status.approved"),
  delivered: () => i18n.t("auth:device.status.delivered"),
  denied: () => i18n.t("auth:device.status.denied"),
  expired: () => i18n.t("auth:device.status.expired"),
};

function ResultMessage({ status }: { status: string }) {
  const { t } = useTranslation("auth");
  const msg: Record<string, string> = {
    approved: t("device.result.approved"),
    delivered: t("device.result.delivered"),
    awaiting_admin: t("device.result.awaiting_admin"),
    denied: t("device.result.denied"),
    expired: t("device.result.expired"),
  };
  const ok = status === "approved" || status === "delivered" || status === "awaiting_admin";
  return (
    <div className="space-y-3">
      {ok ? (
        <Callout tone="teal">{msg[status]}</Callout>
      ) : (
        <Notice tone={status === "denied" ? "muted" : "danger"}>
          {msg[status] ?? t("device.currentStatus", { status: statusLabels[status]?.() ?? status })}
        </Notice>
      )}
      <Link to="/me" className="inline-block text-sm text-accent hover:underline">
        {t("device.viewDevices")}
      </Link>
    </div>
  );
}
