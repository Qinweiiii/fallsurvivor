import Image from 'next/image';
import { cn } from '@/lib/utils';

/**
 * 原创装饰元素。
 * 所有 SVG 为自绘，所有 PNG 为自生成，不使用受版权保护的角色形象。
 */

// ---------------- 自绘 SVG 装饰 ----------------

export function StarDecor({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" className={className} fill="none" aria-hidden="true">
      <path
        d="M12 3l1.9 5.1L19 10l-5.1 1.9L12 17l-1.9-5.1L5 10l5.1-1.9L12 3z"
        fill="currentColor"
      />
    </svg>
  );
}

export function CloudDecor({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 40 24" className={className} fill="none" aria-hidden="true">
      <path
        d="M11 19a5 5 0 0 1-.6-9.96A7 7 0 0 1 23.6 8.2A4.4 4.4 0 1 1 30 12.6V19H11z"
        fill="currentColor"
      />
    </svg>
  );
}

export function HeartDecor({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" className={className} fill="none" aria-hidden="true">
      <path
        d="M12 20s-7-4.4-7-9.3A4.2 4.2 0 0 1 12 8a4.2 4.2 0 0 1 7 2.7C19 15.6 12 20 12 20z"
        fill="currentColor"
      />
    </svg>
  );
}

export function WandDecor({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" className={className} fill="none" aria-hidden="true">
      <path d="M5 19L15 9" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
      <path
        d="M17 3l.9 2.4L20 6.2l-2.1.9L17 9.4l-.9-2.3L14 6.2l2.1-.8L17 3z"
        fill="currentColor"
      />
      <circle cx="19.5" cy="12" r="1.2" fill="currentColor" opacity="0.7" />
      <circle cx="12" cy="4.5" r="1" fill="currentColor" opacity="0.6" />
    </svg>
  );
}

export function FlowerDecor({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" className={className} fill="none" aria-hidden="true">
      <g fill="currentColor" opacity="0.75">
        <circle cx="12" cy="7" r="3" />
        <circle cx="17" cy="12" r="3" />
        <circle cx="12" cy="17" r="3" />
        <circle cx="7" cy="12" r="3" />
      </g>
      <circle cx="12" cy="12" r="2.4" fill="#fff8e3" />
    </svg>
  );
}

export function SparkleDecor({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 20 20" className={className} fill="none" aria-hidden="true">
      <path
        d="M10 2 L11 9 L18 10 L11 11 L10 18 L9 11 L2 10 L9 9 Z"
        fill="currentColor"
      />
    </svg>
  );
}

// 装饰属性的预置（用于散落漂浮）
type Sparkle = {
  left: string;
  top: string;
  size: number;
  tone: 'lilac' | 'blossom' | 'sky' | 'cream' | 'mint';
  delay: string;
};

/**
 * 带动画延迟的星形装饰。用于在 Tailwind 不能拼接 delay 的场景。
 */
function StyledStar({
  className,
  delay,
}: {
  className: string;
  delay: string;
}) {
  return (
    <span
      className={cn('absolute animate-sparkle', className)}
      style={{ animationDelay: delay }}
    >
      <SparkleDecor className="h-full w-full" />
    </span>
  );
}

/**
 * 装饰星粒层。点缀在 Header 周围。
 */
export function Sparkles({ className, count = 14 }: { className?: string; count?: number }) {
  const tones: Array<'lilac' | 'blossom' | 'sky' | 'cream' | 'mint'> = [
    'lilac',
    'blossom',
    'sky',
    'cream',
    'mint',
  ];
  const items: Sparkle[] = Array.from({ length: count }).map((_, i) => ({
    left: `${(i * 37) % 100}%`,
    top: `${(i * 53) % 100}%`,
    size: 6 + ((i * 7) % 14),
    tone: tones[i % tones.length] ?? 'lilac',
    delay: `${(i % 6) * 0.3}s`,
  }));
  const colorClass: Record<Sparkle['tone'], string> = {
    lilac: 'text-lilac-300',
    blossom: 'text-blossom-300',
    sky: 'text-sky-300',
    cream: 'text-cream-400',
    mint: 'text-mint-300',
  };

  return (
    <div className={cn('pointer-events-none absolute inset-0', className)}>
      {items.map((s, i) => (
        <span
          key={i}
          className="absolute animate-sparkle"
          style={{
            left: s.left,
            top: s.top,
            animationDelay: s.delay,
          }}
        >
          <span
            aria-hidden="true"
            className={cn('block', colorClass[s.tone])}
            style={{ width: s.size, height: s.size }}
          >
            <SparkleDecor className="h-full w-full" />
          </span>
        </span>
      ))}
    </div>
  );
}

/**
 * Header 装饰层：漂浮的云朵、星星、花朵，分布更密。
 */
export function HeaderDecor() {
  return (
    <div className="pointer-events-none absolute right-0 top-0 h-full w-[460px] overflow-hidden">
      <CloudDecor className="absolute -top-2 right-8 h-12 w-20 animate-float text-lilac-200" />
      <CloudDecor className="absolute right-44 top-16 h-8 w-14 animate-floatAlt text-blossom-200" />
      <StyledStar className="right-32 top-4 h-5 w-5 text-blossom-300" delay="0s" />
      <StyledStar className="right-14 top-24 h-3 w-3 text-sky-300" delay="0.8s" />
      <StyledStar className="right-56 top-32 h-4 w-4 text-cream-400" delay="1.4s" />
      <FlowerDecor className="absolute bottom-2 right-44 h-7 w-7 animate-float text-lilac-200" />
      <HeartDecor className="absolute right-72 top-20 h-4 w-4 animate-floatAlt text-blossom-300" />
      <SparkleDecor className="absolute right-4 bottom-6 h-3 w-3 animate-sparkle text-cream-400" />
    </div>
  );
}

/**
 * 页面级装饰层：散落到页面四个角。z-index 较低，不抢内容焦点。
 */
export function PageDecor() {
  return (
    <div className="pointer-events-none fixed inset-0 z-0 overflow-hidden">
      <StyledStar className="left-12 top-32 h-4 w-4 text-blossom-300" delay="0s" />
      <StyledStar className="left-32 bottom-40 h-3 w-3 text-cream-400" delay="1.2s" />
      <HeartDecor className="absolute right-16 top-48 h-5 w-5 animate-float text-blossom-200" />
      <FlowerDecor className="absolute right-8 bottom-24 h-6 w-6 animate-floatAlt text-lilac-200" />
      <CloudDecor className="absolute left-8 bottom-12 h-7 w-12 animate-float text-sky-200" />
    </div>
  );
}

// ---------------- PNG 吉祥物 ----------------

/**
 * 云朵吉祥物（带星星眼、魔法棒、蝴蝶结）。
 * 默认占位 alt 不出现具体角色名，仅描述外观。
 */
export function CloudMascot({
  className,
  size = 80,
  priority = false,
}: {
  className?: string;
  size?: number;
  priority?: boolean;
}) {
  return (
    <Image
      src="/decor/cloud-mascot.png"
      alt=""
      width={size}
      height={size}
      priority={priority}
      className={cn('object-contain', className)}
    />
  );
}

/** 文件夹吉祥物。 */
export function FolderMascot({
  className,
  size = 80,
  priority = false,
}: {
  className?: string;
  size?: number;
  priority?: boolean;
}) {
  return (
    <Image
      src="/decor/folder-mascot.png"
      alt=""
      width={size}
      height={size}
      priority={priority}
      className={cn('object-contain', className)}
    />
  );
}

// ---------------- 进度胶囊条 ----------------

/**
 * 多色胶囊条，参考图右下角那种三段小指示器。
 * 当前只用作视觉装饰，不与任何数据绑定。
 */
export function PastelPillBar({
  segments = 3,
  active = 1,
  className,
}: {
  segments?: number;
  active?: number;
  className?: string;
}) {
  const colors = ['bg-blossom-300', 'bg-cream-300', 'bg-lilac-300'];
  return (
    <div
      className={cn(
        'inline-flex items-center gap-1.5 rounded-pill bg-white/70 px-2 py-1 shadow-card',
        className,
      )}
      aria-hidden="true"
    >
      {Array.from({ length: segments }).map((_, i) => (
        <span
          key={i}
          className={cn(
            'h-2 w-6 rounded-pill transition',
            i === active ? colors[i % colors.length] : 'bg-ink-100',
          )}
        />
      ))}
    </div>
  );
}