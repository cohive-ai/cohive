// @vitest-environment jsdom
import { afterEach, beforeAll, expect, it } from "vitest";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import i18n from "./i18n";
import { ConfirmButton } from "./ui";

// jsdom 没有实现模态对话框，按浏览器的行为补上：showModal 打开；按 Esc 先发可取消的 cancel 事件，
// 没被阻止才关闭并发 close 事件。
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

afterEach(async () => {
  cleanup();
  await i18n.changeLanguage("zh-CN");
});

function pressEsc(d: HTMLDialogElement) {
  const ev = new Event("cancel", { cancelable: true });
  d.dispatchEvent(ev);
  if (!ev.defaultPrevented) d.close();
}

it("确认对话框处理中按 Esc 不会关闭", async () => {
  let finish: () => void = () => {};
  render(
    <ConfirmButton
      label="吊销"
      title="吊销这台设备？"
      message="确认吗"
      action={() => new Promise<void>((r) => (finish = r))}
    />,
  );
  fireEvent.click(screen.getByText("吊销"));
  const dialog = document.querySelector("dialog")!;
  expect(dialog.hasAttribute("open")).toBe(true);
  fireEvent.click(screen.getByText("吊销", { selector: "dialog button" }));
  expect(await screen.findByText("正在处理…")).toBeTruthy();
  pressEsc(dialog);
  expect(dialog.hasAttribute("open")).toBe(true);
  await act(async () => finish());
  expect(dialog.hasAttribute("open")).toBe(false);
});

it("确认对话框空闲时按 Esc 可以关闭", () => {
  render(<ConfirmButton label="作废" title="作废邀请？" message="确认吗" action={async () => {}} />);
  fireEvent.click(screen.getByText("作废"));
  const dialog = document.querySelector("dialog")!;
  pressEsc(dialog);
  expect(dialog.hasAttribute("open")).toBe(false);
});

it("确认对话框的取消与处理中文案随语言切换", async () => {
  await i18n.changeLanguage("en");
  let finish: () => void = () => {};
  render(<ConfirmButton label="Revoke" title="Revoke?" message="Sure?" action={() => new Promise<void>((r) => (finish = r))} />);
  fireEvent.click(screen.getByText("Revoke"));
  expect(screen.getByText("Cancel")).toBeTruthy();
  fireEvent.click(screen.getByText("Revoke", { selector: "dialog button" }));
  expect(await screen.findByText("Working…")).toBeTruthy();
  await act(async () => finish());
});
