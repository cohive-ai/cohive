// @vitest-environment jsdom
import { afterEach, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import "./i18n";
import { WorkspaceSwitcher } from "./components";

afterEach(cleanup);

const list = [
  { name: "pay", role: "member" },
  { name: "old", role: "admin", archived: true },
];

it("切换器隐藏已归档的工作空间", () => {
  render(<WorkspaceSwitcher workspaces={list} current="pay" onChange={() => {}} />);
  expect(screen.getByRole("option", { name: /pay/ })).toBeTruthy();
  expect(screen.queryByRole("option", { name: /old/ })).toBeNull();
});

it("正看着已归档的工作空间时仍显示它并标出已归档", () => {
  render(<WorkspaceSwitcher workspaces={list} current="old" onChange={() => {}} />);
  expect(screen.getByRole("option", { name: /old.*已归档/ })).toBeTruthy();
});
