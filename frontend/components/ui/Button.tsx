"use client";
import { forwardRef, type ButtonHTMLAttributes } from "react";

export type ButtonVariant = "primary" | "secondary" | "ghost" | "danger";

// Иерархия та же, что во входе (AuthGate): чернильная заливка — главное
// действие экрана, всё остальное визуально подчинено ей.
const VARIANT: Record<ButtonVariant, string> = {
  primary: "bg-ink text-paper hover:opacity-90",
  secondary: "border border-black/[0.08] bg-white text-ink hover:border-black/[0.16] hover:bg-paper",
  ghost: "text-ink-faint hover:bg-black/[0.05] hover:text-ink",
  danger: "border border-compromise/25 bg-white text-compromise hover:bg-compromise/10",
};

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  /** Асинхронное действие в работе: кнопка блокируется, но текст остаётся. */
  loading?: boolean;
}

const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { variant = "primary", loading = false, disabled, className = "", children, ...rest },
  ref,
) {
  return (
    <button
      ref={ref}
      // Текст не подменяем спиннером: скринридер должен продолжать читать,
      // какое именно действие выполняется, а aria-busy сообщает, что оно идёт.
      aria-busy={loading || undefined}
      disabled={disabled || loading}
      className={[
        "inline-flex min-h-11 items-center justify-center gap-2 rounded-lg px-4 text-sm",
        "cursor-pointer transition-[opacity,background-color,border-color] duration-150 ease-out",
        "disabled:cursor-not-allowed disabled:opacity-50",
        VARIANT[variant],
        className,
      ].join(" ")}
      {...rest}
    >
      {children}
      {loading && (
        <span aria-hidden className="h-1 w-1 animate-pulse rounded-full bg-current" />
      )}
    </button>
  );
});

export default Button;
