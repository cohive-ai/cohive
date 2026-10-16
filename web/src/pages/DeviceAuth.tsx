import { useState } from "react";
import { Link, useParams } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { api, ApiError } from "../api";
import { Notice } from "../components";
import { absoluteTime, deviceAuthStatusLabel, relativeTime, toolsText } from "../format";
import { btn, Callout, ErrorText, errMsg } from "../ui";

// 终端执行 cohive login 后打开的授权页
export function DeviceAuthPage() {
  const { nonce } = useParams({ from: "/layout/device/$nonce" });
  const auth = useQuery({ queryKey: ["device-auth", nonce], queryFn: () => api.deviceAuth(nonce), retry: false });
  const [result, setResult] = useState<string>("");
  const [busy, setBusy] = useState<"" | "approve" | "deny">("");
  const [error, setError] = useState("");

  const decide = async (approve: boolean) => {
    setBusy(approve ? "approve" : "deny");
    setError("");
    try {
      const res = await api.decideDeviceAuth(nonce, approve);
      setResult(res.status);
    } catch (err) {
      setError(errMsg(err));
    } finally {
      setBusy("");
    }
  };

  let body: React.ReactNode;
  if (auth.isPending) {
    body = <Notice>正在读取授权请求…</Notice>;
  } else if (auth.isError) {
    const gone = auth.error instanceof ApiError && [400, 404, 410].includes(auth.error.status);
    body = gone ? (
      <Notice tone="danger">这个授权链接无效或已过期。回到终端重新执行 cohive login 获取新的链接。</Notice>
    ) : (
      <div>
        <Notice tone="danger">读取失败：{auth.error.message}</Notice>
        <button type="button" onClick={() => auth.refetch()} className={`${btn.secondary} mt-3`}>
          重试
        </button>
      </div>
    );
  } else {
    const d = auth.data;
    const status = result || d.status;
    body = (
      <div className="space-y-4">
        <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 rounded-lg border border-line bg-surface p-4 text-sm">
          <dt className="text-muted">设备名</dt>
          <dd className="min-w-0 break-words">
            <span className="font-medium">{d.device_name || "未命名"}</span>
            <span className="ml-2 text-xs text-muted">（终端自报）</span>
          </dd>
          <dt className="text-muted">编程工具</dt>
          <dd>{toolsText(d.tools)}</dd>
          <dt className="text-muted">发起时间</dt>
          <dd>
            {absoluteTime(d.created_at)}
            <span className="ml-2 text-muted">{relativeTime(d.created_at)}</span>
          </dd>
        </dl>
        {status === "pending" ? (
          <>
            <Callout>
              <p className="font-medium">只授权你自己刚刚在终端发起的登录。</p>
              <p className="mt-1">
                如果你没有在自己的电脑上执行 cohive login，或者这个链接是别人发给你的，请点“拒绝”。授权后这台设备就能以你的身份操作问题单。
              </p>
            </Callout>
            <p className="text-sm text-muted">
              {d.first_device
                ? "这是你的第一台设备，授权后立即生效。"
                : "你已经有其他设备，这台新设备授权后还需要管理员批准才能使用。"}
            </p>
            <ErrorText>{error}</ErrorText>
            <div className="flex flex-wrap gap-3">
              <button type="button" onClick={() => decide(true)} disabled={!!busy} className={btn.primary}>
                {busy === "approve" ? "正在授权…" : "授权"}
              </button>
              <button type="button" onClick={() => decide(false)} disabled={!!busy} className={btn.danger}>
                {busy === "deny" ? "正在拒绝…" : "拒绝"}
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
      <h1 className="mb-4 text-lg font-semibold">授权这台电脑登录</h1>
      {body}
    </div>
  );
}

function ResultMessage({ status }: { status: string }) {
  const msg: Record<string, React.ReactNode> = {
    approved: "已授权，回到终端即可。",
    delivered: "已授权，终端已经完成登录。",
    awaiting_admin: "已提交，等待管理员批准。终端开着的话批准后会自动完成；终端已经关掉的话，批准后在那台电脑上执行 cohive login --resume 领取。",
    denied: "已拒绝。这次登录不会生效。",
    expired: "这个授权请求已过期。回到终端重新执行 cohive login。",
  };
  const ok = status === "approved" || status === "delivered" || status === "awaiting_admin";
  return (
    <div className="space-y-3">
      {ok ? (
        <Callout tone="teal">{msg[status]}</Callout>
      ) : (
        <Notice tone={status === "denied" ? "muted" : "danger"}>
          {msg[status] ?? `当前状态：${deviceAuthStatusLabel[status] ?? status}`}
        </Notice>
      )}
      <Link to="/me" className="inline-block text-sm text-teal hover:underline">
        去个人中心查看我的设备
      </Link>
    </div>
  );
}
