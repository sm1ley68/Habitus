"use client";
import { cloneElement, useId, type ReactElement } from "react";

export interface FieldProps {
  label: string;
  /** Постоянная подсказка под полем — она объясняет, а не подменяет лейбл. */
  hint?: string;
  error?: string;
  required?: boolean;
  className?: string;
  children: ReactElement;
}

type Wired = {
  id: string;
  "aria-describedby"?: string;
  "aria-invalid"?: true;
};

/**
 * Field связывает лейбл, подсказку и ошибку с полем через id, а не «на глаз»:
 * без aria-describedby скринридер прочитает лейбл и промолчит о причине отказа.
 */
export default function Field({
  label, hint, error, required = false, className = "", children,
}: FieldProps) {
  const id = useId();
  const hintId = `${id}-hint`;
  const errorId = `${id}-error`;

  const describedBy = [hint ? hintId : null, error ? errorId : null]
    .filter(Boolean)
    .join(" ");

  const wired: Wired = { id };
  if (describedBy) wired["aria-describedby"] = describedBy;
  if (error) wired["aria-invalid"] = true;

  return (
    <div className={`flex flex-col gap-1.5 ${className}`}>
      <label htmlFor={id} className="text-sm text-zinc-500">
        {label}
        {required && <span aria-hidden className="ml-1 text-[#b25e4a]">*</span>}
      </label>

      {cloneElement(children, wired)}

      {hint && (
        <p id={hintId} className="text-xs text-zinc-400">
          {hint}
        </p>
      )}
      {/* Ошибка живёт под своим полем, а не в сводке наверху: так видно, что
          именно править, не отматывая форму. */}
      {error && (
        <p id={errorId} className="text-xs text-[#b25e4a]">
          {error}
        </p>
      )}
    </div>
  );
}
