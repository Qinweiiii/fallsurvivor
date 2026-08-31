'use client';

import { cn } from '@/lib/utils';
import { Button } from './Button';

interface PaginationBarProps {
  page: number;
  pageSize: number;
  total: number;
  totalPages: number;
  onPageChange: (page: number) => void;
  onPageSizeChange: (size: number) => void;
}

const PAGE_SIZES = [20, 50, 100];

/** 分页控件。分页由后端完成，此处只负责发起请求。 */
export function PaginationBar({
  page,
  pageSize,
  total,
  totalPages,
  onPageChange,
  onPageSizeChange,
}: PaginationBarProps) {
  const from = total === 0 ? 0 : (page - 1) * pageSize + 1;
  const to = Math.min(page * pageSize, total);

  return (
    <div className="flex flex-wrap items-center justify-between gap-3 px-4 py-3">
      <div className="flex items-center gap-3 text-xs text-ink-500">
        <span>
          共 {total} 条，当前 {from}–{to}
        </span>
        <label className="flex items-center gap-1.5">
          每页
          <select
            value={pageSize}
            onChange={(e) => onPageSizeChange(Number(e.target.value))}
            className="rounded-lg border border-ink-200 bg-white px-2 py-1 text-xs text-ink-700"
          >
            {PAGE_SIZES.map((s) => (
              <option key={s} value={s}>
                {s}
              </option>
            ))}
          </select>
          条
        </label>
      </div>

      <div className="flex items-center gap-1.5">
        <Button
          size="sm"
          variant="secondary"
          disabled={page <= 1}
          onClick={() => onPageChange(page - 1)}
        >
          上一页
        </Button>

        {buildPageList(page, totalPages).map((p, i) =>
          p === '...' ? (
            <span key={`gap-${i}`} className="px-1.5 text-xs text-ink-400">
              ...
            </span>
          ) : (
            <button
              key={p}
              type="button"
              onClick={() => onPageChange(p)}
              className={cn(
                'h-8 min-w-8 rounded-pill px-2.5 text-xs font-medium transition',
                p === page
                  ? 'bg-lilac-500 text-white shadow-soft'
                  : 'text-ink-600 hover:bg-lilac-50 hover:text-lilac-700',
              )}
            >
              {p}
            </button>
          ),
        )}

        <Button
          size="sm"
          variant="secondary"
          disabled={page >= totalPages}
          onClick={() => onPageChange(page + 1)}
        >
          下一页
        </Button>
      </div>
    </div>
  );
}

/** 生成带省略号的页码列表。 */
function buildPageList(current: number, total: number): (number | '...')[] {
  if (total <= 0) return [1];
  if (total <= 7) return Array.from({ length: total }, (_, i) => i + 1);

  const out: (number | '...')[] = [1];
  const start = Math.max(2, current - 1);
  const end = Math.min(total - 1, current + 1);

  if (start > 2) out.push('...');
  for (let i = start; i <= end; i++) out.push(i);
  if (end < total - 1) out.push('...');
  out.push(total);
  return out;
}
