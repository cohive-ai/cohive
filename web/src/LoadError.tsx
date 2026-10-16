import { useRouter, type ErrorComponentProps } from "@tanstack/react-router";
import { Notice } from "./components";
import { btn } from "./ui";

// 页面加载失败（例如进入前确认登录态时网络中断）。“重试”要让路由重新执行加载，
// 只清掉错误状态的话，进入前的检查不会再跑，页面会一直停在这里。
export function LoadError({ error, reset }: ErrorComponentProps) {
  const router = useRouter();
  return (
    <div className="mx-auto max-w-md px-4 py-16">
      <Notice tone="danger">页面加载失败：{error instanceof Error ? error.message : String(error)}</Notice>
      <button
        type="button"
        onClick={() => {
          reset();
          void router.invalidate();
        }}
        className={`${btn.secondary} mt-3`}
      >
        重试
      </button>
    </div>
  );
}
