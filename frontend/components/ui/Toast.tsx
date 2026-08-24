"use client";
import {
  createContext, useCallback, useContext, useEffect, useMemo, useRef, useState,
  type ReactNode,
} from "react";

export type ToastTone = "ok" | "error";

interface Toast {
  id: number;
  tone: ToastTone;
  text: string;
}

interface ToastApi {
  /** Короткое подтверждение выполненного действия или причина отказа. */
  show: (text: string, tone?: ToastTone) => void;
}

const ToastContext = createContext<ToastApi | null>(null);

const DISMISS_MS = 4000;

const TONE: Record<ToastTone, string> = {
  ok: "border-[#cfe7da] bg-[#e9f5ee] text-[#2f8f5f]",
  error: "border-[#e4c9c2] bg-[#f6ece9] text-[#b25e4a]",
};

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const nextId = useRef(0);
  const timers = useRef<ReturnType<typeof setTimeout>[]>([]);

  const show = useCallback((text: string, tone: ToastTone = "ok") => {
    const id = nextId.current++;
    setToasts((current) => [...current, { id, tone, text }]);
    timers.current.push(
      setTimeout(() => setToasts((current) => current.filter((t) => t.id !== id)), DISMISS_MS),
    );
  }, []);

  useEffect(() => () => timers.current.forEach(clearTimeout), []);

  const api = useMemo(() => ({ show }), [show]);

  return (
    <ToastContext.Provider value={api}>
      {children}
      {/* polite и без фокуса: сообщение зачитывается, но не выдёргивает
          пользователя из того, что он делает. */}
      <div
        role="status"
        aria-live="polite"
        className="pointer-events-none fixed bottom-6 left-1/2 z-50 flex -translate-x-1/2 flex-col items-center gap-2"
      >
        {toasts.map((toast) => (
          <div
            key={toast.id}
            className={`rounded-lg border px-4 py-2.5 text-sm shadow-[0_12px_32px_-20px_rgba(28,29,32,0.5)] ${TONE[toast.tone]}`}
          >
            {toast.text}
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}

export function useToast(): ToastApi {
  const api = useContext(ToastContext);
  if (!api) throw new Error("useToast вызван вне ToastProvider");
  return api;
}
