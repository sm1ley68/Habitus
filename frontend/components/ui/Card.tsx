import type { ElementType, HTMLAttributes } from "react";

export interface CardProps extends HTMLAttributes<HTMLElement> {
  as?: ElementType;
}

// Та же оболочка, что у карточки объекта в выдаче: белая плоскость, волосяная
// рамка, радиус 2xl. Кабинет должен читаться как тот же продукт.
export default function Card({ as: Tag = "div", className = "", children, ...rest }: CardProps) {
  return (
    <Tag
      className={`rounded-2xl border border-zinc-200 bg-white ${className}`}
      {...rest}
    >
      {children}
    </Tag>
  );
}
