// @vitest-environment jsdom
import { afterEach, beforeAll, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { InviteLinkDialog } from "./Admin";

beforeAll(() => {
  HTMLDialogElement.prototype.showModal = function (this: HTMLDialogElement) {
    this.setAttribute("open", "");
  };
  HTMLDialogElement.prototype.close = function (this: HTMLDialogElement) {
    if (!this.hasAttribute("open")) return;
    this.removeAttribute("open");
    this.dispatchEvent(new Event("close"));
  };
});

afterEach(cleanup);

it("一次性邀请链接对话框不能按 Esc 关闭，只能点“我已复制”", () => {
  const done = vi.fn();
  render(<InviteLinkDialog url="https://cohive.example.com/i/abc" onDone={done} />);
  const dialog = document.querySelector("dialog")!;
  expect(dialog.hasAttribute("open")).toBe(true);
  const ev = new Event("cancel", { cancelable: true });
  dialog.dispatchEvent(ev);
  if (!ev.defaultPrevented) dialog.close();
  expect(dialog.hasAttribute("open")).toBe(true);
  expect(done).not.toHaveBeenCalled();
  fireEvent.click(screen.getByText("我已复制"));
  expect(done).toHaveBeenCalledTimes(1);
});
