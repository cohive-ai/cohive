import { useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Trans, useTranslation } from "react-i18next";
import { safeTrans } from "../i18n/safeTrans";
import { api, ApiError, type CodeProvider, type LinkStatus, type Me } from "../api";
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
        setError(t("dingtalk.pollRetry", { message: errMsg(err) })); // 两种通讯工具的这两句文案相同
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

// CodeLink 是个人中心里的“连接钉钉 / 连接 Slack”（用码绑定的通讯工具）：网页给出绑定码，私聊发给机器人，再回网页确认才生效。
// 文案在 me 语言包里按通讯工具分组（dingtalk.*、slack.*），两组的键相同。
export function CodeLink({ me, provider: p }: { me: Me; provider: CodeProvider }) {
  const { t } = useTranslation("me");
  const qc = useQueryClient();
  const linked = me.chat?.find((c) => c.provider === p && c.linked);
  const [attempt, setAttempt] = useState<{ id: number; code: string; startedAt: number } | null>(null);
  const { state, setState, error: pollError } = usePollStatus(attempt, linkStatus, ["pending", "awaiting_confirm"]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const status = attempt ? (state?.status ?? "pending") : "";

  const start = async () => {
    setBusy(true);
    setError("");
    try {
      const res = await api.startCodeLink(p);
      setAttempt({ id: res.id, code: res.code, startedAt: Date.now() });
      setState({ status: "pending" });
    } catch (err) {
      setError(
        err instanceof ApiError && err.status === 503
          ? t(`${p}.notEnabled`)
          : t(`${p}.startFailed`, { message: errMsg(err) }),
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
      setState({ status: accept ? "linked" : "failed", detail: accept ? "" : t(`${p}.cancelled`) });
      if (accept) await qc.invalidateQueries({ queryKey: ["me"] });
    } catch (err) {
      setError(t(`${p}.confirmFailed`, { message: errMsg(err) }));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="border-t border-line pt-3 text-sm first:border-t-0 first:pt-0">
      <div className="flex flex-wrap items-center gap-3">
        <span className="font-medium">{t(`${p}.name`)}</span>
        {linked ? (
          <span className="rounded bg-teal-soft px-1.5 py-px text-xs text-teal">
            {linked.display_name ? t(`${p}.connectedAs`, { name: linked.display_name }) : t(`${p}.connected`)}
          </span>
        ) : (
          <span className="rounded bg-sunken px-1.5 py-px text-xs text-muted">{t(`${p}.notConnected`)}</span>
        )}
        <span className="ml-auto">
          {linked ? (
            <ConfirmButton
              label={t(`${p}.unlink`)}
              title={t(`${p}.unlinkTitle`)}
              message={t(`${p}.unlinkMessage`)}
              confirmLabel={t(`${p}.unlinkConfirm`)}
              action={async () => {
                await api.unlink(p);
                setAttempt(null);
                await qc.invalidateQueries({ queryKey: ["me"] });
              }}
            />
          ) : (
            <button type="button" onClick={start} disabled={busy || status === "pending"} className={btn.primary}>
              {status === "pending" ? t(`${p}.waiting`) : t(`${p}.connect`)}
            </button>
          )}
        </span>
      </div>

      {attempt && status === "pending" && !linked && (
        <div className="mt-3 space-y-2 rounded border border-line bg-bg px-3 py-2">
          <p>
            <Trans t={t} i18nKey={`${p}.instructions`} components={{ b: <span className="font-medium" />, s: <strong /> }} />
          </p>
          <CopyField value={attempt.code} label={t(`${p}.codeLabel`)} />
          <p className="text-muted">{t(`${p}.codeWarn`)}</p>
        </div>
      )}
      {status === "awaiting_confirm" && (
        <div className="mt-3 space-y-2 rounded border border-line bg-bg px-3 py-2">
          <p>
            <Trans {...safeTrans}
              t={t}
              i18nKey={`${p}.confirmPrompt`}
              values={{ name: state?.candidate_name || t(`${p}.noNickname`) }}
              components={{ b: <span className="font-medium" /> }}
            />
          </p>
          <div className="flex gap-2">
            <button type="button" className={btn.primary} disabled={busy} onClick={() => confirm(true)}>
              {t(`${p}.confirmYes`)}
            </button>
            <button type="button" className={btn.secondary} disabled={busy} onClick={() => confirm(false)}>
              {t(`${p}.confirmNo`)}
            </button>
          </div>
        </div>
      )}
      {status === "linked" && <p className="mt-3 text-teal">{t(`${p}.linked`, { detail: state?.detail ?? "" })}</p>}
      {(status === "failed" || status === "expired") && (
        <p className="mt-3 text-danger">
          {status === "failed"
            ? t(`${p}.failed`, { detail: state?.detail ?? "" })
            : t(`${p}.expired`, { detail: state?.detail ?? "" })}
        </p>
      )}
      <ErrorText>{error || pollError}</ErrorText>
    </div>
  );
}

// CodeBind 是工作空间设置里的“绑定钉钉群 / Slack 频道”：生成群绑定码，在群里 @机器人 发出后完成。
export function CodeBind({
  ws,
  chat,
  provider: p,
  onChange,
}: {
  ws: string;
  chat?: { provider: string; chat_id: string; title?: string } | null;
  provider: CodeProvider;
  onChange: () => void;
}) {
  const { t } = useTranslation("me");
  const [attempt, setAttempt] = useState<{ id: number; code: string; startedAt: number } | null>(null);
  const [fetchStatus] = useState(() => (id: number) => api.chatBindStatus(ws, id));
  const { state, error: pollError } = usePollStatus(attempt, fetchStatus, ["pending"]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const status = attempt ? (state?.status ?? "pending") : "";
  const bound = chat?.provider === p ? chat : null;
  // 机器人同时认中文“绑定群”与英文 bindgroup，按界面语言显示
  const sendText = attempt ? `${t(`${p}.group.command`)} ${attempt.code}` : "";

  useEffect(() => {
    if (status === "bound") onChange();
  }, [status, onChange]);

  const start = async () => {
    setBusy(true);
    setError("");
    try {
      const res = await api.startChatBind(ws, p);
      setAttempt({ id: res.id, code: res.code, startedAt: Date.now() });
    } catch (err) {
      setError(t(`${p}.group.startFailed`, { message: errMsg(err) }));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="space-y-2 text-sm">
      <div className="flex flex-wrap items-center gap-3">
        <span className="font-medium">{t(`${p}.group.name`)}</span>
        {bound ? (
          <span className="rounded bg-teal-soft px-1.5 py-px text-xs text-teal">{bound.title ? t(`${p}.group.boundTitle`, { title: bound.title }) : t(`${p}.group.bound`)}</span>
        ) : (
          <span className="rounded bg-sunken px-1.5 py-px text-xs text-muted">{t(`${p}.group.notBound`)}</span>
        )}
        <span className="ml-auto">
          {bound ? (
            <ConfirmButton
              label={t(`${p}.group.unbind`)}
              title={t(`${p}.group.unbindTitle`)}
              message={t(`${p}.group.unbindMessage`)}
              confirmLabel={t(`${p}.group.unbindConfirm`)}
              action={async () => {
                await api.unbindChat(ws, p);
                setAttempt(null);
                onChange();
              }}
            />
          ) : (
            <button type="button" className={btn.secondary} disabled={busy || chat != null} onClick={start}>
              {t(`${p}.group.generate`)}
            </button>
          )}
        </span>
      </div>
      {chat != null && !bound && <p className="text-muted">{t(`${p}.group.otherBound`)}</p>}
      {attempt && status === "pending" && !bound && (
        <div className="space-y-2 rounded border border-line bg-bg px-3 py-2">
          <p>
            <Trans t={t} i18nKey={`${p}.group.instructions`} components={{ b: <strong /> }} />
          </p>
          <CopyField value={sendText} label={t(`${p}.group.sendLabel`)} />
          <p className="text-muted">{t(`${p}.group.expiry`)}</p>
        </div>
      )}
      {status === "failed" && <p className="text-danger">{t(`${p}.group.failed`, { detail: state?.detail ?? "" })}</p>}
      {status === "expired" && <p className="text-danger">{t(`${p}.group.expired`)}</p>}
      <ErrorText>{error || pollError}</ErrorText>
    </div>
  );
}
