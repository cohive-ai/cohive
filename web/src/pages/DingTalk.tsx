import { useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Trans, useTranslation } from "react-i18next";
import { safeTrans } from "../i18n/safeTrans";
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
  const { t } = useTranslation("me");
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
          if (!(err instanceof ApiError && err.status === 401)) setError(t("dingtalk.pollFailed", { message: errMsg(err) }));
          return;
        }
        setError(t("dingtalk.pollRetry", { message: errMsg(err) }));
      }
      timer = setTimeout(tick, POLL_MS);
    };
    timer = setTimeout(tick, POLL_MS);
    return () => {
      stopped = true;
      clearTimeout(timer);
    };
  }, [key, fetch, waitKey, t]);
  return { state, setState, error };
}

const linkStatus = (id: number) => api.linkStatus(id);

// DingTalkLink 是个人中心里的“连接钉钉”：网页给出绑定码，私聊发给机器人，再回网页确认才生效。
export function DingTalkLink({ me }: { me: Me }) {
  const { t } = useTranslation("me");
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
        err instanceof ApiError && err.status === 503
          ? t("dingtalk.notEnabled")
          : t("dingtalk.startFailed", { message: errMsg(err) }),
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
      setState({ status: accept ? "linked" : "failed", detail: accept ? "" : t("dingtalk.cancelled") });
      if (accept) await qc.invalidateQueries({ queryKey: ["me"] });
    } catch (err) {
      setError(t("dingtalk.confirmFailed", { message: errMsg(err) }));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="border-t border-line pt-3 text-sm first:border-t-0 first:pt-0">
      <div className="flex flex-wrap items-center gap-3">
        <span className="font-medium">{t("dingtalk.name")}</span>
        {linked ? (
          <span className="rounded bg-teal-soft px-1.5 py-px text-xs text-teal">
            {linked.display_name ? t("dingtalk.connectedAs", { name: linked.display_name }) : t("dingtalk.connected")}
          </span>
        ) : (
          <span className="rounded bg-sunken px-1.5 py-px text-xs text-muted">{t("dingtalk.notConnected")}</span>
        )}
        <span className="ml-auto">
          {linked ? (
            <ConfirmButton
              label={t("dingtalk.unlink")}
              title={t("dingtalk.unlinkTitle")}
              message={t("dingtalk.unlinkMessage")}
              confirmLabel={t("dingtalk.unlinkConfirm")}
              action={async () => {
                await api.unlink("dingtalk");
                setAttempt(null);
                await qc.invalidateQueries({ queryKey: ["me"] });
              }}
            />
          ) : (
            <button type="button" onClick={start} disabled={busy || status === "pending"} className={btn.primary}>
              {status === "pending" ? t("dingtalk.waiting") : t("dingtalk.connect")}
            </button>
          )}
        </span>
      </div>

      {attempt && status === "pending" && !linked && (
        <div className="mt-3 space-y-2 rounded border border-line bg-bg px-3 py-2">
          <p>
            <Trans t={t} i18nKey="dingtalk.instructions" components={{ b: <span className="font-medium" />, s: <strong /> }} />
          </p>
          <CopyField value={attempt.code} label={t("dingtalk.codeLabel")} />
          <p className="text-muted">{t("dingtalk.codeWarn")}</p>
        </div>
      )}
      {status === "awaiting_confirm" && (
        <div className="mt-3 space-y-2 rounded border border-line bg-bg px-3 py-2">
          <p>
            <Trans {...safeTrans}
              t={t}
              i18nKey="dingtalk.confirmPrompt"
              values={{ name: state?.candidate_name || t("dingtalk.noNickname") }}
              components={{ b: <span className="font-medium" /> }}
            />
          </p>
          <div className="flex gap-2">
            <button type="button" className={btn.primary} disabled={busy} onClick={() => confirm(true)}>
              {t("dingtalk.confirmYes")}
            </button>
            <button type="button" className={btn.secondary} disabled={busy} onClick={() => confirm(false)}>
              {t("dingtalk.confirmNo")}
            </button>
          </div>
        </div>
      )}
      {status === "linked" && <p className="mt-3 text-teal">{t("dingtalk.linked", { detail: state?.detail ?? "" })}</p>}
      {(status === "failed" || status === "expired") && (
        <p className="mt-3 text-danger">
          {status === "failed"
            ? t("dingtalk.failed", { detail: state?.detail ?? "" })
            : t("dingtalk.expired", { detail: state?.detail ?? "" })}
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
  const { t } = useTranslation("me");
  const [attempt, setAttempt] = useState<{ id: number; code: string; startedAt: number } | null>(null);
  const [fetchStatus] = useState(() => (id: number) => api.chatBindStatus(ws, id));
  const { state, error: pollError } = usePollStatus(attempt, fetchStatus, ["pending"]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const status = attempt ? (state?.status ?? "pending") : "";
  const bound = chat?.provider === "dingtalk" ? chat : null;
  // 机器人同时认中文“绑定群”与英文 bindgroup，按界面语言显示
  const sendText = attempt ? `${t("dingtalk.group.command")} ${attempt.code}` : "";

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
      setError(t("dingtalk.group.startFailed", { message: errMsg(err) }));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="space-y-2 text-sm">
      <div className="flex flex-wrap items-center gap-3">
        <span className="font-medium">{t("dingtalk.group.name")}</span>
        {bound ? (
          <span className="rounded bg-teal-soft px-1.5 py-px text-xs text-teal">{bound.title ? t("dingtalk.group.boundTitle", { title: bound.title }) : t("dingtalk.group.bound")}</span>
        ) : (
          <span className="rounded bg-sunken px-1.5 py-px text-xs text-muted">{t("dingtalk.group.notBound")}</span>
        )}
        <span className="ml-auto">
          {bound ? (
            <ConfirmButton
              label={t("dingtalk.group.unbind")}
              title={t("dingtalk.group.unbindTitle")}
              message={t("dingtalk.group.unbindMessage")}
              confirmLabel={t("dingtalk.group.unbindConfirm")}
              action={async () => {
                await api.unbindChat(ws, "dingtalk");
                setAttempt(null);
                onChange();
              }}
            />
          ) : (
            <button type="button" className={btn.secondary} disabled={busy || chat != null} onClick={start}>
              {t("dingtalk.group.generate")}
            </button>
          )}
        </span>
      </div>
      {chat != null && !bound && <p className="text-muted">{t("dingtalk.group.otherBound")}</p>}
      {attempt && status === "pending" && !bound && (
        <div className="space-y-2 rounded border border-line bg-bg px-3 py-2">
          <p>
            <Trans t={t} i18nKey="dingtalk.group.instructions" components={{ b: <strong /> }} />
          </p>
          <CopyField value={sendText} label={t("dingtalk.group.sendLabel")} />
          <p className="text-muted">{t("dingtalk.group.expiry")}</p>
        </div>
      )}
      {status === "failed" && <p className="text-danger">{t("dingtalk.group.failed", { detail: state?.detail ?? "" })}</p>}
      {status === "expired" && <p className="text-danger">{t("dingtalk.group.expired")}</p>}
      <ErrorText>{error || pollError}</ErrorText>
    </div>
  );
}
