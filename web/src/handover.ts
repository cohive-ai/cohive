// 移出 / 改派时的转交清单：每张单、每个服务各选一个接手人。单独放出来便于测试。
import type { AdminMember, Handover, HandoverPlan } from "./api";

export type Assignments = Record<string, string>; // 条目键 → 接手人成员编号（字符串，空表示未选）

export const issueKey = (it: { id: number; as: string }) => `issue:${it.id}:${it.as}`;
export const serviceKey = (sv: { name: string }) => `service:${sv.name}`;

export function handoverKeys(h: Handover): string[] {
  return [...h.issues.map(issueKey), ...h.services.map(serviceKey)];
}

// 接手人候选：本空间在册、未停用、不是被移出（被改派）的人
export function handoverCandidates(members: AdminMember[], targetMemberId: number): AdminMember[] {
  return members.filter((m) => m.status === "active" && !m.disabled && m.member_id !== targetMemberId);
}

// 顶部“全部转给”：把每一项都设成同一个人
export function assignAll(h: Handover, toMemberId: string): Assignments {
  return Object.fromEntries(handoverKeys(h).map((k) => [k, toMemberId]));
}

// 服务端返回了最新清单（409）时，保留仍然存在的条目已选的接手人，新出现的条目留空
export function keepAssignments(h: Handover, prev: Assignments): Assignments {
  return Object.fromEntries(handoverKeys(h).map((k) => [k, prev[k] ?? ""]));
}

// 生成提交给服务端的转交计划；有条目没选接手人时返回缺几项
export function buildHandoverPlan(h: Handover, a: Assignments): { plan: HandoverPlan } | { missing: number } {
  const pick = (k: string) => {
    const v = Number(a[k]);
    return Number.isInteger(v) && v > 0 ? v : 0;
  };
  const missing = handoverKeys(h).filter((k) => !pick(k)).length;
  if (missing > 0) return { missing };
  return {
    plan: {
      issues: h.issues.map((it) => ({
        id: it.id,
        as: it.as,
        to_member_id: pick(issueKey(it)),
        expected_version: it.version,
      })),
      services: h.services.map((sv) => ({ name: sv.name, to_member_id: pick(serviceKey(sv)) })),
    },
  };
}

// 409 响应里带的最新清单
export function handoverFromConflict(body: Record<string, unknown>): Handover | null {
  const h = body.handover as Handover | undefined;
  if (!h || typeof h !== "object" || !Array.isArray(h.issues) || !Array.isArray(h.services)) return null;
  return h;
}
