'use client';

import { useCallback, useState } from 'react';
import { useRouter } from 'next/navigation';
import useSWR from 'swr';
import { api, buildQuery, fetcher } from '@/lib/api';
import type { CartItem, Page } from '@/lib/types';
import { firstDisplayValue, formatRelative, JOB_STATUS_LABELS, orDash } from '@/lib/utils';
import { PageHeader } from '@/components/layout/PageHeader';
import { Button } from '@/components/ui/Button';
import { Badge, jobStatusToneOf } from '@/components/ui/Badge';
import { EmptyState, ErrorState, TableSkeleton } from '@/components/ui/States';
import { PaginationBar } from '@/components/ui/PaginationBar';
import { MatchBadge } from '@/components/jobs/MatchBadge';
import { JobDrawer } from '@/components/jobs/JobDrawer';
import { HeartDecor } from '@/components/decor/Decor';

export default function CartPage() {
  const router = useRouter();
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
  const [detailJobId, setDetailJobId] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [toast, setToast] = useState<string | null>(null);

  const key = `/job-cart${buildQuery({ page, page_size: pageSize })}`;
  const { data, error, isLoading, mutate } = useSWR<Page<CartItem>>(key, fetcher, {
    keepPreviousData: true,
  });

  const items = data?.items ?? [];

  const toggleOne = useCallback((id: string) => {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }, []);

  function toggleAll() {
    setSelectedIds((prev) => {
      const allSelected = items.length > 0 && items.every((i) => prev.has(i.id));
      if (allSelected) return new Set();
      return new Set(items.map((i) => i.id));
    });
  }

  /** 批量移出岗位车。 */
  async function removeSelected() {
    const ids = [...selectedIds];
    if (ids.length === 0) return;

    setBusy(true);
    try {
      const res = await api.del<{ removed: number }>('/job-cart/batch', { job_ids: ids });
      setToast(`已移出 ${res.removed} 个岗位`);
      setSelectedIds(new Set());
      await mutate();
    } catch (err) {
      setToast(err instanceof Error ? err.message : '操作失败');
    } finally {
      setBusy(false);
    }
  }

  /** 批量创建投递任务。 */
  async function startPreparing() {
    const ids = [...selectedIds];
    if (ids.length === 0) return;

    setBusy(true);
    try {
      const res = await api.post<{ created: number; existing: number; skipped: number }>(
        '/applications/batch',
        { job_ids: ids },
      );
      setToast(
        `已创建 ${res.created} 个投递任务${res.existing > 0 ? `，${res.existing} 个此前已创建` : ''}`,
      );
      setSelectedIds(new Set());
      await mutate();
      // 引导用户前往投递进度页继续操作
      router.push('/applications');
    } catch (err) {
      setToast(err instanceof Error ? err.message : '操作失败');
    } finally {
      setBusy(false);
    }
  }

  const allSelected = items.length > 0 && items.every((i) => selectedIds.has(i.id));

  return (
    <>
      <PageHeader
        title="岗位车"
        subtitle="这里是你明确表示「准备投」的岗位。确认后再开始准备投递。"
      />

      <div className="flex-1 overflow-y-auto px-8 py-6">
        {toast ? (
          <div className="mb-4 flex items-start justify-between gap-3 rounded-card border border-lilac-200 bg-lilac-50 px-4 py-3">
            <p className="text-sm text-lilac-800">{toast}</p>
            <button
              type="button"
              onClick={() => setToast(null)}
              className="text-xs text-lilac-500 hover:text-lilac-700"
            >
              关闭
            </button>
          </div>
        ) : null}

        {selectedIds.size > 0 ? (
          <div className="mb-3 flex flex-wrap items-center justify-between gap-3 rounded-card border border-blossom-200 bg-blossom-50 px-4 py-2.5">
            <p className="text-sm text-blossom-800">已选择 {selectedIds.size} 个岗位</p>
            <div className="flex gap-2">
              <Button size="sm" variant="ghost" onClick={() => setSelectedIds(new Set())}>
                取消选择
              </Button>
              <Button size="sm" variant="danger" loading={busy} onClick={() => void removeSelected()}>
                移出岗位车
              </Button>
              <Button size="sm" loading={busy} onClick={() => void startPreparing()}>
                开始准备投递
              </Button>
            </div>
          </div>
        ) : null}

        <div className="table-shell">
          {error ? (
            <ErrorState message="加载岗位车失败" onRetry={() => void mutate()} />
          ) : isLoading && items.length === 0 ? (
            <TableSkeleton rows={5} cols={7} />
          ) : items.length === 0 ? (
            <EmptyState
              title="岗位车还是空的"
              description="到「岗位总览」勾选感兴趣的岗位，点击「加入岗位车」。"
              icon={<HeartDecor className="h-10 w-10" />}
              action={
                <Button size="sm" onClick={() => router.push('/jobs')}>
                  去看看岗位
                </Button>
              }
            />
          ) : (
            <>
              <div className="overflow-x-auto">
                <table className="w-full min-w-[900px] border-collapse">
                  <thead className="bg-lilac-50/60">
                    <tr>
                      <th className="th w-10">
                        <input
                          type="checkbox"
                          aria-label="全选"
                          checked={allSelected}
                          onChange={toggleAll}
                          className="h-4 w-4 rounded border-ink-300 text-lilac-500 focus:ring-lilac-300"
                        />
                      </th>
                      <th className="th">公司</th>
                      <th className="th">部门 / 业务</th>
                      <th className="th">岗位</th>
                      <th className="th">地点</th>
                      <th className="th w-24">匹配度</th>
                      <th className="th w-28">加入时间</th>
                      <th className="th w-28">投递状态</th>
                      <th className="th w-24 text-right">操作</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-ink-100">
                    {items.map((item) => (
                      <tr key={item.id} className="transition hover:bg-lilac-50/40">
                        <td className="td">
                          <input
                            type="checkbox"
                            aria-label={`选择 ${item.title}`}
                            checked={selectedIds.has(item.id)}
                            onChange={() => toggleOne(item.id)}
                            className="h-4 w-4 rounded border-ink-300 text-lilac-500 focus:ring-lilac-300"
                          />
                        </td>
                        <td className="td font-medium text-ink-800">{orDash(item.company_name)}</td>
                        <td className="td text-ink-600">
                          {orDash(firstDisplayValue(item.department, item.business))}
                        </td>
                        <td className="td">
                          <button
                            type="button"
                            onClick={() => setDetailJobId(item.id)}
                            className="text-left font-medium text-ink-800 hover:text-lilac-700"
                          >
                            {orDash(item.title)}
                          </button>
                        </td>
                        <td className="td text-ink-600">{orDash(item.location)}</td>
                        <td className="td">
                          <MatchBadge score={item.match_score} />
                        </td>
                        <td className="td text-xs text-ink-500">{formatRelative(item.added_at)}</td>
                        <td className="td">
                          <Badge tone={jobStatusToneOf(item.status)}>
                            {JOB_STATUS_LABELS[item.status] ?? item.status}
                          </Badge>
                        </td>
                        <td className="td text-right">
                          <Button size="sm" variant="ghost" onClick={() => setDetailJobId(item.id)}>
                            详细信息
                          </Button>
                        </td>
                      </tr>
                    ))}
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

      <JobDrawer
        jobId={detailJobId}
        onClose={() => setDetailJobId(null)}
        onAddToCart={async () => {
          /* 岗位车页面无需再次加入 */
        }}
      />
    </>
  );
}
