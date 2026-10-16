import { useEffect, useId, useRef, useState } from "react";

// 表单与对话框的通用件：颜色只用 styles.css 里的令牌

export const btn = {
  primary:
    "inline-flex items-center justify-center rounded bg-teal px-3 py-2 text-sm font-medium text-surface hover:opacity-90 disabled:opacity-50",
  secondary:
    "inline-flex items-center justify-center rounded border border-line bg-surface px-3 py-1.5 text-sm text-ink hover:bg-sunken disabled:opacity-50",
  danger:
    "inline-flex items-center justify-center rounded border border-line bg-surface px-3 py-1.5 text-sm text-danger hover:bg-sunken disabled:opacity-50",
  dangerSolid:
    "inline-flex items-center justify-center rounded bg-danger px-3 py-2 text-sm font-medium text-surface hover:opacity-90 disabled:opacity-50",
};

export const inputClass =
  "w-full rounded border border-line bg-bg px-3 py-2 text-sm text-ink placeholder:text-muted disabled:opacity-60";

export function Field({
  label,
  hint,
  children,
  htmlFor,
}: {
  label: string;
  hint?: React.ReactNode;
  children: React.ReactNode;
  htmlFor: string;
}) {
  return (
    <div>
      <label htmlFor={htmlFor} className="mb-1 block text-sm">
        {label}
      </label>
      {children}
      {hint && <p className="mt-1 text-xs text-muted">{hint}</p>}
    </div>
  );
}

export function Section({
  title,
  description,
  children,
  actions,
}: {
  title: string;
  description?: React.ReactNode;
  children: React.ReactNode;
  actions?: React.ReactNode;
}) {
  return (
    <section className="rounded-lg border border-line bg-surface p-4 sm:p-5">
      <div className="mb-3 flex flex-wrap items-start gap-2">
        <div className="min-w-0 flex-1">
          <h2 className="font-semibold">{title}</h2>
          {description && <p className="mt-0.5 text-sm text-muted">{description}</p>}
        </div>
        {actions}
      </div>
      {children}
    </section>
  );
}

export function ErrorText({ children }: { children: React.ReactNode }) {
  if (!children) return null;
  return (
    <p role="alert" className="mt-3 text-sm text-danger">
      {children}
    </p>
  );
}

export function SuccessText({ children }: { children: React.ReactNode }) {
  if (!children) return null;
  return (
    <p role="status" className="mt-3 text-sm text-teal">
      {children}
    </p>
  );
}

// 醒目提示：琥珀色只用于“需要你注意”
export function Callout({ children, tone = "amber" }: { children: React.ReactNode; tone?: "amber" | "teal" }) {
  const cls = tone === "amber" ? "border-amber bg-amber-soft text-ink" : "border-teal bg-teal-soft text-ink";
  return <div className={`rounded border-l-4 px-4 py-3 text-sm ${cls}`}>{children}</div>;
}

export const errMsg = (err: unknown) => (err instanceof Error ? err.message : String(err));

// 原生 dialog：自带焦点约束与 Esc 关闭
export function Dialog({
  open,
  onClose,
  title,
  children,
}: {
  open: boolean;
  onClose: () => void;
  title: string;
  children: React.ReactNode;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const titleId = useId();
  useEffect(() => {
    const d = ref.current;
    if (!d) return;
    if (open && !d.open) d.showModal();
    else if (!open && d.open) d.close();
  }, [open]);
  return (
    <dialog
      ref={ref}
      aria-labelledby={titleId}
      onClose={onClose}
      className="m-auto w-[min(calc(100vw-2rem),30rem)] rounded-lg border border-line bg-surface p-5 text-ink backdrop:bg-black/40"
    >
      {open && (
        <>
          <h2 id={titleId} className="mb-3 font-semibold">
            {title}
          </h2>
          {children}
        </>
      )}
    </dialog>
  );
}

// 带二次确认的按钮：确认后执行操作，失败时在对话框里显示原因
export function ConfirmButton({
  label,
  title,
  message,
  confirmLabel,
  danger = true,
  className,
  action,
  disabled,
}: {
  label: string;
  title: string;
  message: React.ReactNode;
  confirmLabel?: string;
  danger?: boolean;
  className?: string;
  action: () => Promise<unknown>;
  disabled?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const close = () => {
    if (busy) return;
    setOpen(false);
    setError("");
  };
  const run = async () => {
    setBusy(true);
    setError("");
    try {
      await action();
      setOpen(false);
    } catch (err) {
      setError(errMsg(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <>
      <button
        type="button"
        disabled={disabled}
        onClick={() => setOpen(true)}
        className={className ?? (danger ? btn.danger : btn.secondary)}
      >
        {label}
      </button>
      <Dialog open={open} onClose={close} title={title}>
        <div className="text-sm text-muted">{message}</div>
        <ErrorText>{error}</ErrorText>
        <div className="mt-5 flex justify-end gap-2">
          <button type="button" onClick={close} disabled={busy} className={btn.secondary}>
            取消
          </button>
          <button type="button" onClick={run} disabled={busy} className={danger ? btn.dangerSolid : btn.primary}>
            {busy ? "正在处理…" : (confirmLabel ?? label)}
          </button>
        </div>
      </Dialog>
    </>
  );
}

// 可复制的一行文字。非安全来源（局域网 http）下剪贴板接口不可用，退回到选中后复制
export function CopyField({ value, label }: { value: string; label: string }) {
  const ref = useRef<HTMLInputElement>(null);
  const id = useId();
  const [state, setState] = useState<"" | "ok" | "fail">("");
  useEffect(() => {
    if (!state) return;
    const t = setTimeout(() => setState(""), 2500);
    return () => clearTimeout(t);
  }, [state]);
  const copy = async () => {
    try {
      if (navigator.clipboard && window.isSecureContext) {
        await navigator.clipboard.writeText(value);
        setState("ok");
        return;
      }
      throw new Error("剪贴板不可用");
    } catch {
      const el = ref.current;
      if (!el) return setState("fail");
      el.focus();
      el.select();
      // 旧接口只作为兜底
      const ok = document.execCommand("copy");
      setState(ok ? "ok" : "fail");
    }
  };
  return (
    <div>
      <label htmlFor={id} className="sr-only">
        {label}
      </label>
      <div className="flex gap-2">
        <input
          id={id}
          ref={ref}
          readOnly
          value={value}
          onFocus={(e) => e.currentTarget.select()}
          className={`${inputClass} min-w-0 flex-1 font-mono`}
        />
        <button type="button" onClick={copy} className={`${btn.secondary} shrink-0`}>
          {state === "ok" ? "已复制" : "复制"}
        </button>
      </div>
      {state === "fail" && <p className="mt-1 text-xs text-danger">没能自动复制，已选中文字，请手动复制。</p>}
    </div>
  );
}

// 登录、注册等未进入站点时的居中卡片
export function AuthShell({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="flex min-h-dvh items-center justify-center px-4 py-8">
      <div className="w-full max-w-md rounded-lg border border-line bg-surface p-6">
        <div className="mb-5 flex items-center gap-2">
          <svg viewBox="0 0 32 32" className="h-6 w-6 text-teal" aria-hidden="true">
            <path d="M16 3l11 6.5v13L16 29 5 22.5v-13z" fill="currentColor" />
          </svg>
          <h1 className="text-lg font-semibold">{title}</h1>
        </div>
        {children}
      </div>
    </div>
  );
}
