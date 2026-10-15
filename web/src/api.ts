// 看板使用的只读接口。令牌保存在本机浏览器里，只用于向本服务鉴权。

const TOKEN_KEY = "cohive.token";

export function getToken(): string | null {
  try {
    return localStorage.getItem(TOKEN_KEY);
  } catch {
    return null;
  }
}

export function setToken(token: string) {
  try {
    localStorage.setItem(TOKEN_KEY, token);
  } catch {
    // 无法保存时只在本次会话内有效
  }
  memoryToken = token;
}

export function clearToken() {
  try {
    localStorage.removeItem(TOKEN_KEY);
  } catch {
    // 忽略
  }
  memoryToken = null;
}

let memoryToken: string | null = null;

export class UnauthorizedError extends Error {}

async function request<T>(path: string, token?: string): Promise<T> {
  const t = token ?? memoryToken ?? getToken();
  const res = await fetch(path, { headers: t ? { Authorization: `Bearer ${t}` } : {} });
  if (res.status === 401) throw new UnauthorizedError("令牌无效或已吊销");
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(body.error ?? `请求失败（${res.status}）`);
  return body as T;
}

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
  tool: string;
}

export const api = {
  me: (token?: string) => request<Me>("/api/v1/me", token),
  board: () => request<Board>("/api/v1/board"),
  issues: (params: { q?: string; status?: string }) => {
    const qs = new URLSearchParams();
    if (params.q) qs.set("q", params.q);
    if (params.status) qs.set("status", params.status);
    const s = qs.toString();
    return request<{ items: Issue[] }>(`/api/v1/issues${s ? `?${s}` : ""}`);
  },
  issue: (id: number) => request<{ issue: Issue; events: IssueEvent[] }>(`/api/v1/issues/${id}`),
};
