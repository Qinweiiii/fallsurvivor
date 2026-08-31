'use client';

import { Badge, jobStatusToneOf } from '@/components/ui/Badge';
import { Button } from '@/components/ui/Button';
import { MatchBadge } from './MatchBadge';
import { EmptyState, TableSkeleton } from '@/components/ui/States';
import { cn, JOB_STATUS_LABELS, orDash } from '@/lib/utils';
import type { Job } from '@/lib/types';

interface JobTableProps {
  jobs: Job[];
  loading: boolean;
  selectedIds: Set<string>;
  onToggle: (id: string) => void;
  onToggleAll: () => void;
  onOpenDetail: (job: Job) => void;
  emptyAction?: React.ReactNode;
}

/**
 * 岗位表格。
 * 只展示真正重要的字段，完整 JD 放在详情 Drawer 中。
 */
export function JobTable({
  jobs,
  loading,
  selectedIds,
  onToggle,
  onToggleAll,
  onOpenDetail,
  emptyAction,
}: JobTableProps) {
  if (loading) return <TableSkeleton rows={8} cols={8} />;

  if (jobs.length === 0) {
    return (
      <EmptyState
        title="还没有符合条件的岗位"
        description="试试放宽筛选条件，或点击右上角「获取岗位」让系统搜索一批。"
        action={emptyAction}
      />
    );
  }

  const allSelected = jobs.length > 0 && jobs.every((j) => selectedIds.has(j.id));

  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[980px] border-collapse">
        <thead className="bg-lilac-50/60">
          <tr>
            <th className="th w-10">
              <input
                type="checkbox"
                aria-label="全选本页岗位"
                checked={allSelected}
                onChange={onToggleAll}
                className="h-4 w-4 rounded border-ink-300 text-lilac-500 focus:ring-lilac-300"
              />
            </th>
            <th className="th">公司</th>
            <th className="th">部门 / 业务</th>
            <th className="th">岗位</th>
            <th className="th">地点</th>
            <th className="th w-24">匹配度</th>
            <th className="th w-28">状态</th>
            <th className="th w-24 text-right">操作</th>
          </tr>
        </thead>

        <tbody className="divide-y divide-ink-100">
          {jobs.map((job) => {
            const selected = selectedIds.has(job.id);
            return (
              <tr
                key={job.id}
                className={cn(
                  'transition hover:bg-lilac-50/40',
                  selected && 'bg-lilac-50/70',
                )}
              >
                <td className="td">
                  <input
                    type="checkbox"
                    aria-label={`选择 ${job.company_name} ${job.title}`}
                    checked={selected}
                    onChange={() => onToggle(job.id)}
                    className="h-4 w-4 rounded border-ink-300 text-lilac-500 focus:ring-lilac-300"
                  />
                </td>

                <td className="td font-medium text-ink-800">{orDash(job.company_name)}</td>

                <td
                  className={cn(
                    'td text-ink-600',
                    (job.department || job.business || '').includes('|') && 'td-preline',
                  )}
                >
                  {renderDeptOrBusiness(job.department || job.business)}
                </td>

                <td className="td">
                  <button
                    type="button"
                    onClick={() => onOpenDetail(job)}
                    className="text-left font-medium text-ink-800 hover:text-lilac-700"
                  >
                    {orDash(job.title)}
                  </button>
                  {/* JD 不完整标识：评分基于摘要片段，可信度较低。 */}
                  {job.desc_quality !== 'full' && (
                    <span
                      className="ml-1.5 inline-block rounded-pill bg-amber-100 px-1.5 py-0.5 align-middle text-[10px] font-medium text-amber-700"
                      title="JD 正文不完整（仅搜索摘要），匹配评分可信度较低，可点击「补全 JD」获取完整内容"
                    >
                      JD 不完整
                    </span>
                  )}
                </td>

                <td className="td text-ink-600">{orDash(job.location)}</td>

                <td className="td">
                  <MatchBadge score={job.match_score} />
                </td>

                <td className="td">
                  <Badge tone={jobStatusToneOf(job.status)}>
                    {JOB_STATUS_LABELS[job.status] ?? job.status}
                  </Badge>
                </td>

                <td className="td text-right">
                  <Button size="sm" variant="ghost" onClick={() => onOpenDetail(job)}>
                    详细信息
                  </Button>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

/**
 * 渲染部门/业务列。
 *
 * 输入可能形如「腾讯金融科技(CDG) | 腾讯营销(CDG) | ...」（腾讯校招多部门场景）。
 * 含「|」时按分隔符换行展示，其余沿用 orDash：空值显示「-」。
 */
function renderDeptOrBusiness(value: string | null | undefined): string {
  if (!value || !value.trim()) return '-';
  if (value.includes('|')) {
    return value.split('|').map((s) => s.trim()).filter(Boolean).join('\n');
  }
  return value;
}
