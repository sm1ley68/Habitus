"use client";
import { forwardRef, type ButtonHTMLAttributes } from "react";

export type ButtonVariant = "primary" | "secondary" | "ghost" | "danger";

// Иерархия та же, что во входе (AuthGate): чернильная заливка — главное
// действие экрана, всё остальное визуально подчинено ей.
const VARIANT: Record<ButtonVariant, string> = {
  primary: "bg-[#1c1d20] text-white hover:opacity-90",
  secondary: "border border-zinc-200 bg-white text-[#1c1d20] hover:border-zinc-300 hover:bg-zinc-50",
  ghost: "text-zinc-500 hover:bg-zinc-100 hover:text-[#1c1d20]",
  danger: "border border-[#e4c9c2] bg-white text-[#b25e4a] hover:bg-[#f6ece9]",
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
