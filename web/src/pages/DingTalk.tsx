import { useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { api, ApiError, type LinkStatus, type Me } from "../api";
import { btn, ConfirmButton, CopyField, ErrorText, errMsg } from "../ui";
import { shouldRetryPoll } from "../rules";

const POLL_MS = 2000;
const TIMEOUT_MS = 10 * 60 * 1000;

// usePollStatus 每 2 秒查一次状态，直到状态不再是 waitFor 之一、超时或卸载。
export function usePollStatus(
  key: { id: number; startedAt: number } | null,
  fetch: (id: number) => Promise<LinkStatus>,
  waitFor: string[],
) {
  const [state, setState] = useState<LinkStatus | null>(null);
  const [error, setError] = useState("");
  const waitKey = waitFor.join(",");
  useEffect(() => {
    if (!key) return;
    let stopped = false;
    let timer: ReturnType<typeof setTimeout>;
    const tick = async () => {
      if (stopped) return;
      if (Date.now() - key.startedAt > TIMEOUT_MS) {
        setState({ status: "expired" });
        return;
      }
      try {
        const res = await fetch(key.id);
        if (stopped) return;
        setState(res);
        setError("");
        if (!waitKey.split(",").includes(res.status)) return;
      } catch (err) {
        if (stopped) return;
        if (!shouldRetryPoll(err)) {
          if (!(err instanceof ApiError && err.status === 401)) setError(`查询状态失败：${errMsg(err)}`);
          return;
        }
        setError(`查询状态失败：${errMsg(err)}，稍后自动重试。`);
      }
      timer = setTimeout(tick, POLL_MS);
    };
    timer = setTimeout(tick, POLL_MS);
    return () => {
      stopped = true;
      clearTimeout(timer);
    };
  }, [key, fetch, waitKey]);
  return { state, setState, error };
}

const linkStatus = (id: number) => api.linkStatus(id);

// DingTalkLink 是个人中心里的“连接钉钉”：网页给出绑定码，私聊发给机器人，再回网页确认才生效。
export function DingTalkLink({ me }: { me: Me }) {
  const qc = useQueryClient();
  const linked = me.chat?.find((c) => c.provider === "dingtalk" && c.linked);
  const [attempt, setAttempt] = useState<{ id: number; code: string; startedAt: number } | null>(null);
  const { state, setState, error: pollError } = usePollStatus(attempt, linkStatus, ["pending", "awaiting_confirm"]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const status = attempt ? (state?.status ?? "pending") : "";

  const start = async () => {
    setBusy(true);
    setError("");
    try {
      const res = await api.startDingTalkLink();
      setAttempt({ id: res.id, code: res.code, startedAt: Date.now() });
      setState({ status: "pending" });
    } catch (err) {
      setError(
        err instanceof ApiError && err.status === 503 ? "协作服务没有启用钉钉，请联系管理员。" : `无法发起连接：${errMsg(err)}`,
      );
    } finally {
      setBusy(false);
    }
  };
  const confirm = async (accept: boolean) => {
    if (!attempt) return;
    setBusy(true);
    setError("");
    try {
      await api.confirmLink(attempt.id, accept);
      setState({ status: accept ? "linked" : "failed", detail: accept ? "" : "已取消" });
      if (accept) await qc.invalidateQueries({ queryKey: ["me"] });
    } catch (err) {
      setError(`确认失败：${errMsg(err)}`);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="border-t border-line pt-3 text-sm first:border-t-0 first:pt-0">
      <div className="flex flex-wrap items-center gap-3">
        <span className="font-medium">钉钉</span>
        {linked ? (
          <span className="rounded bg-teal-soft px-1.5 py-px text-xs text-teal">
            已连接{linked.display_name ? ` ${linked.display_name}` : ""}
          </span>
        ) : (
          <span className="rounded bg-sunken px-1.5 py-px text-xs text-muted">未连接</span>
        )}
        <span className="ml-auto">
          {linked ? (
            <ConfirmButton
              label="解除连接"
              title="解除钉钉连接？"
              message="解除后群里的通知不会再 @ 到你，你也不能在钉钉群里点按钮审批。之后可以随时重新连接。"
              confirmLabel="确认解除"
              action={async () => {
                await api.unlink("dingtalk");
                setAttempt(null);
                await qc.invalidateQueries({ queryKey: ["me"] });
              }}
            />
          ) : (
            <button type="button" onClick={start} disabled={busy || status === "pending"} className={btn.primary}>
              {status === "pending" ? "等待在钉钉里发码…" : "连接钉钉"}
            </button>
          )}
        </span>
      </div>

      {attempt && status === "pending" && !linked && (
        <div className="mt-3 space-y-2 rounded border border-line bg-bg px-3 py-2">
          <p>
            在钉钉里打开与机器人 <span className="font-medium">Cohive</span> 的<strong>单聊</strong>
            （群里点机器人头像 → 发消息，或在钉钉搜索里搜机器人名），把下面的绑定码发给它。10 分钟内有效。
          </p>
          <CopyField value={attempt.code} label="绑定码" />
          <p className="text-muted">绑定码只能私聊发给机器人；发到群里会立即作废。</p>
        </div>
      )}
      {status === "awaiting_confirm" && (
        <div className="mt-3 space-y-2 rounded border border-line bg-bg px-3 py-2">
          <p>
            机器人收到了钉钉用户 <span className="font-medium">{state?.candidate_name || "（没有昵称）"}</span>{" "}
            发来的绑定码。确认是你本人吗？确认后才会连接。
          </p>
          <div className="flex gap-2">
            <button type="button" className={btn.primary} disabled={busy} onClick={() => confirm(true)}>
              是我，确认连接
            </button>
            <button type="button" className={btn.secondary} disabled={busy} onClick={() => confirm(false)}>
              不是我，取消
            </button>
          </div>
        </div>
      )}
      {status === "linked" && <p className="mt-3 text-teal">已连接钉钉。{state?.detail}</p>}
      {(status === "failed" || status === "expired") && (
        <p className="mt-3 text-danger">
          {status === "failed" ? "连接没有完成。" : "绑定码已过期，请重新点“连接钉钉”。"}
          {state?.detail}
        </p>
      )}
      <ErrorText>{error || pollError}</ErrorText>
    </div>
  );
}

// DingTalkBind 是工作空间设置里的“绑定钉钉群”：生成群绑定码，在群里 @机器人 发出后完成。
export function DingTalkBind({
  ws,
  chat,
  onChange,
}: {
  ws: string;
  chat?: { provider: string; chat_id: string; title?: string } | null;
  onChange: () => void;
}) {
  const [attempt, setAttempt] = useState<{ id: number; code: string; startedAt: number } | null>(null);
  const [fetchStatus] = useState(() => (id: number) => api.chatBindStatus(ws, id));
  const { state, error: pollError } = usePollStatus(attempt, fetchStatus, ["pending"]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const status = attempt ? (state?.status ?? "pending") : "";
  const bound = chat?.provider === "dingtalk" ? chat : null;

  useEffect(() => {
    if (status === "bound") onChange();
  }, [status, onChange]);

  const start = async () => {
    setBusy(true);
    setError("");
    try {
      const res = await api.startChatBind(ws, "dingtalk");
      setAttempt({ id: res.id, code: res.code, startedAt: Date.now() });
    } catch (err) {
      setError(`无法生成绑定码：${errMsg(err)}`);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="space-y-2 text-sm">
      <div className="flex flex-wrap items-center gap-3">
        <span className="font-medium">钉钉群</span>
        {bound ? (
          <span className="rounded bg-teal-soft px-1.5 py-px text-xs text-teal">已绑定{bound.title ? `：${bound.title}` : ""}</span>
        ) : (
          <span className="rounded bg-sunken px-1.5 py-px text-xs text-muted">未绑定</span>
        )}
        <span className="ml-auto">
          {bound ? (
            <ConfirmButton
              label="解绑"
              title="解绑钉钉群？"
              message="解绑后群通知和审批消息不再发到这个群。之后可以重新生成绑定码绑定。"
              confirmLabel="确认解绑"
              action={async () => {
                await api.unbindChat(ws, "dingtalk");
                setAttempt(null);
                onChange();
              }}
            />
          ) : (
            <button type="button" className={btn.secondary} disabled={busy || chat != null} onClick={start}>
              生成群绑定码
            </button>
          )}
        </span>
      </div>
      {chat != null && !bound && <p className="text-muted">本工作空间已绑定别的通讯工具的群，要先解绑才能绑定钉钉群。</p>}
      {attempt && status === "pending" && !bound && (
        <div className="space-y-2 rounded border border-line bg-bg px-3 py-2">
          <p>
            先把机器人 Cohive 加进项目的钉钉<strong>内部群</strong>（群设置 → 机器人 → 添加机器人），然后在群里 @Cohive 发送：
          </p>
          <CopyField value={`绑定群 ${attempt.code}`} label="在群里发送" />
          <p className="text-muted">10 分钟内有效，只能用一次。绑定成功后这里会自动更新。</p>
        </div>
      )}
      {status === "failed" && <p className="text-danger">绑定没有完成：{state?.detail}</p>}
      {status === "expired" && <p className="text-danger">绑定码已过期，请重新生成。</p>}
      <ErrorText>{error || pollError}</ErrorText>
    </div>
  );
}
