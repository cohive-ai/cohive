// @vitest-environment jsdom
import { afterEach, expect, it } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import i18n from "../i18n";
import { ChangePasswordForm } from "./Password";

afterEach(cleanup);

const show = (hasPassword = true) =>
  render(
    <QueryClientProvider client={new QueryClient()}>
      <ChangePasswordForm hasPassword={hasPassword} />
    </QueryClientProvider>,
  );

it("改密码表单：新密码过短与两次不一致的提示（中文）", () => {
  show();
  fireEvent.change(screen.getByLabelText("新密码"), { target: { value: "abc" } });
  expect(screen.getByText("新密码至少 10 位。")).toBeTruthy();
  fireEvent.change(screen.getByLabelText("新密码"), { target: { value: "abcdefghij" } });
  fireEvent.change(screen.getByLabelText("确认新密码"), { target: { value: "x" } });
  expect(screen.getByText("两次输入的新密码不一致。")).toBeTruthy();
});

it("英文界面：改密码表单文案", async () => {
  await i18n.changeLanguage("en");
  try {
    show(false);
    expect(screen.getByText(/You haven't set a password yet/)).toBeTruthy();
    fireEvent.change(screen.getByLabelText("New password"), { target: { value: "abc" } });
    expect(screen.getByText("The new password must be at least 10 characters.")).toBeTruthy();
    fireEvent.change(screen.getByLabelText("New password"), { target: { value: "abcdefghij" } });
    fireEvent.change(screen.getByLabelText("Confirm new password"), { target: { value: "x" } });
    expect(screen.getByText("The two new passwords don't match.")).toBeTruthy();
    expect(screen.getByText("Change password")).toBeTruthy();
  } finally {
    await i18n.changeLanguage("zh-CN");
  }
});
