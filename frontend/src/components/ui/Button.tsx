import { cn } from '@/lib/utils';
import type { ButtonHTMLAttributes, ReactNode } from 'react';

type Variant = 'primary' | 'secondary' | 'ghost' | 'danger';
type Size = 'sm' | 'md';

interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant;
  size?: Size;
  loading?: boolean;
  icon?: ReactNode;
}

const variantStyles: Record<Variant, string> = {
  primary:
    'bg-btn-primary text-white shadow-soft hover:opacity-95 active:opacity-90 disabled:opacity-50',
  secondary:
    'border border-lilac-200 bg-white text-lilac-700 hover:border-lilac-300 hover:bg-lilac-50 disabled:text-ink-400',
  ghost: 'text-ink-600 hover:bg-lilac-50 hover:text-lilac-700 disabled:text-ink-300',
  danger:
    'border border-blossom-200 bg-white text-blossom-700 hover:border-blossom-300 hover:bg-blossom-50',
};

const sizeStyles: Record<Size, string> = {
  sm: 'h-8 gap-1.5 px-3 text-xs',
  md: 'h-10 gap-2 px-4 text-sm',
};

export function Button({
  variant = 'primary',
  size = 'md',
  loading = false,
  icon,
  className,
  children,
  disabled,
  ...rest
}: ButtonProps) {
  return (
    <button
      type="button"
      disabled={disabled || loading}
      className={cn(
        'inline-flex items-center justify-center rounded-pill font-medium transition',
        'disabled:cursor-not-allowed',
        variantStyles[variant],
        sizeStyles[size],
        className,
      )}
      {...rest}
    >
      {loading ? <Spinner /> : icon}
      {children}
    </button>
  );
}

function Spinner() {
  return (
    <svg
      className="h-3.5 w-3.5 animate-spin"
      viewBox="0 0 24 24"
      fill="none"
      aria-hidden="true"
    >
      <circle cx="12" cy="12" r="10" stroke="currentColor" strokeOpacity="0.25" strokeWidth="3" />
      <path
        d="M22 12a10 10 0 0 0-10-10"
        stroke="currentColor"
        strokeWidth="3"
        strokeLinecap="round"
      />
    </svg>
  );
}