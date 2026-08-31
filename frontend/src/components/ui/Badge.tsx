import { cn } from '@/lib/utils';
import type { ReactNode } from 'react';

type Tone = 'lilac' | 'blossom' | 'sky' | 'mint' | 'cream' | 'neutral';

const toneStyles: Record<Tone, string> = {
  lilac: 'bg-lilac-100 text-lilac-700 border-lilac-200',
  blossom: 'bg-blossom-100 text-blossom-700 border-blossom-200',
  sky: 'bg-sky-100 text-sky-700 border-sky-200',
  mint: 'bg-mint-100 text-mint-700 border-mint-200',
  cream: 'bg-cream-100 text-cream-700 border-cream-200',
  neutral: 'bg-ink-100 text-ink-600 border-ink-200',
};

interface BadgeProps {
  tone?: Tone;
  children: ReactNode;
  className?: string;
  size?: 'sm' | 'md';
}

export function Badge({ tone = 'neutral', children, className, size = 'sm' }: BadgeProps) {
  return (
    <span
      className={cn(
        'inline-flex items-center whitespace-nowrap rounded-pill border font-medium',
        toneStyles[tone],
        size === 'sm' ? 'px-2.5 py-0.5 text-xs' : 'px-3 py-1 text-sm',
        className,
      )}
    >
      {children}
    </span>
  );
}

/** 岗位状态对应的色调。 */
const jobStatusTone: Record<string, Tone> = {
  NEW: 'neutral',
  IN_CART: 'lilac',
  PREPARING: 'sky',
  SUBMITTED: 'mint',
  CLOSED: 'neutral',
};

/** 投递状态对应的色调。 */
const appStatusTone: Record<string, Tone> = {
  PREPARING: 'neutral',
  LOGIN_REQUIRED: 'cream',
  FORM_ANALYZING: 'sky',
  FORM_FILLING: 'sky',
  WAITING_USER: 'cream',
  READY_TO_SUBMIT: 'lilac',
  SUBMITTED: 'mint',
  WRITTEN_TEST: 'sky',
  INTERVIEW_1: 'lilac',
  INTERVIEW_2: 'lilac',
  HR_INTERVIEW: 'lilac',
  OFFER: 'mint',
  REJECTED: 'neutral',
  WITHDRAWN: 'neutral',
  FAILED: 'blossom',
  BLOCKED: 'blossom',
};

export function jobStatusToneOf(status: string): Tone {
  return jobStatusTone[status] ?? 'neutral';
}

export function appStatusToneOf(status: string): Tone {
  return appStatusTone[status] ?? 'neutral';
}

/**
 * 大号徽章块：用于 Dashboard 统计卡上的色块。
 * 颜色更纯正，更大，作为视觉装饰而非状态指示。
 */
export function StatBlock({
  tone,
  children,
  className,
}: {
  tone: 'lilac' | 'blossom' | 'sky' | 'mint' | 'cream';
  children: ReactNode;
  className?: string;
}) {
  const bg: Record<typeof tone, string> = {
    lilac: 'bg-card-lilac text-lilac-700',
    blossom: 'bg-blossom-100 text-blossom-700',
    sky: 'bg-card-sky text-sky-700',
    mint: 'bg-card-mint text-mint-700',
    cream: 'bg-card-cream text-cream-700',
  };
  return (
    <div
      className={cn(
        'flex h-12 w-12 items-center justify-center rounded-2xl text-lg font-semibold shadow-card',
        bg[tone],
        className,
      )}
    >
      {children}
    </div>
  );
}