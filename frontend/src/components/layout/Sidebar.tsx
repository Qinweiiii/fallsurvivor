'use client';

import Link from 'next/link';
import { usePathname } from 'next/navigation';
import useSWR from 'swr';
import { cn } from '@/lib/utils';
import { fetcher } from '@/lib/api';
import type { DashboardData } from '@/lib/types';
import { CloudMascot } from '@/components/decor/Decor';

interface NavItem {
  href: string;
  label: string;
  icon: JSX.Element;
  badgeKey?: 'cart' | 'pending';
}

const NAV_ITEMS: NavItem[] = [
  { href: '/', label: 'Dashboard', icon: <IconHome /> },
  { href: '/jobs', label: '岗位总览', icon: <IconSearch /> },
  { href: '/cart', label: '岗位车', icon: <IconCart />, badgeKey: 'cart' },
  { href: '/applications', label: '投递进度', icon: <IconTrack />, badgeKey: 'pending' },
  { href: '/profile', label: '求职画像', icon: <IconUser /> },
  { href: '/llm-logs', label: 'LLM 日志', icon: <IconCode /> },
  { href: '/site-recipes', label: '站点配置', icon: <IconSite /> },
  { href: '/settings', label: '设置', icon: <IconGear /> }];

export function Sidebar() {
  const pathname = usePathname();
  const { data } = useSWR<DashboardData>('/dashboard', fetcher, {
    refreshInterval: 60_000,
    revalidateOnFocus: true,
  });

  return (
    <aside className="relative flex h-full w-64 shrink-0 flex-col border-r border-white/70 bg-white/70 backdrop-blur-md">
      {/* 顶部 logo 区 */}
      <div className="relative flex items-center gap-3 px-5 pb-6 pt-7">
        <span className="relative">
          <span className="absolute -inset-1 rounded-2xl bg-btn-primary opacity-30 blur-md" />
          <span className="relative flex h-12 w-12 items-center justify-center rounded-2xl bg-white shadow-card ring-1 ring-lilac-100">
            <CloudMascot size={44} priority />
          </span>
        </span>
        <div className="leading-tight">
          <p className="text-base font-semibold text-ink-900">Fall Survivor</p>
          <p className="text-[11px] text-ink-400">个人求职工作台</p>
        </div>
      </div>

      <nav className="flex-1 space-y-1 px-3">
        {NAV_ITEMS.map((item) => {
          const active =
            item.href === '/' ? pathname === '/' : pathname.startsWith(item.href);
          const badge = item.badgeKey ? data?.stats?.[item.badgeKey] : undefined;

          return (
            <Link
              key={item.href}
              href={item.href}
              className={cn(
                'group flex items-center gap-2.5 rounded-xl px-3 py-2.5 text-sm transition',
                active
                  ? 'bg-card-lilac font-medium text-lilac-700 shadow-card'
                  : 'text-ink-600 hover:bg-lilac-50 hover:text-lilac-700',
              )}
            >
              <span className={cn('shrink-0', active ? 'text-lilac-600' : 'text-ink-400')}>
                {item.icon}
              </span>
              <span className="flex-1">{item.label}</span>
              {badge !== undefined && badge > 0 ? (
                <span className="rounded-pill bg-blossom-200 px-1.5 py-0.5 text-[10px] font-semibold text-blossom-800">
                  {badge}
                </span>
              ) : null}
            </Link>
          );
        })}
      </nav>

      {/* 底部便签式提示卡：参考图那种"卷尾便签"风格 */}
      <div className="px-4 pb-5">
        <div className="relative overflow-hidden rounded-card border border-lilac-100 bg-card-lilac px-4 py-3 shadow-card">
          <p className="text-[11px] leading-relaxed text-lilac-800">
            系统只做辅助填写。
            <br />
            最终提交始终由你亲自点击。
          </p>
          {/* 装饰小尾巴 */}
          <svg
            className="absolute -bottom-1.5 right-6 h-3 w-6 text-card-lilac"
            viewBox="0 0 24 12"
            fill="currentColor"
            aria-hidden="true"
          >
            <path d="M2 0L12 12H0Q4 0 2 0Z" />
          </svg>
        </div>
      </div>
    </aside>
  );
}

/* ---------------- 图标 ---------------- */

function IconHome() {
  return (
    <svg width="18" height="18" viewBox="0 0 20 20" fill="none" aria-hidden="true">
      <path
        d="M3 8.5L10 3l7 5.5V16a1 1 0 0 1-1 1h-3.5v-4.5h-5V17H4a1 1 0 0 1-1-1V8.5z"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinejoin="round"
      />
    </svg>
  );
}

function IconSearch() {
  return (
    <svg width="18" height="18" viewBox="0 0 20 20" fill="none" aria-hidden="true">
      <circle cx="9" cy="9" r="5.5" stroke="currentColor" strokeWidth="1.5" />
      <path d="M13.5 13.5L17 17" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
    </svg>
  );
}

function IconCart() {
  return (
    <svg width="18" height="18" viewBox="0 0 20 20" fill="none" aria-hidden="true">
      <path
        d="M3 4h2l1.6 8.4a1 1 0 0 0 1 .8h6.8a1 1 0 0 0 1-.78L17 7H6"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      <circle cx="8" cy="16" r="1.2" fill="currentColor" />
      <circle cx="14.5" cy="16" r="1.2" fill="currentColor" />
    </svg>
  );
}

function IconTrack() {
  return (
    <svg width="18" height="18" viewBox="0 0 20 20" fill="none" aria-hidden="true">
      <path d="M4 15V9M10 15V5M16 15v-4" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
      <path d="M3 17.5h14" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" opacity="0.5" />
    </svg>
  );
}

function IconUser() {
  return (
    <svg width="18" height="18" viewBox="0 0 20 20" fill="none" aria-hidden="true">
      <circle cx="10" cy="7" r="3.2" stroke="currentColor" strokeWidth="1.5" />
      <path
        d="M4 17c0-3 2.7-5 6-5s6 2 6 5"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
      />
    </svg>
  );
}

function IconGear() {
  return (
    <svg width="18" height="18" viewBox="0 0 20 20" fill="none" aria-hidden="true">
      <circle cx="10" cy="10" r="2.6" stroke="currentColor" strokeWidth="1.5" />
      <path
        d="M10 3v2M10 15v2M3 10h2M15 10h2M5.2 5.2l1.4 1.4M13.4 13.4l1.4 1.4M14.8 5.2l-1.4 1.4M6.6 13.4l-1.4 1.4"
        stroke="currentColor"
        strokeWidth="1.4"
        strokeLinecap="round"
      />
    </svg>
  );
}

function IconCode() {
  return (
    <svg width="18" height="18" viewBox="0 0 20 20" fill="none" aria-hidden="true">
      <path
        d="M7 5L3 10l4 5M13 5l4 5-4 5"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

function IconSite() {
  return (
    <svg width="18" height="18" viewBox="0 0 20 20" fill="none" aria-hidden="true">
      <circle cx="10" cy="10" r="7.2" stroke="currentColor" strokeWidth="1.5" />
      <path
        d="M2.8 10h14.4M10 2.8c1.9 2 2.9 4.5 2.9 7.2S11.9 15.2 10 17.2c-1.9-2-2.9-4.5-2.9-7.2S8.1 4.8 10 2.8z"
        stroke="currentColor"
        strokeWidth="1.3"
      />
    </svg>
  );
}