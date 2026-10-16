export const actionLabel: Record<string, string> = {
  draft: "起草",
  submit: "发出",
  accept: "接单",
  decline: "退回",
  transfer: "转交",
  start: "开始修复",
  request_verification: "请求验证",
  close: "关闭",
  reopen: "重开",
  comment: "评论",
};

export const statusOptions: { value: string; label: string }[] = [
  { value: "", label: "全部状态" },
  { value: "DRAFT", label: "草稿" },
  { value: "OPEN", label: "待接单" },
  { value: "ACCEPTED", label: "已接单" },
  { value: "FIXING", label: "修复中" },
  { value: "READY_FOR_VERIFICATION", label: "待验证" },
  { value: "REOPENED", label: "已重开" },
  { value: "CLOSED", label: "已关闭" },
  { value: "DECLINED", label: "已退回" },
];

export const statusLabel = Object.fromEntries(statusOptions.map((o) => [o.value, o.label]));

export const severityLabel: Record<string, string> = {
  low: "低",
  normal: "普通",
  high: "高",
  critical: "紧急",
};

export const toolLabel = (tool: string) =>
  tool === "claude-code" ? "Claude Code" : tool === "codex" ? "Codex" : tool === "web" ? "网页" : tool;

// 设备的工具列表：兼容数组与逗号分隔字符串
export function toolsText(tools: string[] | string | null | undefined): string {
  const list = Array.isArray(tools) ? tools : (tools ?? "").split(",");
  const names = list.map((t) => t.trim()).filter(Boolean).map(toolLabel);
  return names.length ? names.join("、") : "未注明";
}

export const roleLabel = (role: string) => (role === "admin" ? "管理员" : "成员");

export const inviteKindLabel = (kind: string) =>
  kind === "personal" ? "个人邀请" : kind === "team" ? "团队邀请" : kind;

export const deviceAuthStatusLabel: Record<string, string> = {
  pending: "等待你确认",
  awaiting_admin: "等待管理员批准",
  approved: "已授权",
  delivered: "已授权，终端已完成登录",
  denied: "已拒绝",
  expired: "已过期",
};

// 成员名规则：小写字母、数字、下划线、横线，1 到 32 位
export const memberNamePattern = /^[a-z0-9_-]{1,32}$/;
export const minPasswordLength = 10;

// 登录后回跳地址：只接受本站路径，避免被拼成跳去外站
export function safeRedirect(target: string | undefined, fallback = "/"): string {
  if (!target || !target.startsWith("/") || target.startsWith("//") || target.startsWith("/\\")) return fallback;
  if (target === "/login" || target.startsWith("/login?")) return fallback;
  return target;
}

export function relativeTime(iso: string, now = Date.now()): string {
  const sec = Math.max(0, Math.round((now - new Date(iso).getTime()) / 1000));
  if (sec < 10) return "刚刚";
  if (sec < 60) return `${sec} 秒前`;
  const min = Math.round(sec / 60);
  if (min < 60) return `${min} 分钟前`;
  const hr = Math.round(min / 60);
  if (hr < 24) return `${hr} 小时前`;
  return `${Math.round(hr / 24)} 天前`;
}

export function absoluteTime(iso: string): string {
  const d = new Date(iso);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}
