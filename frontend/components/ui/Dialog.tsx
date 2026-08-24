"use client";
import { useEffect, useId, useRef, type ReactNode } from "react";

export interface DialogProps {
  open: boolean;
  title: string;
  onClose: () => void;
  children: ReactNode;
  /** Кнопки действий; закрытие всегда доступно и без них — Esc и клик вне окна. */
  footer?: ReactNode;
}

/**
 * Модальное окно на нативном <dialog>: браузер сам держит фокус внутри, гасит
 * фон и обрабатывает Esc — своя реализация ловушки фокуса тут была бы хуже.
 * Фокус после закрытия возвращается на элемент, с которого окно открыли.
 */
export default function Dialog({ open, title, onClose, children, footer }: DialogProps) {
  const ref = useRef<HTMLDialogElement>(null);
  const opener = useRef<Element | null>(null);
  const titleId = useId();

  useEffect(() => {
    const dialog = ref.current;
    if (!dialog) return;

    if (open && !dialog.open) {
      opener.current = document.activeElement;
      // jsdom не реализует showModal — в тестах довольствуемся обычным показом.
      if (typeof dialog.showModal === "function") dialog.showModal();
      else dialog.setAttribute("open", "");
    }
    if (!open && dialog.open) {
      dialog.close();
      if (opener.current instanceof HTMLElement) opener.current.focus();
    }
  }, [open]);

  return (
    <dialog
      ref={ref}
      aria-labelledby={titleId}
      onCancel={(e) => {
        e.preventDefault();
        onClose();
      }}
      onClick={(e) => {
        // Клик пришёл ровно в подложку — значит, мимо содержимого окна.
        if (e.target === ref.current) onClose();
      }}
      className="m-auto w-[min(32rem,calc(100vw-2rem))] rounded-2xl border border-zinc-200 bg-white p-0 text-[#1c1d20] backdrop:bg-black/40"
    >
      <div className="flex flex-col gap-4 p-6">
        <h2 id={titleId} className="text-lg tracking-tight">
          {title}
        </h2>
        <div className="text-sm text-zinc-600">{children}</div>
        {footer && <div className="flex justify-end gap-2">{footer}</div>}
      </div>
    </dialog>
  );
}
