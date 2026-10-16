import { expect, it } from "vitest";
import { memberNamePattern } from "./format";

// 与服务端规则一致：小写字母、数字、下划线、横线，1 到 32 位，首字符为字母或数字。
it("成员名规则与服务端一致", () => {
  for (const ok of ["a", "lee", "lee-codex", "ann_dev", "9lives", "a".repeat(32)]) {
    expect(memberNamePattern.test(ok), ok).toBe(true);
  }
  for (const bad of ["", "-lee", "_lee", "Lee", "李", "a b", "a".repeat(33)]) {
    expect(memberNamePattern.test(bad), bad).toBe(false);
  }
});
