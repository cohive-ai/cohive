import i18n from "./i18n";

// 文案都在调用时取当前语言，切换语言后不用重新加载。

const actionKeys = {
  draft: "action.draft",
  submit: "action.submit",
  accept: "action.accept",
  decline: "action.decline",
  transfer: "action.transfer",
  start: "action.start",
  request_verification: "action.request_verification",
  close: "action.close",
  reopen: "action.reopen",
  comment: "action.comment",
  reassign: "action.reassign",
  cancel: "action.cancel",
} as const;

// 事件动作的说法（过去式）；不认识的动作原样返回
export function actionLabel(action: string): string {
  const key = actionKeys[action as keyof typeof actionKeys];
  return key ? i18n.t(key) : action;
}

const statusKeys = {
  DRAFT: "status.DRAFT",
  OPEN: "status.OPEN",
  ACCEPTED: "status.ACCEPTED",
  FIXING: "status.FIXING",
  READY_FOR_VERIFICATION: "status.READY_FOR_VERIFICATION",
  REOPENED: "status.REOPENED",
  CLOSED: "status.CLOSED",
  DECLINED: "status.DECLINED",
  CANCELLED: "status.CANCELLED",
} as const;

export const statusIds = Object.keys(statusKeys);

// 状态名；不认识的状态用 fallback（例如服务端给的 status_label），再没有就原样返回
export function statusLabel(status: string, fallback?: string): string {
  const key = statusKeys[status as keyof typeof statusKeys];
  return key ? i18n.t(key) : (fallback ?? status);
}

// 状态筛选下拉的选项：第一项是“全部状态”
export function statusOptions(): { value: string; label: string }[] {
  return [{ value: "", label: i18n.t("status.all") }, ...statusIds.map((value) => ({ value, label: statusLabel(value) }))];
}

const severityKeys = {
  low: "severity.low",
  normal: "severity.normal",
  high: "severity.high",
  critical: "severity.critical",
} as const;

export function severityLabel(severity: string): string {
  const key = severityKeys[severity as keyof typeof severityKeys];
  return key ? i18n.t(key) : severity;
}

export const toolLabel = (tool: string) =>
  tool === "claude-code" ? "Claude Code" : tool === "codex" ? "Codex" : tool === "web" ? i18n.t("tool.web") : tool;

// 列表的连接符（中文顿号，英文逗号加空格）
export const listSeparator = () => i18n.t("list.separator");
export const joinList = (items: string[]) => items.join(listSeparator());

// 设备的工具列表：兼容数组与逗号分隔字符串
export function toolsText(tools: string[] | string | null | undefined): string {
  const list = Array.isArray(tools) ? tools : (tools ?? "").split(",");
  const names = list.map((t) => t.trim()).filter(Boolean).map(toolLabel);
  return names.length ? joinList(names) : i18n.t("tool.unspecified");
}

// 工作空间给人看的名字：显示名，没设时为路径
export const workspaceName = (w: { name: string; display_name?: string }) => w.display_name || w.name;

// 同时显示路径的写法：显示名与路径不同时写成“显示名（路径）”，避免两个显示名相同的工作空间分不清
export const workspaceLabel = (w: { name: string; display_name?: string }) =>
  w.display_name && w.display_name !== w.name
    ? i18n.t("workspace.label", { display: w.display_name, name: w.name })
    : w.name;

export const roleLabel = (role: string) => (role === "admin" ? i18n.t("role.admin") : i18n.t("role.member"));

export const inviteKindLabel = (kind: string) =>
  kind === "personal" ? i18n.t("inviteKind.personal") : kind === "team" ? i18n.t("inviteKind.team") : kind;

// 登录名规则（与服务端一致，工作空间名同样适用）：小写字母、数字、下划线、横线，1 到 32 位，第一个字符须是字母或数字
export const memberNamePattern = /^[a-z0-9][a-z0-9_-]{0,31}$/;
export const minPasswordLength = 10;

// 登录后回跳地址：只接受本站的相对路径，避免被拼成跳去外站（//evil、/\\evil、带控制字符的地址等）
export function safeRedirect(target: string | undefined | null, fallback = "/"): string {
  if (typeof target !== "string" || !target.startsWith("/")) return fallback;
  if (target.startsWith("//") || target.includes("\\")) return fallback;
  // 浏览器解析地址时会去掉制表符与换行，"/\t/evil.com" 会变成 "//evil.com"
  if (/[\u0000-\u001f\u007f]/.test(target)) return fallback;
  if (target === "/login" || target.startsWith("/login?") || target.startsWith("/login/")) return fallback;
  return target;
}

// 设备令牌按工作空间分组：[{workspace, tools: ["Claude Code", "Codex"]}]
export function tokensByWorkspace(
  tokens: { workspace: string; tool: string }[] | null | undefined,
): { workspace: string; tools: string[] }[] {
  const out: { workspace: string; tools: string[] }[] = [];
  for (const t of tokens ?? []) {
    let g = out.find((x) => x.workspace === t.workspace);
    if (!g) {
      g = { workspace: t.workspace, tools: [] };
      out.push(g);
    }
    const label = toolLabel(t.tool);
    if (!g.tools.includes(label)) g.tools.push(label);
  }
  return out;
}

// 工作空间名列表的展示：A、B；没有时给出说明
export const workspaceListText = (list: string[] | null | undefined, empty = i18n.t("list.none")) =>
  list && list.length ? joinList(list) : empty;

export const signupKindLabel = (kind: string) =>
  kind === "existing" ? i18n.t("signupKind.existing") : kind === "new" ? i18n.t("signupKind.new") : kind;

const lang = () => i18n.resolvedLanguage || i18n.language || "en";

// Intl 对象按语言缓存：看板每秒重绘一次，不必每次新建
const cache = new Map<string, Intl.RelativeTimeFormat | Intl.DateTimeFormat>();
function formatter<T extends Intl.RelativeTimeFormat | Intl.DateTimeFormat>(kind: string, make: (l: string) => T): T {
  const l = lang();
  const key = `${kind}:${l}`;
  let f = cache.get(key) as T | undefined;
  if (!f) {
    try {
      f = make(l);
    } catch {
      f = make("en"); // 语言代码不被 Intl 认识时退回英文
    }
    cache.set(key, f);
  }
  return f;
}

// 相对时间（“5 分钟前” / “5 minutes ago”），不足 10 秒写“现在”
export function relativeTime(iso: string, now = Date.now()): string {
  const rtf = formatter("rel", (l) => new Intl.RelativeTimeFormat(l, { numeric: "auto" }));
  const sec = Math.max(0, Math.round((now - new Date(iso).getTime()) / 1000));
  if (sec < 10) return i18n.t("time.justNow");
  if (sec < 60) return rtf.format(-sec, "second");
  const min = Math.round(sec / 60);
  if (min < 60) return rtf.format(-min, "minute");
  const hr = Math.round(min / 60);
  if (hr < 24) return rtf.format(-hr, "hour");
  return rtf.format(-Math.round(hr / 24), "day");
}

// 绝对时间：本地时区，格式跟随语言
export function absoluteTime(iso: string): string {
  const f = formatter("abs", (l) => new Intl.DateTimeFormat(l, { dateStyle: "medium", timeStyle: "short" }));
  return f.format(new Date(iso));
}
