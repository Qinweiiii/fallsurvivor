'use client';

import Link from 'next/link';
import Image from 'next/image';
import useSWR from 'swr';
import { fetcher } from '@/lib/api';
import type { DashboardData } from '@/lib/types';
import {
  formatDaysLeft,
  formatRelative,
  orDash,
  scoreTone,
  SEARCH_STATUS_LABELS,
} from '@/lib/utils';
import { PageHeader } from '@/components/layout/PageHeader';
import { Badge, StatBlock } from '@/components/ui/Badge';
import { EmptyState, ErrorState, Skeleton } from '@/components/ui/States';
import { HeartDecor, PastelPillBar, Sparkles, StarDecor, WandDecor } from '@/components/decor/Decor';

export default function DashboardPage() {
  const { data, error, isLoading, mutate } = useSWR<DashboardData>('/dashboard', fetcher, {
    refreshInterval: 30_000,
  });

  return (
    <>
      <PageHeader
        title="Dashboard"
        subtitle="今天的秋招进度一览。慢慢来，一个一个投。"
      />

      <div className="relative z-10 flex-1 overflow-y-auto px-8 py-6">
        {error ? (
          <ErrorState
            message="无法加载数据，请确认后端服务已启动"
            onRetry={() => void mutate()}
          />
        ) : isLoading ? (
          <StatsSkeleton />
        ) : data ? (
          <div className="space-y-6">
            <HeroCard data={data} />

            <StatCards data={data} />

            <div className="grid gap-6 lg:grid-cols-3">
              <HighMatchCard data={data} />
              <DeadlineCard data={data} />
              <ActivityCard data={data} />
            </div>
          </div>
        ) : null}
      </div>
    </>
  );
}

/**
 * 头部主卡：参考图风格 —— 吉祥物 + 渐变背景 + 标题 + PillBar 装饰。
 */
function HeroCard({ data }: { data: DashboardData }) {
  const task = data.latest_search_task;
  const searching = task?.status === 'RUNNING' || task?.status === 'PENDING';

  return (
    <section className="relative overflow-hidden rounded-card border border-white/70 bg-card-lilac px-8 py-7 shadow-card">
      <div className="pointer-events-none absolute -right-6 -top-8 h-44 w-44 animate-float opacity-90">
        <Image
          src="/decor/cloud-mascot.png"
          alt=""
          width={176}
          height={176}
          priority
          className="object-contain"
        />
      </div>
      <Sparkles count={18} />

      <div className="relative max-w-3xl">
        <p className="text-xs font-semibold uppercase tracking-wider text-lilac-700">
          Welcome to 你的求职工作台
        </p>
        <h2 className="mt-1.5 text-2xl font-semibold text-ink-900">
          今天，{data.stats.jobs.new > 0 ? `有 ${data.stats.jobs.new} 个新岗位被发现` : '暂时没有新岗位'}
        </h2>
        <p className="mt-1.5 text-sm text-ink-600">
          {searching
            ? SEARCH_STATUS_LABELS[task.status] + '，马上回来……'
            : data.stats.pending > 0
              ? `还有 ${data.stats.pending} 个投递待完成。今天也加油！`
              : '当前没有待处理的投递，休息一下也不错。'}
        </p>

        <div className="mt-5 flex flex-wrap items-center gap-3">
          <Link
            href="/jobs"
            className="inline-flex h-10 items-center rounded-pill bg-btn-primary px-5 text-sm font-medium text-white shadow-soft transition hover:opacity-95"
          >
            去岗位总览
          </Link>
          <Link
            href="/applications"
            className="inline-flex h-10 items-center rounded-pill border border-lilac-200 bg-white/80 px-5 text-sm font-medium text-lilac-700 transition hover:bg-white"
          >
            查看投递进度
          </Link>
        </div>

        <div className="mt-5 flex items-center gap-3">
          <PastelPillBar />
          <span className="text-[11px] text-lilac-700">为你的秋招保驾护航</span>
        </div>
      </div>
    </section>
  );
}

function StatCards({ data }: { data: DashboardData }) {
  const s = data.stats;
  const cards = [
    {
      label: '已发现岗位',
      value: s.jobs.total,
      hint: `近 7 天新增 ${s.jobs.new}`,
      tone: 'lilac' as const,
      icon: <StarDecor className="h-6 w-6" />,
    },
    {
      label: '岗位车',
      value: s.cart,
      hint: '准备投递的岗位',
      tone: 'blossom' as const,
      icon: <HeartDecor className="h-6 w-6" />,
    },
    {
      label: '待投递',
      value: s.pending,
      hint: '尚未完成提交',
      tone: 'cream' as const,
      icon: <WandDecor className="h-6 w-6" />,
    },
    {
      label: '已投递',
      value: s.submitted,
      hint: '已在网站完成提交',
      tone: 'mint' as const,
      icon: <CheckSvg />,
    },
    {
      label: '笔试',
      value: s.written_test,
      hint: '',
      tone: 'sky' as const,
      icon: <PenSvg />,
    },
    {
      label: '面试',
      value: s.interview,
      hint: '含一面 / 二面 / HR 面',
      tone: 'lilac' as const,
      icon: <ChatSvg />,
    },
    {
      label: 'Offer',
      value: s.offer,
      hint: '',
      tone: 'mint' as const,
      icon: <GiftSvg />,
    },
  ];

  return (
    <section className="grid grid-cols-2 gap-4 md:grid-cols-4 xl:grid-cols-7">
      {cards.map((c) => (
        <div
          key={c.label}
          className="relative overflow-hidden rounded-card border border-white/70 bg-white/85 p-4 shadow-card backdrop-blur-sm"
        >
          <div className="flex items-start justify-between">
            <div>
              <p className="text-xs text-ink-500">{c.label}</p>
              <p className="mt-1.5 text-2xl font-semibold tabular-nums text-ink-900">
                {c.value}
              </p>
              {c.hint ? <p className="mt-1 text-[11px] text-ink-400">{c.hint}</p> : null}
            </div>
            <StatBlock tone={c.tone}>{c.icon}</StatBlock>
          </div>
        </div>
      ))}
    </section>
  );
}

function HighMatchCard({ data }: { data: DashboardData }) {
  const task = data.latest_search_task;

  return (
    <section className="card flex flex-col overflow-hidden">
      <div className="flex items-center justify-between border-b border-ink-100 px-5 py-4">
        <h2 className="flex items-center gap-2 text-sm font-semibold text-ink-800">
          <WandDecor className="h-4 w-4 text-lilac-400" />
          新发现的高匹配岗位
        </h2>
        <Link href="/jobs" className="text-xs font-medium text-lilac-600 hover:text-lilac-700">
          查看全部
        </Link>
      </div>

      {task && (task.status === 'RUNNING' || task.status === 'PENDING') ? (
        <p className="border-b border-ink-100 bg-lilac-50/60 px-5 py-2.5 text-xs text-lilac-700">
          {SEARCH_STATUS_LABELS[task.status]}…
        </p>
      ) : null}

      {data.recent_high_match.length === 0 ? (
        <EmptyState
          title="还没有高匹配岗位"
          description="到「岗位总览」点击获取岗位，让系统帮你找一找。"
          icon={<StarDecor className="h-10 w-10" />}
        />
      ) : (
        <ul className="divide-y divide-ink-100">
          {data.recent_high_match.slice(0, 6).map((job) => (
            <li key={job.id} className="px-5 py-3">
              <div className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                  <p className="truncate text-sm font-medium text-ink-800">{job.title}</p>
                  <p className="mt-0.5 truncate text-xs text-ink-500">
                    {job.company_name}
                    {job.department ? ` · ${job.department}` : ''}
                    {job.location ? ` · ${job.location}` : ''}
                  </p>
                </div>
                <MatchBadge score={job.match_score} />
              </div>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function DeadlineCard({ data }: { data: DashboardData }) {
  return (
    <section className="card flex flex-col overflow-hidden">
      <div className="border-b border-ink-100 px-5 py-4">
        <h2 className="flex items-center gap-2 text-sm font-semibold text-ink-800">
          <HeartDecor className="h-4 w-4 text-blossom-400" />
          即将截止
        </h2>
      </div>

      {data.upcoming_deadlines.length === 0 ? (
        <EmptyState
          title="暂无明确截止日期的岗位"
          description="只有岗位页面写明截止日期时才会显示，系统不会推测。"
        />
      ) : (
        <ul className="divide-y divide-ink-100">
          {data.upcoming_deadlines.map((d) => (
            <li key={d.job_id} className="flex items-start justify-between gap-3 px-5 py-3">
              <div className="min-w-0">
                <p className="truncate text-sm font-medium text-ink-800">{d.title}</p>
                <p className="mt-0.5 truncate text-xs text-ink-500">{d.company_name}</p>
              </div>
              <Badge tone={d.days_left <= 3 ? 'blossom' : 'cream'}>
                {formatDaysLeft(d.days_left)}
              </Badge>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function ActivityCard({ data }: { data: DashboardData }) {
  return (
    <section className="card flex flex-col overflow-hidden">
      <div className="border-b border-ink-100 px-5 py-4">
        <h2 className="text-sm font-semibold text-ink-800">最近动态</h2>
      </div>

      {data.recent_events.length === 0 ? (
        <EmptyState
          title="还没有动态"
          description="加入岗位车、开始投递后这里会记录你的每一步。"
        />
      ) : (
        <ul className="divide-y divide-ink-100">
          {data.recent_events.slice(0, 6).map((e) => (
            <li key={e.id} className="px-5 py-3">
              <p className="text-sm text-ink-700">{e.description}</p>
              <p className="mt-0.5 text-[11px] text-ink-400">
                {orDash(e.company_name)} · {formatRelative(e.created_at)}
              </p>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function MatchBadge({ score }: { score: number }) {
  const tone = scoreTone(score);
  const toneMap = { high: 'mint', good: 'lilac', mid: 'sky', low: 'neutral' } as const;
  return <Badge tone={toneMap[tone]}>{score}%</Badge>;
}

function StatsSkeleton() {
  return (
    <div className="space-y-6">
      <Skeleton className="h-44 rounded-card" />
      <div className="grid grid-cols-2 gap-4 md:grid-cols-4 xl:grid-cols-7">
        {Array.from({ length: 7 }).map((_, i) => (
          <Skeleton key={i} className="h-[88px] rounded-card" />
        ))}
      </div>
      <div className="grid gap-6 lg:grid-cols-3">
        {Array.from({ length: 3 }).map((_, i) => (
          <Skeleton key={i} className="h-72 rounded-card" />
        ))}
      </div>
    </div>
  );
}

/* 小图标 */
function CheckSvg() {
  return (
    <svg width="22" height="22" viewBox="0 0 20 20" fill="none" aria-hidden="true">
      <circle cx="10" cy="10" r="8" stroke="currentColor" strokeWidth="1.6" />
      <path d="M6.5 10.5L9 13l4.5-5.5" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

function PenSvg() {
  return (
    <svg width="22" height="22" viewBox="0 0 20 20" fill="none" aria-hidden="true">
      <path d="M4 16l2-7 8-8 3 3-8 8-5 4z" stroke="currentColor" strokeWidth="1.6" strokeLinejoin="round" />
    </svg>
  );
}

function ChatSvg() {
  return (
    <svg width="22" height="22" viewBox="0 0 20 20" fill="none" aria-hidden="true">
      <path
        d="M3 5a2 2 0 0 1 2-2h10a2 2 0 0 1 2 2v6a2 2 0 0 1-2 2H8l-4 4V5z"
        stroke="currentColor"
        strokeWidth="1.6"
        strokeLinejoin="round"
      />
    </svg>
  );
}

function GiftSvg() {
  return (
    <svg width="22" height="22" viewBox="0 0 20 20" fill="none" aria-hidden="true">
      <rect x="3" y="8" width="14" height="9" rx="1.4" stroke="currentColor" strokeWidth="1.6" />
      <path d="M3 12h14M10 8v9" stroke="currentColor" strokeWidth="1.6" />
      <path d="M7.5 8C6 8 5 7 5 5.5S6 4 7 4.5c1 .4 3 2.5 3 3.5M12.5 8C14 8 15 7 15 5.5S14 4 13 4.5c-1 .4-3 2.5-3 3.5" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" />
    </svg>
  );
}