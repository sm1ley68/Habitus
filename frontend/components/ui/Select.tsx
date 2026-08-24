"use client";
import { forwardRef, type SelectHTMLAttributes } from "react";
import { fieldClass } from "./Input";

const Select = forwardRef<HTMLSelectElement, SelectHTMLAttributes<HTMLSelectElement>>(
  function Select({ className = "", children, ...rest }, ref) {
    return (
      <select ref={ref} className={`${fieldClass} cursor-pointer ${className}`} {...rest}>
        {children}
      </select>
    );
  },
);

export default Select;
