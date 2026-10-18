// 网页接口层。鉴权用协作服务设置的会话 Cookie（HttpOnly，前端读不到也不保存任何令牌）。
import i18n from "./i18n";

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
  // 服务端按界面语言回报错
  const headers: Record<string, string> = { Accept: "application/json", "Accept-Language": i18n.language };
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
      throw new ApiError(0, i18n.t(ctrl.signal.aborted ? "api.timeout" : "api.offline"));
    }
    if (res.status === 204) return undefined as T;
    body = (await res.json().catch(() => ({}))) as Record<string, unknown>;
    if (ctrl.signal.aborted) throw new ApiError(0, i18n.t("api.timeout"));
  } finally {
    clearTimeout(timer);
  }
  if (res.ok) return body as T;
  const message = typeof body.error === "string" && body.error ? body.error : i18n.t("api.failed", { status: res.status });
  if (res.status === 401 && opts.session !== false) {
    unauthorizedHandler?.();
    throw new UnauthorizedError(401, i18n.t("api.sessionExpired"), body);
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
  // 账号已被服务器管理员停用（成员资格仍在册）
  disabled?: boolean;
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

export type Role = "admin" | "member";

// 账号所在的一个工作空间及他在里面的角色
export interface WorkspaceRef {
  name: string; // 路径：出现在链接与项目配置里，不可改
  display_name?: string; // 显示名，没设时等于路径
  role: Role | string;
}

// 全局账号：一个人一个账号，可以是多个工作空间的成员
export interface Account {
  id: number;
  name: string;
  display_name?: string;
  server_admin: boolean;
  disabled: boolean;
  has_password: boolean;
  telegram?: { username?: string; linked: boolean };
  // 账号语言（网页、编程工具、命令行、会话提醒都按它）；空表示没设
  language?: string;
}

// 服务器支持的语言：名称用各语言自己的文字
export interface LanguageOption {
  code: string;
  name: string;
}

export interface Me {
  account: Account;
  must_change_password: boolean;
  // 本次登录方式：password 账号密码、device 已配对电脑免密（cohive web）、token 编程工具令牌
  login_method?: "password" | "device" | "token";
  tool: string;
  workspaces: WorkspaceRef[];
  // 本人在各通讯工具上的连接、服务器启用的通讯工具
  chat?: ChatIdentity[];
  channels?: { provider: ChatProvider }[];
  // 服务器缺省语言（账号没设语言时编程工具、命令行用它）与支持的语言
  default_language?: string;
  languages?: LanguageOption[];
}

export type ChatProvider = "telegram" | "dingtalk";

export interface ChatIdentity {
  provider: ChatProvider;
  linked: boolean; // 有用户编号，能识别他在群里点的按钮
  handle?: string;
  display_name?: string;
}

export interface LoginResult {
  account: Account;
  must_change_password: boolean;
  workspaces: WorkspaceRef[];
}

export interface InviteInfo {
  workspace: string;
  workspace_display?: string;
  kind: string;
  role?: string;
  valid: boolean;
  // 带着网页会话打开时才有
  signed_in_as?: string;
  already_member?: boolean;
}

export interface RegisterResult {
  status: "active" | "pending";
  account?: Account;
  workspace?: WorkspaceRef;
}

export interface AcceptResult {
  status: "active" | "pending";
  workspace: { name: string; display_name?: string; role?: string };
}

// 工具列表：约定里没写明是数组还是逗号分隔的字符串，两种都接受
export type Tools = string[] | string | null | undefined;

export interface DeviceAuth {
  device_name: string;
  tools: Tools;
  created_at: string;
  status: string;
  first_device: boolean;
  // 批准后这台电脑可用于的工作空间（本人在册的全部工作空间）
  workspaces?: string[];
}

export interface DeviceToken {
  workspace: string;
  tool: string;
}

export interface Device {
  id: number;
  account?: string;
  name: string;
  created_at: string;
  revoked_at?: string | null;
  last_used_at?: string | null;
  tokens?: DeviceToken[] | null;
  workspaces?: string[] | null;
}

export interface LinkStart {
  id: number | string;
  url: string;
}

// 钉钉：网页给出绑定码，私聊发给机器人，再回网页确认
export interface CodeStart {
  id: number;
  code: string;
  expires_in: number;
}

export interface LinkStatus {
  // pending 等发码；awaiting_confirm 机器人已收到，等本人确认；linked、failed、expired
  status: string;
  detail?: string;
  provider?: ChatProvider;
  candidate_name?: string;
}

export interface ChannelStatus {
  provider: ChatProvider;
  bot_name?: string;
  app_id?: string;
  online: boolean;
  detail?: string;
}

export interface PendingSignup {
  id: number;
  // new 新人注册；existing 已有账号申请加入
  kind: "new" | "existing" | string;
  name: string;
  display_name?: string;
  created_at: string;
}

export interface PendingDevice {
  id: number;
  account: string;
  display_name?: string;
  device_name: string;
  tools: Tools;
  created_at: string;
  workspaces?: string[] | null;
}

export interface AdminMember {
  member_id: number;
  account: string;
  display_name?: string;
  role: string;
  // active 在册；removed 已移出
  status: string;
  disabled: boolean;
  telegram_username?: string;
  telegram_linked: boolean;
  devices: number;
  has_password: boolean;
  pending_issues: number;
  owned_services: number;
}

export interface AccountLookup {
  name: string;
  display_name?: string;
  member_status: "none" | "active" | "removed";
  disabled: boolean;
}

export interface HandoverIssue {
  id: number;
  title: string;
  status: string;
  status_label: string;
  // owner 他是负责人；source 他是发起人
  as: "owner" | "source";
  version: number;
}

export interface Handover {
  member_id: number;
  account: string;
  issues: HandoverIssue[];
  services: { name: string }[];
}

export interface HandoverPlan {
  issues: { id: number; as: "owner" | "source"; to_member_id: number; expected_version: number }[];
  services: { name: string; to_member_id: number }[];
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
  path: string;
  display_name: string;
  telegram_chat: string;
  show_title: boolean;
  show_brief: boolean;
  bot_enabled: boolean;
  // 对外地址是服务器级的：这里只读显示群消息里的链接写法；没设时为空
  external_url_set: boolean;
  issue_link_example: string;
  // 绑定的群（第一期最多一个）与服务器启用的通讯工具；telegram_chat 只在绑的是 Telegram 群时有值
  chat?: { provider: ChatProvider; chat_id: string; title?: string } | null;
  channels?: { provider: ChatProvider }[];
  // 超时催办时长（小时），0 表示关闭
  remind_open_hours?: number;
  remind_verify_hours?: number;
  // 群消息语言（空表示跟随服务器缺省语言）、服务器缺省语言与支持的语言
  language?: string;
  default_language?: string;
  languages?: LanguageOption[];
}

export interface SaveWorkspaceSettings {
  telegram_chat: string;
  show_title: boolean;
  show_brief: boolean;
  display_name: string;
  remind_open_hours?: number;
  remind_verify_hours?: number;
  // 空串表示跟随服务器缺省语言；不传则不改
  language?: string;
}

// 服务器级设置：对外地址
export interface ServerSettings {
  external_url: string;
  locked: boolean; // 由环境变量 COHIVE_EXTERNAL_URL 锁定
  suggested: string; // 本次访问用的地址，没设时用来预填
}

export interface ServerWorkspace {
  name: string;
  display_name?: string;
  created_at: string;
  members: number;
  admins: string[] | null;
  open_issues: number;
  telegram_bound: boolean;
  i_am_member: boolean;
}

export interface ServerAccount {
  name: string;
  display_name?: string;
  server_admin: boolean;
  disabled: boolean;
  telegram_linked: boolean;
  dingtalk_linked?: boolean;
  devices: number;
  workspaces: { name: string; role: string; status: string }[] | null;
}

// 管理操作记录
export interface AdminEvent {
  id: number;
  at: string;
  via: string;
  via_label: string;
  actor: string;
  workspace: string;
  action: string;
  action_label: string;
  category: string;
  target_kind: string;
  target: string;
  text: string; // 服务端写好的人话
  detail: { label: string; value: string }[] | null;
}

export interface Option {
  id: string;
  label: string;
}

export interface AdminEventPage {
  items: AdminEvent[];
  next_before: number | null;
  types: Option[];
  vias: Option[];
}

export interface EventFilter {
  type?: string;
  actor?: string;
  via?: string;
  workspace?: string; // 只用于服务器管理页
  before?: number;
}

function eventQuery(f: EventFilter): string {
  const qs = new URLSearchParams();
  if (f.type) qs.set("type", f.type);
  if (f.actor?.trim()) qs.set("actor", f.actor.trim());
  if (f.via) qs.set("via", f.via);
  if (f.workspace?.trim()) qs.set("workspace", f.workspace.trim());
  if (f.before) qs.set("before", String(f.before));
  const s = qs.toString();
  return s ? `?${s}` : "";
}

// 工作空间内接口的前缀
const w = (ws: string) => `/api/v1/w/${seg(ws)}`;
const adm = (ws: string) => `${w(ws)}/admin`;
const srv = "/api/v1/server";

// 协作服务版本与它附带的命令行安装包；cli 为 null 表示构建时没有附带安装包
export interface ServerVersion {
  version: string;
  commit: string;
  cli: { version: string; commit: string; platforms: string[] } | null;
}

export const api = {
  // 版本与命令行安装包（不需要登录）
  version: () => request<ServerVersion>("/api/v1/version", { session: false }),

  // 看板与问题单（工作空间内）
  board: (ws: string) => request<Board>(`${w(ws)}/board`),
  issues: (ws: string, params: { q?: string; status?: string }) => {
    const qs = new URLSearchParams();
    if (params.q) qs.set("q", params.q);
    if (params.status) qs.set("status", params.status);
    const s = qs.toString();
    return request<{ items: Issue[] }>(`${w(ws)}/issues${s ? `?${s}` : ""}`);
  },
  issue: (ws: string, id: number) => request<{ issue: Issue; events: IssueEvent[] }>(`${w(ws)}/issues/${seg(id)}`),

  // 账号
  me: () => request<Me>("/api/v1/me"),
  // 设置账号语言；空串表示清除（编程工具、命令行回到服务器缺省语言）
  setMyLanguage: (language: string) => post("/api/v1/me/language", { language }),
  login: (body: { name: string; password: string }) => post<LoginResult>("/api/v1/auth/login", body, false),
  logout: () => post("/api/v1/auth/logout", undefined, false),
  changePassword: (body: { old_password: string; new_password: string }) =>
    post("/api/v1/auth/password", body, false),

  // 邀请、注册与加入
  invite: (code: string) => request<InviteInfo>(`/api/v1/invites/${seg(code)}`, { session: false }),
  // 注册时带上当前界面语言，作为新账号的语言
  register: (body: { code: string; name: string; display_name: string; password: string }) =>
    post<RegisterResult>("/api/v1/register", { ...body, language: i18n.resolvedLanguage || i18n.language }, false),
  acceptInvite: (code: string) => post<AcceptResult>(`/api/v1/invites/${seg(code)}/accept`, {}),

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
  startDingTalkLink: () => post<CodeStart>("/api/v1/me/links", { provider: "dingtalk" }),
  confirmLink: (id: number, accept: boolean) => post(`/api/v1/me/links/${seg(id)}/confirm`, { accept }),
  unlink: (provider: ChatProvider) => del(`/api/v1/me/links/${seg(provider)}`),

  // 工作空间管理
  pending: (ws: string) => request<{ signups: PendingSignup[]; devices: PendingDevice[] }>(`${adm(ws)}/pending`),
  decideSignup: (ws: string, id: number, approve: boolean) => post(`${adm(ws)}/signups/${seg(id)}`, { approve }),
  decideDevice: (ws: string, id: number, approve: boolean) => post(`${adm(ws)}/device-auths/${seg(id)}`, { approve }),
  members: (ws: string, status?: "removed") =>
    request<{ items: AdminMember[] }>(`${adm(ws)}/members${status ? `?status=${status}` : ""}`),
  lookupAccount: (ws: string, name: string) => request<AccountLookup>(`${adm(ws)}/accounts/${seg(name)}`),
  addMember: (ws: string, account: string, role: string) =>
    post<{ member_id: number }>(`${adm(ws)}/members`, { account, role }),
  setMemberRole: (ws: string, id: number, role: string) => post(`${adm(ws)}/members/${seg(id)}`, { role }),
  handover: (ws: string, id: number) => request<Handover>(`${adm(ws)}/members/${seg(id)}/handover`),
  // remove 为 true 时改派并移出；为 false 时只改派（给已停用的人用）
  applyHandover: (ws: string, id: number, plan: HandoverPlan, remove: boolean) =>
    post(`${adm(ws)}/members/${seg(id)}/${remove ? "remove" : "handover"}`, plan),
  devices: (ws: string) => request<{ items: Device[] }>(`${adm(ws)}/devices`),
  startChatBind: (ws: string, provider: ChatProvider) => post<CodeStart>(`${adm(ws)}/chat-binds`, { provider }),
  chatBindStatus: (ws: string, id: number) => request<LinkStatus>(`${adm(ws)}/chat-binds/${seg(id)}`),
  unbindChat: (ws: string, provider: ChatProvider) => del(`${adm(ws)}/chats/${seg(provider)}`),
  revokeDevice: (ws: string, id: number) => post(`${adm(ws)}/devices/${seg(id)}/revoke`),
  invites: (ws: string) => request<{ items: Invite[] }>(`${adm(ws)}/invites`),
  createInvite: (ws: string, body: NewInvite) => post<CreatedInvite>(`${adm(ws)}/invites`, body),
  revokeInvite: (ws: string, id: number) => post(`${adm(ws)}/invites/${seg(id)}/revoke`),
  workspaceSettings: (ws: string) => request<WorkspaceSettings>(`${adm(ws)}/workspace`),
  saveWorkspaceSettings: (ws: string, body: SaveWorkspaceSettings) => post(`${adm(ws)}/workspace`, body),
  workspaceEvents: (ws: string, f: EventFilter) =>
    request<AdminEventPage>(`${adm(ws)}/events${eventQuery({ ...f, workspace: undefined })}`),

  // 服务器管理
  serverWorkspaces: () => request<{ items: ServerWorkspace[] }>(`${srv}/workspaces`),
  createWorkspace: (body: { name: string; display_name?: string; admin?: string }) => post<{ name: string }>(`${srv}/workspaces`, body),
  deleteWorkspace: (name: string) => del(`${srv}/workspaces/${seg(name)}`),
  serverAccounts: (q: string) =>
    request<{ items: ServerAccount[] }>(`${srv}/accounts${q ? `?q=${encodeURIComponent(q)}` : ""}`),
  setServerAdmin: (name: string, on: boolean) => post(`${srv}/accounts/${seg(name)}`, { server_admin: on }),
  disableAccount: (name: string) => post(`${srv}/accounts/${seg(name)}/disable`, {}),
  enableAccount: (name: string) => post(`${srv}/accounts/${seg(name)}/enable`, {}),
  serverUnlinkTelegram: (name: string) => del(`${srv}/accounts/${seg(name)}/telegram`),
  serverUnlink: (name: string, provider: ChatProvider) => del(`${srv}/accounts/${seg(name)}/links/${seg(provider)}`),
  serverPendingDevices: () => request<{ items: PendingDevice[] }>(`${srv}/pending-devices`),
  serverDecideDevice: (id: number, approve: boolean) => post(`${srv}/device-auths/${seg(id)}`, { approve }),
  serverSettings: () => request<ServerSettings>(`${srv}/settings`),
  serverChannels: () => request<{ channels: ChannelStatus[] }>(`${srv}/channels`),
  saveServerSettings: (external_url: string) => post<ServerSettings>(`${srv}/settings`, { external_url }),
  serverEvents: (f: EventFilter) => request<AdminEventPage>(`${srv}/events${eventQuery(f)}`),
};

// 接口要求账号密码登录的网页会话（当前是 cohive web 免密会话）
export function needsPasswordLogin(err: unknown): boolean {
  return err instanceof ApiError && err.status === 403 && err.body.need_password_login === true;
}
