'use client';

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import useSWR from 'swr';
import { api, buildQuery, fetcher, ApiError, enrichmentApi } from '@/lib/api';
import type { FilterOptions, Job, Page, SearchTask } from '@/lib/types';
import { SEARCH_STATUS_LABELS } from '@/lib/utils';
import { PageHeader } from '@/components/layout/PageHeader';
import { Button } from '@/components/ui/Button';
import { ErrorState } from '@/components/ui/States';
import { PaginationBar } from '@/components/ui/PaginationBar';
import { JobTable } from '@/components/jobs/JobTable';
import { JobDrawer } from '@/components/jobs/JobDrawer';
import { DEFAULT_FILTERS, JobFiltersPanel, type JobFilters } from '@/components/jobs/JobFiltersPanel';
import { WandDecor } from '@/components/decor/Decor';

export default function JobsPage() {
  const [filters, setFilters] = useState<JobFilters>(DEFAULT_FILTERS);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
  const [detailJobId, setDetailJobId] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [enriching, setEnriching] = useState(false);
  const [toast, setToast] = useState<string | null>(null);

  // 搜索任务轮询
  const [taskId, setTaskId] = useState<string | null>(null);
  const [searching, setSearching] = useState(false);

  // 误触检索时的冲突弹窗（已有任务进行中）。仅保存最小信息用于触发轮询与展示。
  const [conflictTask, setConflictTask] = useState<
    | { id: string; status: string; query_count: number; found_count: number; new_count: number }
    | null
  >(null);

  // 关键词等文本输入做防抖，避免每次按键都请求
  const debouncedFilters = useDebounced(filters, 350);

  const listKey = useMemo(
    () =>
      `/jobs${buildQuery({
        page,
        page_size: pageSize,
        keyword: debouncedFilters.keyword,
        company: debouncedFilters.company,
        title: debouncedFilters.title,
        locations: debouncedFilters.locations,
        languages: debouncedFilters.languages,
        statuses: debouncedFilters.statuses,
        sources: debouncedFilters.sources,
        min_score: debouncedFilters.min_score || undefined,
        sort_by: debouncedFilters.sort_by,
        sort_order: debouncedFilters.sort_order,
      })}`,
    [page, pageSize, debouncedFilters],
  );

  const { data, error, isLoading, mutate } = useSWR<Page<Job>>(listKey, fetcher, {
    keepPreviousData: true,
  });

  const { data: options } = useSWR<FilterOptions>('/jobs/filter-options', fetcher);

  // 轮询搜索任务进度
  const { data: task } = useSWR<SearchTask>(
    taskId ? `/search-tasks/${taskId}` : null,
    fetcher,
    { refreshInterval: searching ? 2500 : 0 },
  );

  // 冲突弹窗：误触检索时展示当前进行中任务，并轮询直到其结束
  const { data: conflictPoll } = useSWR<SearchTask>(
    conflictTask ? `/search-tasks/${conflictTask.id}` : null,
    fetcher,
    { refreshInterval: 2500 },
  );

  useEffect(() => {
    if (!conflictPoll) return;
    const done =
      conflictPoll.status === 'COMPLETED' ||
      conflictPoll.status === 'COMPLETED_WITH_WARNING' ||
      conflictPoll.status === 'FAILED' ||
      conflictPoll.status === 'TERMINATED';
    if (!done) return;
    // 任务已结束，自动关闭弹窗并刷新列表
    setConflictTask(null);
    setToast(
      conflictPoll.status === 'TERMINATED'
        ? '上一次搜索任务已被终止（服务关闭/重启）'
        : conflictPoll.status === 'FAILED'
          ? conflictPoll.error_message || '上一次搜索任务失败'
          : `搜索已完成，本次新增 ${conflictPoll.new_count} 个岗位`,
    );
    void mutate();
  }, [conflictPoll, mutate]);

  useEffect(() => {
    if (!task) return;
    const done =
      task.status === 'COMPLETED' ||
      task.status === 'COMPLETED_WITH_WARNING' ||
      task.status === 'FAILED' ||
      task.status === 'TERMINATED';
    if (!done) return;

    setSearching(false);
    if (task.status === 'TERMINATED') {
      setToast('搜索任务已被终止（服务关闭或重启），未获取到新岗位');
      void mutate();
    } else if (task.status === 'FAILED') {
      setToast(task.error_message || '获取岗位失败，请稍后重试');
    } else {
      const warn = task.warnings.length > 0 ? `（${task.warnings.join('；')}）` : '';
      setToast(
        `本次新增 ${task.new_count} 个岗位，发现 ${task.duplicate_count} 个重复岗位${warn}`,
      );
      void mutate();
    }
  }, [task, mutate]);

  // 筛选条件变化时回到第一页
  useEffect(() => {
    setPage(1);
  }, [debouncedFilters]);

  const jobs = useMemo(() => data?.items ?? [], [data?.items]);

  const toggleOne = useCallback((id: string) => {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }, []);

  const toggleAll = useCallback(() => {
    setSelectedIds((prev) => {
      const allSelected = jobs.length > 0 && jobs.every((j) => prev.has(j.id));
      if (allSelected) {
        const next = new Set(prev);
        for (const j of jobs) next.delete(j.id);
        return next;
      }
      const next = new Set(prev);
      for (const j of jobs) next.add(j.id);
      return next;
    });
  }, [jobs]);

  /** 触发一次岗位搜索。 */
  async function startSearch(force = false) {
    setBusy(true);
    setToast(null);
    try {
      const res = await api.post<{ task_id: string; status: string }>('/jobs/search', { force });
      setTaskId(res.task_id);
      setSearching(true);
      setToast('正在获取岗位……这可能需要一两分钟，你可以先做别的事');
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        // 后端在 409 时一并返回当前进行中任务的信息，用于弹窗展示与轮询。
        const d = (err.data ?? {}) as {
          task_id?: string;
          status?: string;
          query_count?: number;
          found_count?: number;
          new_count?: number;
        };
        if (d.task_id) {
          setConflictTask({
            id: d.task_id,
            status: d.status ?? 'RUNNING',
            query_count: d.query_count ?? 0,
            found_count: d.found_count ?? 0,
            new_count: d.new_count ?? 0,
          });
        } else {
          setToast('已有搜索任务正在执行，请稍候');
        }
      } else {
        setToast(err instanceof Error ? err.message : '触发失败');
      }
    } finally {
      setBusy(false);
    }
  }

  /** 批量加入岗位车。 */
  async function addSelectedToCart() {
    const ids = [...selectedIds];
    if (ids.length === 0) return;

    setBusy(true);
    try {
      const res = await api.post<{ added: number; existing: number; skipped: number }>(
        '/job-cart/batch',
        { job_ids: ids },
      );
      setToast(
        `已加入 ${res.added} 个岗位到岗位车${res.existing > 0 ? `，${res.existing} 个此前已加入` : ''}`,
      );
      setSelectedIds(new Set());
      await mutate();
    } catch (err) {
      setToast(err instanceof Error ? err.message : '操作失败');
    } finally {
      setBusy(false);
    }
  }

  /** 单个加入岗位车（Drawer 内使用）。 */
  async function addOneToCart(jobId: string) {
    await api.post('/job-cart', { job_id: jobId });
    setToast('已加入岗位车');
    await mutate();
  }

  /**
   * 补全 JD：为摘要型岗位（如 BOSS，受反爬限制只能拿到片段）补齐完整正文。
   *
   * 若当前有选中岗位，只补全选中的；否则自动挑选全部 JD 不完整的岗位。
   * 依赖用户在 Worker 浏览器中已登录对应站点。
   */
  async function enrichJD() {
    setEnriching(true);
    setToast(null);
    try {
      const body =
        selectedIds.size > 0 ? { job_ids: [...selectedIds] } : {};
      const res = await enrichmentApi.enrich(body);
      if (res.total === 0) {
        setToast('没有需要补全的岗位（所有岗位都已有完整 JD）');
      } else {
        const parts = [`补全完成：成功 ${res.enriched}`];
        if (res.skipped > 0) parts.push(`跳过 ${res.skipped}`);
        if (res.failed > 0) parts.push(`失败 ${res.failed}`);
        // 补全会重算评分，提示分数变化最大的几条。
        const changed = res.results
          .filter((r) => r.status === 'enriched')
          .map((r) => `${r.title} ${r.old_score}→${r.new_score}`)
          .slice(0, 3);
        if (changed.length > 0) parts.push(`评分更新：${changed.join('、')}`);
        setToast(parts.join('，'));
      }
      await mutate();
    } catch (err) {
      setToast(err instanceof Error ? err.message : '补全失败');
    } finally {
      setEnriching(false);
    }
  }

  return (
    <>
      <PageHeader
        title="岗位总览"
        subtitle="根据你的求职画像，为你发现值得关注的岗位。"
        actions={
          <>
            {searching ? (
              <Button variant="secondary" loading>
                正在获取岗位
              </Button>
            ) : (
              <Button
                loading={busy}
                icon={<WandDecor className="h-4 w-4" />}
                onClick={() => void startSearch(false)}
              >
                获取岗位
              </Button>
            )}
            {/* JD 补全：为 BOSS 等只能拿到摘要的岗位补齐完整 JD 并重算评分。 */}
            <Button
              variant="ghost"
              loading={enriching}
              onClick={() => void enrichJD()}
              title="复用已登录的浏览器打开岗位原网页，读取完整 JD 后重新评分"
            >
              补全 JD
            </Button>
          </>
        }
      />

      <div className="flex-1 overflow-y-auto px-8 py-6">
        {/* 搜索进度与结果提示 */}
        {searching && task ? (
          <div className="card mb-4 flex flex-wrap items-center gap-x-6 gap-y-2 px-5 py-3 text-xs text-ink-600">
            <span className="font-medium text-lilac-700">
              {SEARCH_STATUS_LABELS[task.status] ?? task.status}
            </span>
            <span>检索式 {task.query_count}</span>
            <span>发现 {task.found_count}</span>
            <span>新增 {task.new_count}</span>
            <span>重复 {task.duplicate_count}</span>
          </div>
        ) : null}

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

        <JobFiltersPanel value={filters} options={options} onChange={setFilters} />

        {/* 批量操作条 */}
        {selectedIds.size > 0 ? (
          <div className="mb-3 flex items-center justify-between rounded-card border border-blossom-200 bg-blossom-50 px-4 py-2.5">
            <p className="text-sm text-blossom-800">已选择 {selectedIds.size} 个岗位</p>
            <div className="flex gap-2">
              <Button size="sm" variant="ghost" onClick={() => setSelectedIds(new Set())}>
                取消选择
              </Button>
              <Button size="sm" loading={busy} onClick={() => void addSelectedToCart()}>
                加入岗位车
              </Button>
            </div>
          </div>
        ) : null}

        <div className="table-shell">
          {error ? (
            <ErrorState message="加载岗位列表失败" onRetry={() => void mutate()} />
          ) : (
            <>
              <JobTable
                jobs={jobs}
                loading={isLoading && jobs.length === 0}
                selectedIds={selectedIds}
                onToggle={toggleOne}
                onToggleAll={toggleAll}
                onOpenDetail={(job) => setDetailJobId(job.id)}
                emptyAction={
                  <Button size="sm" onClick={() => void startSearch(true)} loading={busy}>
                    立即获取岗位
                  </Button>
                }
              />

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
        onAddToCart={addOneToCart}
        busy={busy}
      />

      {/* 误触检索时的冲突提示弹窗 */}
      {conflictTask ? (
        <ConflictModal
          task={conflictPoll ?? conflictTask}
          onBackground={() => {
            // 转到后台等待：交给主任务进度条继续轮询，弹窗关闭。
            setTaskId(conflictTask.id);
            setSearching(true);
            setConflictTask(null);
          }}
        />
      ) : null}
    </>
  );
}

/** 误触检索时的冲突提示弹窗：告知当前已有任务进行中，需等待完成。 */
function ConflictModal({
  task,
  onBackground,
}: {
  task: { status: string; query_count: number; found_count: number; new_count: number };
  onBackground: () => void;
}) {
  const statusLabel = SEARCH_STATUS_LABELS[task.status] ?? task.status;
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-ink-900/40 px-4">
      <div className="w-full max-w-md rounded-card border border-ink-100 bg-white p-6 shadow-xl">
        <div className="mb-3 flex items-center gap-2 text-amber-600">
          <span className="text-xl">⚠️</span>
          <h3 className="text-base font-semibold text-ink-800">已有搜索任务正在进行</h3>
        </div>
        <p className="text-sm leading-relaxed text-ink-600">
          系统检测到你当前有一个岗位搜索任务尚未完成。为避免重复占用资源，请
          <span className="font-medium text-lilac-700"> 等待当前任务跑完 </span>
          后再发起新的检索。
        </p>

        <div className="mt-4 rounded-card bg-ink-50 px-4 py-3 text-xs text-ink-600">
          <div className="mb-1 font-medium text-lilac-700">{statusLabel}</div>
          <div className="flex flex-wrap gap-x-5 gap-y-1">
            <span>检索式 {task.query_count}</span>
            <span>发现 {task.found_count}</span>
            <span>新增 {task.new_count}</span>
          </div>
        </div>

        <div className="mt-5 flex justify-end">
          <Button size="sm" onClick={onBackground}>
            知道了，后台等待
          </Button>
        </div>
        <p className="mt-2 text-center text-[11px] text-ink-400">
          任务完成后页面会自动刷新并提示结果
        </p>
      </div>
    </div>
  );
}

/** 简单的防抖 Hook。 */
function useDebounced<T>(value: T, delay: number): T {
  const [debounced, setDebounced] = useState(value);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    if (timer.current) clearTimeout(timer.current);
    timer.current = setTimeout(() => setDebounced(value), delay);
    return () => {
      if (timer.current) clearTimeout(timer.current);
    };
  }, [value, delay]);

  return debounced;
}
