import { expect, it } from "vitest";
import type { AdminMember, Handover } from "./api";
import {
  assignAll,
  buildHandoverPlan,
  handoverCandidates,
  handoverFromConflict,
  keepAssignments,
} from "./handover";

const h: Handover = {
  member_id: 7,
  account: "bob",
  issues: [
    { id: 1, title: "登录失败", status: "OPEN", status_label: "待接单", as: "owner", version: 3 },
    { id: 1, title: "登录失败", status: "OPEN", status_label: "待接单", as: "source", version: 3 },
    { id: 2, title: "超时", status: "FIXING", status_label: "修复中", as: "owner", version: 5 },
  ],
  services: [{ name: "payments" }],
};

const member = (over: Partial<AdminMember>): AdminMember => ({
  member_id: 1,
  account: "a",
  role: "member",
  status: "active",
  disabled: false,
  telegram_linked: false,
  devices: 0,
  has_password: true,
  pending_issues: 0,
  owned_services: 0,
  ...over,
});

it("接手人候选只含本空间在册、未停用、不是被移出的人", () => {
  const list = [
    member({ member_id: 1, account: "ann" }),
    member({ member_id: 7, account: "bob" }),
    member({ member_id: 8, account: "cat", disabled: true }),
    member({ member_id: 9, account: "dan", status: "removed" }),
  ];
  expect(handoverCandidates(list, 7).map((m) => m.account)).toEqual(["ann"]);
});

it("“全部转给”把每张单（负责人、发起人分开算）和每个服务都设成同一个人", () => {
  expect(assignAll(h, "3")).toEqual({
    "issue:1:owner": "3",
    "issue:1:source": "3",
    "issue:2:owner": "3",
    "service:payments": "3",
  });
});

it("有条目没选接手人时不提交，并说明缺几项", () => {
  expect(buildHandoverPlan(h, { "issue:1:owner": "3" })).toEqual({ missing: 3 });
  expect(buildHandoverPlan(h, { ...assignAll(h, "3"), "service:payments": "" })).toEqual({ missing: 1 });
});

it("完整时生成转交计划，带上每张单的期望版本", () => {
  const a = { ...assignAll(h, "3"), "issue:2:owner": "4" };
  expect(buildHandoverPlan(h, a)).toEqual({
    plan: {
      issues: [
        { id: 1, as: "owner", to_member_id: 3, expected_version: 3 },
        { id: 1, as: "source", to_member_id: 3, expected_version: 3 },
        { id: 2, as: "owner", to_member_id: 4, expected_version: 5 },
      ],
      services: [{ name: "payments", to_member_id: 3 }],
    },
  });
});

it("没有任何条目时计划为空（直接移出）", () => {
  const empty: Handover = { member_id: 7, account: "bob", issues: [], services: [] };
  expect(buildHandoverPlan(empty, {})).toEqual({ plan: { issues: [], services: [] } });
});

it("服务端返回最新清单后，保留仍存在条目的选择，新条目留空，消失的条目丢掉", () => {
  const latest: Handover = {
    ...h,
    issues: [
      { id: 2, title: "超时", status: "FIXING", status_label: "修复中", as: "owner", version: 6 },
      { id: 5, title: "新单", status: "OPEN", status_label: "待接单", as: "owner", version: 1 },
    ],
  };
  const kept = keepAssignments(latest, { ...assignAll(h, "3"), "issue:2:owner": "4" });
  expect(kept).toEqual({ "issue:2:owner": "4", "issue:5:owner": "", "service:payments": "3" });
  // 新版本号进入计划
  const r = buildHandoverPlan(latest, { ...kept, "issue:5:owner": "3" });
  expect("plan" in r && r.plan.issues[0].expected_version).toBe(6);
});

it("只认结构正确的 409 清单", () => {
  expect(handoverFromConflict({ handover: h })).toBe(h);
  expect(handoverFromConflict({})).toBeNull();
  expect(handoverFromConflict({ handover: { issues: "x" } })).toBeNull();
});
