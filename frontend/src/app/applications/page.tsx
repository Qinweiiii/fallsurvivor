'use client';

import { useState } from 'react';
import { useRouter } from 'next/navigation';
import useSWR from 'swr';
import { buildQuery, fetcher } from '@/lib/api';
import type { Application, Page, StatusOption } from '@/lib/types';
import {
  APP_STATUS_LABELS,
  cn,
  formatRelative,
  isTerminalStatus,
  orDash,
} from '@/lib/utils';
import { PageHeader } from '@/components/layout/PageHeader';
import { Button } from '@/components/ui/Button';
import { Badge, appStatusToneOf } from '@/components/ui/Badge';
import { EmptyState, ErrorState, TableSkeleton } from '@/components/ui/States';
import { PaginationBar } from '@/components/ui/PaginationBar';
import { MatchBadge } from '@/components/jobs/MatchBadge';
import { ApplicationDrawer } from '@/components/applications/ApplicationDrawer';

type Scope = 'active' | 'finished' | 'all';

const SCOPES: { value: Scope; label: string }[] = [
  { value: 'active', label: '进行中' },
  { value: 'finished', label: '已结束' },
  { value: 'all', label: '全部' },
];

export default function ApplicationsPage() {
  const router = useRouter();
  const [scope, setScope] = useState<Scope>('active');
  const [keyword, setKeyword] = useState('');
  const [statuses, setStatuses] = useState<string[]>([]);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [detailId, setDetailId] = useState<string | null>(null);

  const key = `/applications${buildQuery({
    page,
    page_size: pageSize,
    scope,
    keyword,
    statuses,
  })}`;

  const { data, error, isLoading, mutate } = useSWR<Page<Application>>(key, fetcher, {
    keepPreviousData: true,
    refreshInterval: 15_000,
  });
  const { data: statusOptions } = useSWR<StatusOption[]>('/applications/status-options', fetcher);

  const items = data?.items ?? [];

  function toggleStatus(s: string) {
    setStatuses((prev) => (prev.includes(s) ? prev.filter((x) => x !== s) : [...prev, s]));
    setPage(1);
  }

  return (
    <>
      <PageHeader
        title="投递进度"
        subtitle="跟踪每一个岗位从准备到 Offer 的全过程。"
      />

      <div className="flex-1 overflow-y-auto px-8 py-6">
        {/* 筛选 */}
        <section className="card mb-4 flex flex-wrap items-end gap-3 px-5 py-4">
          <div>
            <span className="label-text">范围</span>
            <div className="flex gap-1.5">
              {SCOPES.map((s) => (
                <button
                  key={s.value}
                  type="button"
                  onClick={() => {
                    setScope(s.value);
                    setPage(1);
                  }}
                  className={cn(
                    'rounded-pill border px-3 py-1.5 text-xs font-medium transition',
                    scope === s.value
                      ? 'border-lilac-300 bg-lilac-100 text-lilac-700'
                      : 'border-ink-200 bg-white text-ink-600 hover:text-lilac-700',
                  )}
                >
                  {s.label}
                </button>
              ))}
            </div>
          </div>

          <div className="min-w-[200px] flex-1">
            <label className="label-text" htmlFor="app-keyword">
              关键词
            </label>
            <input
              id="app-keyword"
              className="input"
              placeholder="搜索公司 / 岗位"
              value={keyword}
              onChange={(e) => {
                setKeyword(e.target.value);
                setPage(1);
              }}
            />
          </div>

          {statusOptions ? (
            <div className="w-full">
              <span className="label-text">状态</span>
              <div className="flex flex-wrap gap-1.5">
                {statusOptions
                  .filter((o) => !['FORM_ANALYZING', 'FORM_FILLING'].includes(o.value))
                  .map((o) => (
                    <button
                      key={o.value}
                      type="button"
                      onClick={() => toggleStatus(o.value)}
                      className={cn(
                        'rounded-pill border px-2.5 py-1 text-xs font-medium transition',
                        statuses.includes(o.value)
                          ? 'border-lilac-300 bg-lilac-100 text-lilac-700'
                          : 'border-ink-200 bg-white text-ink-600 hover:text-lilac-700',
                      )}
                    >
                      {o.label}
                    </button>
                  ))}
              </div>
            </div>
          ) : null}
        </section>

        <div className="table-shell">
          {error ? (
            <ErrorState message="加载投递列表失败" onRetry={() => void mutate()} />
          ) : isLoading && items.length === 0 ? (
            <TableSkeleton rows={5} cols={7} />
          ) : items.length === 0 ? (
            <EmptyState
              title="还没有投递任务"
              description="到「岗位车」勾选岗位，点击「开始准备投递」。"
              action={
                <Button size="sm" onClick={() => router.push('/cart')}>
                  去岗位车
                </Button>
              }
            />
          ) : (
            <>
              <div className="overflow-x-auto">
                <table className="w-full min-w-[900px] border-collapse">
                  <thead className="bg-lilac-50/60">
                    <tr>
                      <th className="th">公司</th>
                      <th className="th">岗位</th>
                      <th className="th">地点</th>
                      <th className="th w-24">匹配度</th>
                      <th className="th w-32">状态</th>
                      <th className="th w-28">进度</th>
                      <th className="th w-28">最近操作</th>
                      <th className="th w-24 text-right">操作</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-ink-100">
                    {items.map((app) => {
                      const finished = isTerminalStatus(app.status);
                      return (
                        <tr
                          key={app.id}
                          className={cn(
                            'transition hover:bg-lilac-50/40',
                            // 已结束任务灰化
                            finished && 'opacity-55',
                          )}
                        >
                          <td className="td font-medium text-ink-800">
                            {orDash(app.job?.company_name)}
                          </td>
                          <td className="td">
                            <button
                              type="button"
                              onClick={() => setDetailId(app.id)}
                              className="text-left font-medium text-ink-800 hover:text-lilac-700"
                            >
                              {orDash(app.job?.title)}
                            </button>
                          </td>
                          <td className="td text-ink-600">{orDash(app.job?.location)}</td>
                          <td className="td">
                            <MatchBadge score={app.job?.match_score ?? 0} />
                          </td>
                          <td className="td">
                            <Badge tone={appStatusToneOf(app.status)}>
                              {APP_STATUS_LABELS[app.status] ?? app.status}
                            </Badge>
                          </td>
                          <td className="td">
                            <div className="h-1.5 w-20 overflow-hidden rounded-pill bg-ink-100">
                              <div
                                className="h-full rounded-pill bg-gradient-to-r from-lilac-400 to-blossom-300"
                                style={{ width: `${app.progress}%` }}
                              />
                            </div>
                          </td>
                          <td className="td text-xs text-ink-500">
                            {formatRelative(app.last_action_at ?? app.updated_at)}
                          </td>
                          <td className="td text-right">
                            <Button size="sm" variant="ghost" onClick={() => setDetailId(app.id)}>
                              查看
                            </Button>
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>

              {data && data.pagination.total > 0 ? (
                <div className="border-t border-ink-100">
                  <PaginationBar
                    page={data.pagination.page}
                    pageSize={data.pagination.page_size}
                    total={data.pagination.total}
                    totalPages={data.pagination.total_pages}
                    onPageChange={setPage}
                    onPageSizeChange={(s) => {
                      setPageSize(s);
                      setPage(1);
                    }}
                  />
                </div>
              ) : null}
            </>
          )}
        </div>
      </div>

      <ApplicationDrawer
        applicationId={detailId}
        onClose={() => setDetailId(null)}
        onChanged={() => void mutate()}
      />
    </>
  );
}
