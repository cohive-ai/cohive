// 网页接口层。鉴权用协作服务设置的会话 Cookie（HttpOnly，前端读不到也不保存任何令牌）。

export class ApiError extends Error {
  status: number;
  body: Record<string, unknown>;
  constructor(status: number, message: string, body: Record<string, unknown> = {}) {
    super(message);
    this.status = status;
    this.body = body;
  }
}

// 会话失效或未登录
export class UnauthorizedError extends ApiError {}

let unauthorizedHandler: (() => void) | null = null;

// 由入口设置：会话失效时统一回到登录页
export function setUnauthorizedHandler(fn: () => void) {
  unauthorizedHandler = fn;
}

// 单个请求最长等待时间
export const REQUEST_TIMEOUT_MS = 15_000;

interface RequestOptions {
  method?: "GET" | "POST" | "DELETE";
  body?: unknown;
  // 为 false 时 401 只作为普通错误交给页面提示（登录、注册、改密码），不当作会话失效
  session?: boolean;
}

async function request<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  const method = opts.method ?? "GET";
  const headers: Record<string, string> = { Accept: "application/json" };
  const init: RequestInit = { method, credentials: "same-origin", headers };
  if (method !== "GET") {
    headers["Content-Type"] = "application/json";
    init.body = JSON.stringify(opts.body ?? {});
  }
  // 15 秒没有响应就放弃，避免网络卡住时页面一直转圈
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), REQUEST_TIMEOUT_MS);
  init.signal = ctrl.signal;
  let res: Response;
  let body: Record<string, unknown>;
  try {
    try {
      res = await fetch(path, init);
    } catch {
      throw new ApiError(0, ctrl.signal.aborted ? "请求超时，请检查网络后重试" : "无法连接协作服务，请检查网络后重试");
    }
    if (res.status === 204) return undefined as T;
    body = (await res.json().catch(() => ({}))) as Record<string, unknown>;
    if (ctrl.signal.aborted) throw new ApiError(0, "请求超时，请检查网络后重试");
  } finally {
    clearTimeout(timer);
  }
  if (res.ok) return body as T;
  const message = typeof body.error === "string" && body.error ? body.error : `请求失败（${res.status}）`;
  if (res.status === 401 && opts.session !== false) {
    unauthorizedHandler?.();
    throw new UnauthorizedError(401, "登录已失效，请重新登录", body);
  }
  throw new ApiError(res.status, message, body);
}

const post = <T = void>(path: string, body?: unknown, session?: boolean) =>
  request<T>(path, { method: "POST", body, session });
const del = (path: string) => request<void>(path, { method: "DELETE" });
const seg = (v: string | number) => encodeURIComponent(String(v));

export interface Repro {
  script?: string;
  env?: string;
  expected_exit_code?: number;
}

export interface Issue {
  id: number;
  version: number;
  status: string;
  status_label: string;
  category: "draft" | "in_progress" | "done";
  title: string;
  summary?: string;
  expected?: string;
  actual?: string;
  repro: Repro;
  evidence?: string;
  repo?: string;
  branch?: string;
  commit?: string;
  target_service?: string;
  severity: string;
  source: string;
  source_tool: string;
  owner?: string;
  reopen_count: number;
  escalated: boolean;
  escalation_reason?: string;
  your_actions: string[];
  created_at: string;
  updated_at: string;
}

export interface BoardIssue extends Issue {
  holder?: string;
  needs: string;
}

export interface Member {
  name: string;
  display_name?: string;
}

export interface Board {
  me: string;
  members: Member[];
  issues: BoardIssue[];
}

export interface IssueEvent {
  at: string;
  actor: string;
  tool: string;
  action: string;
  from?: string;
  to?: string;
  note?: string;
  payload?: Record<string, unknown>;
}

export interface Me {
  member: string;
  display_name?: string;
  workspace: string;
  role: string;
  tool: string;
  must_change_password: boolean;
  has_password: boolean;
  telegram?: { username?: string; linked: boolean };
  // 本次登录方式：password 账号密码、device 已配对电脑免密（cohive web）、token 编程工具令牌
  login_method?: "password" | "device" | "token";
}

export interface LoginResult {
  member: string;
  workspace: string;
  role: string;
  must_change_password: boolean;
}

export interface InviteInfo {
  workspace: string;
  kind: string;
  valid: boolean;
}

export interface RegisterResult {
  status: "active" | "pending";
  member?: string;
}

// 工具列表：约定里没写明是数组还是逗号分隔的字符串，两种都接受
export type Tools = string[] | string | null | undefined;

export interface DeviceAuth {
  device_name: string;
  tools: Tools;
  created_at: string;
  status: string;
  first_device: boolean;
}

export interface Device {
  id: number;
  name: string;
  created_at: string;
  revoked_at?: string | null;
  tools: Tools;
}

export interface AdminDevice extends Device {
  member: string;
}

export interface LinkStart {
  id: number | string;
  url: string;
}

export interface LinkStatus {
  status: string;
  detail?: string;
}

export interface PendingSignup {
  id: number;
  name: string;
  display_name?: string;
  created_at: string;
}

export interface PendingDevice {
  id: number;
  member: string;
  device_name: string;
  tools: Tools;
  created_at: string;
}

export interface AdminMember {
  id: number;
  name: string;
  display_name?: string;
  role: string;
  telegram_username?: string;
  telegram_linked: boolean;
  devices: number;
  has_password: boolean;
}

export interface Invite {
  id: number;
  kind: string;
  role: string;
  max_uses?: number | null;
  uses: number;
  expires_at: string;
  revoked_at?: string | null;
  created_at: string;
}

export interface NewInvite {
  kind: "personal" | "team";
  role?: string;
  max_uses?: number;
  ttl_hours?: number;
}

export interface CreatedInvite {
  id: number;
  code: string;
  url: string;
}

export interface WorkspaceSettings {
  name: string;
  public_url: string;
  telegram_chat: string;
  show_title: boolean;
  show_brief: boolean;
  bot_enabled: boolean;
}

export const api = {
  // 看板与问题单
  board: () => request<Board>("/api/v1/board"),
  issues: (params: { q?: string; status?: string }) => {
    const qs = new URLSearchParams();
    if (params.q) qs.set("q", params.q);
    if (params.status) qs.set("status", params.status);
    const s = qs.toString();
    return request<{ items: Issue[] }>(`/api/v1/issues${s ? `?${s}` : ""}`);
  },
  issue: (id: number) => request<{ issue: Issue; events: IssueEvent[] }>(`/api/v1/issues/${id}`),

  // 账号
  me: () => request<Me>("/api/v1/me"),
  login: (body: { name: string; password: string; workspace?: string }) =>
    post<LoginResult>("/api/v1/auth/login", body, false),
  logout: () => post("/api/v1/auth/logout", undefined, false),
  changePassword: (body: { old_password: string; new_password: string }) =>
    post("/api/v1/auth/password", body, false),

  // 邀请与注册（公开）
  invite: (code: string) => request<InviteInfo>(`/api/v1/invites/${seg(code)}`, { session: false }),
  register: (body: { code: string; name: string; display_name: string; password: string }) =>
    post<RegisterResult>("/api/v1/register", body, false),

  // 设备授权
  deviceAuth: (nonce: string) => request<DeviceAuth>(`/api/v1/device/auth/${seg(nonce)}`),
  decideDeviceAuth: (nonce: string, approve: boolean) =>
    post<{ status: string }>(`/api/v1/device/auth/${seg(nonce)}`, { approve }),

  // 个人中心
  myDevices: () => request<{ items: Device[] }>("/api/v1/me/devices"),
  revokeMyDevice: (id: number) => post(`/api/v1/me/devices/${seg(id)}/revoke`),
  startLink: () => post<LinkStart>("/api/v1/me/links", { provider: "telegram" }),
  linkStatus: (id: number | string) => request<LinkStatus>(`/api/v1/me/links/${seg(id)}`),
  unlinkTelegram: () => del("/api/v1/me/links/telegram"),

  // 管理
  pending: () => request<{ signups: PendingSignup[]; devices: PendingDevice[] }>("/api/v1/admin/pending"),
  decideSignup: (id: number, approve: boolean) => post(`/api/v1/admin/signups/${seg(id)}`, { approve }),
  decideDevice: (id: number, approve: boolean) => post(`/api/v1/admin/device-auths/${seg(id)}`, { approve }),
  members: () => request<{ items: AdminMember[] }>("/api/v1/admin/members"),
  setMemberRole: (id: number, role: string) => post(`/api/v1/admin/members/${seg(id)}`, { role }),
  unlinkMemberTelegram: (id: number) => del(`/api/v1/admin/members/${seg(id)}/telegram`),
  devices: () => request<{ items: AdminDevice[] }>("/api/v1/admin/devices"),
  revokeDevice: (id: number) => post(`/api/v1/admin/devices/${seg(id)}/revoke`),
  invites: () => request<{ items: Invite[] }>("/api/v1/admin/invites"),
  createInvite: (body: NewInvite) => post<CreatedInvite>("/api/v1/admin/invites", body),
  revokeInvite: (id: number) => post(`/api/v1/admin/invites/${seg(id)}/revoke`),
  workspace: () => request<WorkspaceSettings>("/api/v1/admin/workspace"),
  saveWorkspace: (body: Omit<WorkspaceSettings, "name" | "bot_enabled">) => post("/api/v1/admin/workspace", body),
};
