'use client';

import { cn } from '@/lib/utils';
import { useEffect, type ReactNode } from 'react';

interface DrawerProps {
  open: boolean;
  onClose: () => void;
  title: string;
  subtitle?: string;
  footer?: ReactNode;
  children: ReactNode;
}

/**
 * 右侧抽屉。用于岗位详情，避免跳转新页面。
 */
export function Drawer({ open, onClose, title, subtitle, footer, children }: DrawerProps) {
  // ESC 关闭 + 打开时锁定背景滚动
  useEffect(() => {
    if (!open) return;

    function onKeyDown(e: KeyboardEvent) {
      if (e.key === 'Escape') onClose();
    }
    document.addEventListener('keydown', onKeyDown);

    const prevOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';

    return () => {
      document.removeEventListener('keydown', onKeyDown);
      document.body.style.overflow = prevOverflow;
    };
  }, [open, onClose]);

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-50 flex justify-end" role="dialog" aria-modal="true">
      <button
        type="button"
        aria-label="关闭详情"
        onClick={onClose}
        className="absolute inset-0 bg-ink-900/20 backdrop-blur-[2px]"
      />

      <aside
        className={cn(
          'relative flex h-full w-full max-w-[640px] flex-col',
          'border-l border-white/70 bg-white shadow-lift animate-slide-in-right',
        )}
      >
        <header className="flex items-start gap-3 border-b border-ink-100 px-6 py-5">
          <div className="min-w-0 flex-1">
            <h2 className="truncate text-lg font-semibold text-ink-900">{title}</h2>
            {subtitle ? (
              <p className="mt-0.5 truncate text-sm text-ink-500">{subtitle}</p>
            ) : null}
          </div>
          <button
            type="button"
            onClick={onClose}
            aria-label="关闭"
            className="rounded-pill p-1.5 text-ink-400 transition hover:bg-ink-100 hover:text-ink-700"
          >
            <svg width="18" height="18" viewBox="0 0 20 20" fill="none" aria-hidden="true">
              <path
                d="M5 5l10 10M15 5L5 15"
                stroke="currentColor"
                strokeWidth="1.8"
                strokeLinecap="round"
              />
            </svg>
          </button>
        </header>

        <div className="flex-1 overflow-y-auto px-6 py-5">{children}</div>

        {footer ? (
          <footer className="border-t border-ink-100 bg-white/95 px-6 py-4">{footer}</footer>
        ) : null}
      </aside>
    </div>
  );
}
