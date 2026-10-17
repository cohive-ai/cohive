import { useEffect, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError, type Me } from "../api";
import { DeviceTokens, Notice } from "../components";
import { absoluteTime, roleLabel, workspaceLabel } from "../format";
import { btn, ConfirmButton, CopyField, ErrorText, errMsg, Section } from "../ui";
import { initCommands, installCommands, isTelegramLink, revokeDeviceMessage, shouldRetryPoll } from "../rules";
import { ChangePasswordForm } from "./Password";

export function MePage() {
  const me = useQuery({ queryKey: ["me"], queryFn: api.me });

  if (me.isPending) return <Notice>正在读取…</Notice>;
  if (me.isError) return <Notice tone="danger">读取失败：{me.error.message}</Notice>;
  const m = me.data;

  return (
    <div className="mx-auto max-w-3xl space-y-5">
      <h1 className="text-lg font-semibold">个人中心</h1>

      <Section title="基本信息">
        <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 text-sm">
          <dt className="text-muted">登录名</dt>
          <dd className="font-mono">{m.account.name}</dd>
          <dt className="text-muted">显示名</dt>
          <dd>{m.account.display_name || <span className="text-muted">未设置</span>}</dd>
          {m.account.server_admin && (
            <>
              <dt className="text-muted">身份</dt>
              <dd>服务器管理员</dd>
            </>
          )}
        </dl>
      </Section>

      <Section title="我的工作空间" description="你是这些工作空间的成员。同一个账号、同一台电脑可以用于全部工作空间。">
        {m.workspaces.length === 0 ? (
          <p className="text-sm text-muted">你还没有加入任何工作空间，请联系管理员。</p>
        ) : (
          <ul className="divide-y divide-line rounded border border-line">
            {m.workspaces.map((w) => (
              <li key={w.name} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2.5 text-sm">
                <Link to="/w/$ws" params={{ ws: w.name }} className="font-medium text-ink hover:text-teal">
                  {workspaceLabel(w)}
                </Link>
                <span
                  className={`rounded px-1.5 py-px text-xs ${
                    w.role === "admin" ? "bg-amber-soft text-amber" : "bg-sunken text-muted"
                  }`}
                >
                  {roleLabel(w.role)}
                </span>
                {w.role === "admin" && (
                  <Link to="/w/$ws/admin" params={{ ws: w.name }} className="ml-auto text-teal hover:underline">
                    管理
                  </Link>
                )}
              </li>
            ))}
          </ul>
        )}
      </Section>

      <BindComputer workspaces={m.workspaces.map((w) => w.name)} />

      <MyDevices />

      <Telegram me={m} />

      <Section title="修改密码">
        <ChangePasswordForm hasPassword={m.account.has_password} />
      </Section>
    </div>
  );
}

// 绑定新电脑：一条命令装好命令行（只从本服务下载、核对校验和）并接着配对，再在项目目录初始化
function BindComputer({ workspaces }: { workspaces: string[] }) {
  const v = useQuery({ queryKey: ["version"], queryFn: api.version, staleTime: 5 * 60_000 });
  const origin = window.location.origin;
  const cmd = installCommands(origin, true);
  const code = (t: string) => <code className="rounded bg-sunken px-1 font-mono text-ink">{t}</code>;

  return (
    <Section title="绑定新电脑" description="让这台电脑上的编程工具以你的身份使用协作服务。">
      <ol className="space-y-4 text-sm">
        <li>
          <p className="mb-2 font-medium">1. 安装命令行并配对这台电脑</p>
          {v.isPending ? (
            <p className="text-muted">正在读取…</p>
          ) : v.isError ? (
            <p className="text-danger">读取安装信息失败：{v.error.message}</p>
          ) : v.data.cli === null ? (
            <Notice tone="danger">这台服务器没有附带命令行安装包，请联系管理员。</Notice>
          ) : (
            <div className="space-y-2">
              <p className="text-muted">macOS、Linux：在终端执行</p>
              <CopyField label="macOS、Linux 安装并配对" value={cmd.unix} />
              <p className="text-muted">Windows：在 PowerShell 执行</p>
              <CopyField label="Windows 安装并配对" value={cmd.windows} />
              <p className="text-muted">
                装好后会接着打开浏览器，点“授权”即可。版本 {v.data.cli.version}。只从本服务下载，安装前会核对校验和；装在{" "}
                {code("~/.local/bin")}（Windows 装在当前用户目录），不需要管理员权限。装过的电脑再执行一次即可升级。
              </p>
              <p className="text-muted">已经装好命令行、只需配对时：</p>
              <CopyField label="配对命令" value={`cohive login --server ${origin}`} />
            </div>
          )}
        </li>
        <li>
          <p className="mb-2 font-medium">2. 在项目目录执行</p>
          <p className="mb-2 text-muted">
            写入编程工具的配置（不含令牌，可以提交进仓库）。团队里已经有人提交过这些配置的项目不用再做。
          </p>
          {initCommands(workspaces).map((c) => (
            <CopyField key={c} label="初始化命令" value={c} />
          ))}
        </li>
        <li>
          <p className="mb-1 font-medium">3. 以后免密打开网页</p>
          <p className="text-muted">已经配对过的电脑，执行 {code("cohive web")} 可以免密打开这个网页。</p>
        </li>
      </ol>
    </Section>
  );
}

function MyDevices() {
  const qc = useQueryClient();
  const list = useQuery({ queryKey: ["me", "devices"], queryFn: api.myDevices });

  return (
    <Section title="我的设备" description="每台绑定过的电脑。丢失或不再使用的电脑请吊销，吊销后它上面的登录立即失效。">
      {list.isPending ? (
        <p className="text-sm text-muted">正在读取…</p>
      ) : list.isError ? (
        <p className="text-sm text-danger">读取失败：{list.error.message}</p>
      ) : list.data.items.length === 0 ? (
        <p className="text-sm text-muted">还没有绑定任何电脑。按上面的命令绑定第一台。</p>
      ) : (
        <ul className="divide-y divide-line rounded border border-line">
          {list.data.items.map((d) => (
            <li key={d.id} className="flex flex-wrap items-center gap-x-4 gap-y-2 px-3 py-2.5 text-sm">
              <div className="min-w-0 flex-1">
                <p className="break-words font-medium">{d.name || "未命名"}</p>
                <DeviceTokens tokens={d.tokens} revoked={!!d.revoked_at} />
                <p className="text-xs text-muted">
                  {absoluteTime(d.created_at)} 添加
                  {d.last_used_at && ` · 最近使用 ${absoluteTime(d.last_used_at)}`}
                  {d.revoked_at && ` · ${absoluteTime(d.revoked_at)} 吊销`}
                </p>
              </div>
              {d.revoked_at ? (
                <span className="rounded bg-sunken px-1.5 py-px text-xs text-muted">已吊销</span>
              ) : (
                <>
                  <span className="rounded bg-teal-soft px-1.5 py-px text-xs text-teal">使用中</span>
                  <ConfirmButton
                    label="吊销"
                    title="吊销这台设备？"
                    message={revokeDeviceMessage(d.name, list.data.items.filter((x) => !x.revoked_at).length === 1)}
                    confirmLabel="确认吊销"
                    action={async () => {
                      await api.revokeMyDevice(d.id);
                      await qc.invalidateQueries({ queryKey: ["me", "devices"] });
                    }}
                  />
                </>
              )}
            </li>
          ))}
        </ul>
      )}
    </Section>
  );
}

interface LinkAttempt {
  id: number | string;
  url: string;
  startedAt: number;
}

const LINK_POLL_MS = 2000;
const LINK_TIMEOUT_MS = 10 * 60 * 1000;

function Telegram({ me }: { me: Me }) {
  const qc = useQueryClient();
  const [attempt, setAttempt] = useState<LinkAttempt | null>(null);
  const [state, setState] = useState<{ status: string; detail?: string } | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const linked = !!me.account.telegram?.linked;

  // 每 2 秒查一次连接结果，最多等 10 分钟
  useEffect(() => {
    if (!attempt) return;
    let stopped = false;
    let timer: ReturnType<typeof setTimeout>;
    const tick = async () => {
      if (stopped) return;
      if (Date.now() - attempt.startedAt > LINK_TIMEOUT_MS) {
        setState({ status: "timeout" });
        return;
      }
      try {
        const res = await api.linkStatus(attempt.id);
        if (stopped) return;
        setState(res);
        setError("");
        if (res.status !== "pending") {
          if (res.status === "linked") qc.invalidateQueries({ queryKey: ["me"] });
          return;
        }
      } catch (err) {
        if (stopped) return;
        if (!shouldRetryPoll(err)) {
          // 401 已由全局处理回登录页；其余 4xx 重试也不会好，停下并提示
          if (!(err instanceof ApiError && err.status === 401)) {
            setState(null);
            setError(`查询连接结果失败：${errMsg(err)}。请重新点“连接 Telegram”。`);
          }
          return;
        }
        // 网络抖动或服务端暂时出错时继续等
        setError(`查询连接结果失败：${errMsg(err)}，稍后自动重试。`);
      }
      timer = setTimeout(tick, LINK_POLL_MS);
    };
    timer = setTimeout(tick, LINK_POLL_MS);
    return () => {
      stopped = true;
      clearTimeout(timer);
    };
  }, [attempt, qc]);

  const connect = async () => {
    setBusy(true);
    setError("");
    setState(null);
    // 先同步打开空白窗口，避免等接口返回后再打开被浏览器拦截
    const win = window.open("", "_blank");
    try {
      const res = await api.startLink();
      if (!isTelegramLink(res.url)) {
        throw new Error("协作服务返回的连接链接不是 Telegram 机器人链接，已拒绝打开");
      }
      if (win) {
        win.opener = null;
        win.location.href = res.url;
      }
      setAttempt({ id: res.id, url: res.url, startedAt: Date.now() });
      setState({ status: "pending" });
    } catch (err) {
      win?.close();
      setError(
        err instanceof ApiError && err.status === 503
          ? "协作服务没有启用 Telegram 机器人，请联系管理员。"
          : `无法发起连接：${errMsg(err)}`,
      );
    } finally {
      setBusy(false);
    }
  };

  const status = state?.status;
  const waiting = status === "pending";

  return (
    <Section
      title="通讯工具"
      description="连接 Telegram 后，群里的问题单通知会 @ 到你，管理员也能在群里直接点按钮审批。"
    >
      <div className="flex flex-wrap items-center gap-3 text-sm">
        <span className="font-medium">Telegram</span>
        {linked ? (
          <span className="rounded bg-teal-soft px-1.5 py-px text-xs text-teal">
            已连接{me.account.telegram?.username ? ` @${me.account.telegram.username}` : ""}
          </span>
        ) : (
          <span className="rounded bg-sunken px-1.5 py-px text-xs text-muted">未连接</span>
        )}
        <span className="ml-auto">
          {linked ? (
            <ConfirmButton
              label="解除连接"
              title="解除 Telegram 连接？"
              message="解除后群里的通知不会再 @ 到你。之后可以随时重新连接。"
              confirmLabel="确认解除"
              action={async () => {
                await api.unlinkTelegram();
                setAttempt(null);
                setState(null);
                await qc.invalidateQueries({ queryKey: ["me"] });
              }}
            />
          ) : (
            <button type="button" onClick={connect} disabled={busy || waiting} className={btn.primary}>
              {busy ? "正在打开…" : waiting ? "等待在 Telegram 里完成…" : "连接 Telegram"}
            </button>
          )}
        </span>
      </div>

      {attempt && waiting && !linked && (
        <div className="mt-3 rounded border border-line bg-bg px-3 py-2 text-sm">
          <p>已在新窗口打开 Telegram 机器人，请在 Telegram 里点“开始”。完成后这里会自动更新。</p>
          <p className="mt-1 text-muted">
            没有自动打开？
            <a href={attempt.url} target="_blank" rel="noopener noreferrer" className="text-teal hover:underline">
              点这里打开机器人
            </a>
          </p>
          {state?.detail && <p className="mt-1 text-muted">{state.detail}</p>}
        </div>
      )}
      {status === "linked" && (
        <p className="mt-3 text-sm text-teal">已连接 Telegram。{state?.detail}</p>
      )}
      {(status === "failed" || status === "expired" || status === "timeout") && (
        <p className="mt-3 text-sm text-danger">
          {status === "failed" ? "连接失败。" : "连接请求已过期，请重新点“连接 Telegram”。"}
          {state?.detail}
        </p>
      )}
      <ErrorText>{error}</ErrorText>
    </Section>
  );
}
