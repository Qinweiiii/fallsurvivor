import { cn } from '@/lib/utils';
import type { ReactNode } from 'react';

/** 空状态。 */
export function EmptyState({
  title,
  description,
  action,
  icon,
}: {
  title: string;
  description?: string;
  action?: ReactNode;
  icon?: ReactNode;
}) {
  return (
    <div className="flex flex-col items-center justify-center gap-3 px-6 py-16 text-center">
      <div className="text-lilac-300">{icon ?? <DefaultEmptyIcon />}</div>
      <p className="text-sm font-medium text-ink-700">{title}</p>
      {description ? <p className="max-w-sm text-xs text-ink-500">{description}</p> : null}
      {action}
    </div>
  );
}

function DefaultEmptyIcon() {
  return (
    <svg width="56" height="56" viewBox="0 0 56 56" fill="none" aria-hidden="true">
      <rect x="10" y="16" width="36" height="26" rx="5" stroke="currentColor" strokeWidth="2" />
      <path d="M10 24h36" stroke="currentColor" strokeWidth="2" />
      <circle cx="28" cy="33" r="3.5" fill="currentColor" opacity="0.5" />
    </svg>
  );
}

/** 加载骨架。 */
export function Skeleton({ className }: { className?: string }) {
  return <div className={cn('animate-pulse rounded-lg bg-ink-100', className)} />;
}

/** 表格加载骨架。 */
export function TableSkeleton({ rows = 6, cols = 7 }: { rows?: number; cols?: number }) {
  return (
    <div className="space-y-2 p-4">
      {Array.from({ length: rows }).map((_, r) => (
        <div key={r} className="flex gap-3">
          {Array.from({ length: cols }).map((_, c) => (
            <Skeleton key={c} className={cn('h-8 flex-1', c === 0 && 'max-w-10')} />
          ))}
        </div>
      ))}
    </div>
  );
}

/** 错误提示。 */
export function ErrorState({ message, onRetry }: { message: string; onRetry?: () => void }) {
  return (
    <div className="flex flex-col items-center gap-3 px-6 py-14 text-center">
      <div className="rounded-pill bg-blossom-100 p-3 text-blossom-600">
        <svg width="22" height="22" viewBox="0 0 20 20" fill="none" aria-hidden="true">
          <circle cx="10" cy="10" r="8" stroke="currentColor" strokeWidth="1.8" />
          <path d="M10 6v5M10 13.5v.5" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
        </svg>
      </div>
      <p className="text-sm text-ink-700">{message}</p>
      {onRetry ? (
        <button
          type="button"
          onClick={onRetry}
          className="text-xs font-medium text-lilac-600 hover:text-lilac-700"
        >
          重试
        </button>
      ) : null}
    </div>
  );
}
