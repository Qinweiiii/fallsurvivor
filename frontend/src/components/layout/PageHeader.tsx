import type { ReactNode } from 'react';
import { HeaderDecor, Sparkles } from '@/components/decor/Decor';

interface PageHeaderProps {
  title: string;
  subtitle?: string;
  actions?: ReactNode;
}

/**
 * 页面顶部区域。
 *
 * 参考图风格：右侧漂浮大量装饰元素（云朵 / 星星 / 爱心 / 花朵），
 * 标题与操作按钮放在左侧保证可读性优先。
 */
export function PageHeader({ title, subtitle, actions }: PageHeaderProps) {
  return (
    <header className="relative overflow-hidden border-b border-white/70 bg-white/55 px-8 py-6 backdrop-blur-md">
      <HeaderDecor />
      <Sparkles count={20} />

      <div className="relative flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="text-xl font-semibold tracking-tight text-ink-900">{title}</h1>
          {subtitle ? <p className="mt-1 text-sm text-ink-500">{subtitle}</p> : null}
        </div>
        {actions ? <div className="flex items-center gap-2">{actions}</div> : null}
      </div>
    </header>
  );
}