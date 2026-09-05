"use client";
import { forwardRef, type InputHTMLAttributes } from "react";

// Оформление поля — то же, что во входе (AuthGate), чтобы кабинет и вход
// читались как один продукт. Высота 44px — минимальная область касания.
export const fieldClass =
  "min-h-11 w-full rounded-lg border border-black/[0.08] bg-white px-3 py-2 text-ink " +
  "outline-none transition-colors placeholder:text-ink-faint focus:border-accent " +
  "disabled:cursor-not-allowed disabled:bg-paper disabled:opacity-60 " +
  "aria-[invalid=true]:border-compromise";

const Input = forwardRef<HTMLInputElement, InputHTMLAttributes<HTMLInputElement>>(
  function Input({ className = "", ...rest }, ref) {
    return <input ref={ref} className={`${fieldClass} ${className}`} {...rest} />;
  },
);

export default Input;
