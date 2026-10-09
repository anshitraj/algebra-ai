"use client";

import { useId, useState } from "react";
import { IconEye, IconEyeOff, Spinner } from "@/components/icons";

export function AuthInput({
  label,
  trailing,
  ...props
}: { label: string; trailing?: React.ReactNode } & React.InputHTMLAttributes<HTMLInputElement>) {
  const id = useId();
  return (
    <div>
      <div className="flex items-baseline justify-between">
        <label htmlFor={id} className="text-sm font-medium text-foreground">
          {label}
        </label>
        {trailing}
      </div>
      <input
        id={id}
        {...props}
        className="mt-1.5 h-11 w-full rounded-xl border border-border-strong bg-surface px-3.5 text-[0.95rem] text-foreground transition-[border-color,box-shadow] placeholder:text-muted/80 focus-visible:border-primary focus-visible:shadow-[0_0_0_3px_color-mix(in_srgb,var(--color-primary)_18%,transparent)] focus-visible:outline-none"
      />
    </div>
  );
}

export function PasswordInput({
  label,
  trailing,
  ...props
}: { label: string; trailing?: React.ReactNode } & React.InputHTMLAttributes<HTMLInputElement>) {
  const id = useId();
  const [shown, setShown] = useState(false);
  return (
    <div>
      <div className="flex items-baseline justify-between">
        <label htmlFor={id} className="text-sm font-medium text-foreground">
          {label}
        </label>
        {trailing}
      </div>
      <div className="relative mt-1.5">
        <input
          id={id}
          type={shown ? "text" : "password"}
          {...props}
          className="h-11 w-full rounded-xl border border-border-strong bg-surface pr-11 pl-3.5 text-[0.95rem] text-foreground transition-[border-color,box-shadow] placeholder:text-muted/80 focus-visible:border-primary focus-visible:shadow-[0_0_0_3px_color-mix(in_srgb,var(--color-primary)_18%,transparent)] focus-visible:outline-none"
        />
        <button
          type="button"
          onClick={() => setShown((s) => !s)}
          aria-label={shown ? "Hide password" : "Show password"}
          aria-pressed={shown}
          className="absolute top-1/2 right-1.5 flex h-8 w-8 -translate-y-1/2 items-center justify-center rounded-lg text-muted transition-colors hover:bg-primary-tint hover:text-foreground"
        >
          {shown ? <IconEyeOff size={17} /> : <IconEye size={17} />}
        </button>
      </div>
    </div>
  );
}

export function SubmitButton({ busy, children }: { busy: boolean; children: React.ReactNode }) {
  return (
    <button
      type="submit"
      disabled={busy}
      className="relative flex h-11 w-full items-center justify-center gap-2 rounded-xl bg-primary text-[0.95rem] font-medium text-primary-tint shadow-[0_6px_16px_-8px_color-mix(in_srgb,var(--color-primary)_80%,transparent)] transition-[transform,opacity] hover:opacity-95 active:scale-[0.99] disabled:cursor-wait disabled:opacity-70"
    >
      {busy && <Spinner size={16} />}
      {children}
    </button>
  );
}

export function FormError({ children }: { children: React.ReactNode }) {
  return (
    <p role="alert" className="rounded-xl bg-danger-tint px-3.5 py-2.5 text-sm leading-snug text-danger">
      {children}
    </p>
  );
}
