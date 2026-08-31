'use client';

import { useState } from 'react';
import { Button } from '@/components/ui/Button';
import { cn, JOB_STATUS_LABELS, SOURCE_LABELS } from '@/lib/utils';
import type { FilterOptions } from '@/lib/types';

/** 岗位筛选条件。全部为可组合的多条件。 */
export interface JobFilters {
  keyword: string;
  company: string;
  title: string;
  locations: string[];
  languages: string[];
  statuses: string[];
  sources: string[];
  min_score: number;
  sort_by: string;
  sort_order: string;
}

export const DEFAULT_FILTERS: JobFilters = {
  keyword: '',
  company: '',
  title: '',
  locations: [],
  languages: [],
  statuses: [],
  sources: [],
  min_score: 0,
  sort_by: 'match_score',
  sort_order: 'desc',
};

/** 岗位方向快捷关键词，来自产品文档第 10 节。 */
const TITLE_PRESETS = ['后端', 'AI 后端', 'Agent', 'AI 全栈', 'Infra'];

/** 匹配度档位。 */
const SCORE_PRESETS = [
  { label: '全部', value: 0 },
  { label: '80+', value: 80 },
  { label: '90+', value: 90 },
  { label: '95+', value: 95 },
];

const SORT_OPTIONS = [
  { label: '匹配度最高', by: 'match_score', order: 'desc' },
  { label: '最新发现', by: 'created_at', order: 'desc' },
  { label: '最近发布', by: 'published_at', order: 'desc' },
  { label: '最早截止', by: 'deadline', order: 'asc' },
];

interface JobFiltersPanelProps {
  value: JobFilters;
  options?: FilterOptions;
  onChange: (next: JobFilters) => void;
}

export function JobFiltersPanel({ value, options, onChange }: JobFiltersPanelProps) {
  const [expanded, setExpanded] = useState(false);

  function patch(partial: Partial<JobFilters>) {
    onChange({ ...value, ...partial });
  }

  function toggleInArray(key: 'locations' | 'languages' | 'statuses' | 'sources', item: string) {
    const list = value[key];
    const next = list.includes(item) ? list.filter((x) => x !== item) : [...list, item];
    patch({ [key]: next } as Partial<JobFilters>);
  }

  const activeCount =
    value.locations.length +
    value.languages.length +
    value.statuses.length +
    value.sources.length +
    (value.keyword ? 1 : 0) +
    (value.company ? 1 : 0) +
    (value.title ? 1 : 0) +
    (value.min_score > 0 ? 1 : 0);

  return (
    <section className="card mb-4 px-5 py-4">
      {/* 第一行：关键词 + 匹配度 + 排序 */}
      <div className="flex flex-wrap items-end gap-3">
        <div className="min-w-[220px] flex-1">
          <label className="label-text" htmlFor="job-keyword">
            关键词
          </label>
          <input
            id="job-keyword"
            className="input"
            placeholder="搜索公司 / 岗位 / 部门"
            value={value.keyword}
            onChange={(e) => patch({ keyword: e.target.value })}
          />
        </div>

        <div>
          <span className="label-text">匹配度</span>
          <div className="flex gap-1.5">
            {SCORE_PRESETS.map((p) => (
              <Chip
                key={p.value}
                active={value.min_score === p.value}
                onClick={() => patch({ min_score: p.value })}
              >
                {p.label}
              </Chip>
            ))}
          </div>
        </div>

        <div>
          <label className="label-text" htmlFor="job-sort">
            排序
          </label>
          <select
            id="job-sort"
            className="input h-10 w-40"
            value={`${value.sort_by}:${value.sort_order}`}
            onChange={(e) => {
              const [by, order] = e.target.value.split(':');
              patch({ sort_by: by ?? 'match_score', sort_order: order ?? 'desc' });
            }}
          >
            {SORT_OPTIONS.map((o) => (
              <option key={o.label} value={`${o.by}:${o.order}`}>
                {o.label}
              </option>
            ))}
          </select>
        </div>

        <Button variant="ghost" size="sm" onClick={() => setExpanded((v) => !v)}>
          {expanded ? '收起筛选' : `更多筛选${activeCount > 0 ? ` (${activeCount})` : ''}`}
        </Button>

        {activeCount > 0 ? (
          <Button variant="ghost" size="sm" onClick={() => onChange(DEFAULT_FILTERS)}>
            清空
          </Button>
        ) : null}
      </div>

      {expanded ? (
        <div className="mt-4 space-y-4 border-t border-ink-100 pt-4">
          {/* 岗位方向 */}
          <FilterRow label="岗位方向">
            <input
              className="input mb-2 max-w-xs"
              placeholder="自定义岗位关键词"
              value={value.title}
              onChange={(e) => patch({ title: e.target.value })}
            />
            <div className="flex flex-wrap gap-1.5">
              {TITLE_PRESETS.map((t) => (
                <Chip key={t} active={value.title === t} onClick={() => patch({ title: value.title === t ? '' : t })}>
                  {t}
                </Chip>
              ))}
            </div>
          </FilterRow>

          {/* 公司 */}
          <FilterRow label="公司">
            <input
              className="input max-w-xs"
              placeholder="公司名包含"
              value={value.company}
              onChange={(e) => patch({ company: e.target.value })}
              list="company-options"
            />
            <datalist id="company-options">
              {(options?.companies ?? []).map((c) => (
                <option key={c} value={c} />
              ))}
            </datalist>
          </FilterRow>

          {/* 城市 */}
          <FilterRow label="城市">
            <div className="flex flex-wrap gap-1.5">
              {(options?.locations ?? []).slice(0, 24).map((loc) => (
                <Chip
                  key={loc}
                  active={value.locations.includes(loc)}
                  onClick={() => toggleInArray('locations', loc)}
                >
                  {loc}
                </Chip>
              ))}
              {(options?.locations ?? []).length === 0 ? (
                <span className="text-xs text-ink-400">获取岗位后这里会出现城市选项</span>
              ) : null}
            </div>
          </FilterRow>

          {/* 技术语言 */}
          <FilterRow label="技术语言">
            <div className="flex flex-wrap gap-1.5">
              {(options?.languages ?? ['Go', 'Python', 'Java', 'C++']).map((lang) => (
                <Chip
                  key={lang}
                  active={value.languages.includes(lang)}
                  onClick={() => toggleInArray('languages', lang)}
                >
                  {lang}
                </Chip>
              ))}
            </div>
          </FilterRow>

          {/* 状态 */}
          <FilterRow label="状态">
            <div className="flex flex-wrap gap-1.5">
              {(options?.statuses ?? []).map((s) => (
                <Chip
                  key={s}
                  active={value.statuses.includes(s)}
                  onClick={() => toggleInArray('statuses', s)}
                >
                  {JOB_STATUS_LABELS[s] ?? s}
                </Chip>
              ))}
            </div>
          </FilterRow>

          {/* 来源 */}
          <FilterRow label="来源">
            <div className="flex flex-wrap gap-1.5">
              {(options?.sources ?? []).map((s) => (
                <Chip
                  key={s}
                  active={value.sources.includes(s)}
                  onClick={() => toggleInArray('sources', s)}
                >
                  {SOURCE_LABELS[s] ?? s}
                </Chip>
              ))}
            </div>
          </FilterRow>
        </div>
      ) : null}
    </section>
  );
}

function FilterRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1.5 sm:flex-row sm:items-start sm:gap-4">
      <span className="w-20 shrink-0 pt-1 text-xs font-medium text-ink-500">{label}</span>
      <div className="min-w-0 flex-1">{children}</div>
    </div>
  );
}

function Chip({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        'rounded-pill border px-2.5 py-1 text-xs font-medium transition',
        active
          ? 'border-lilac-300 bg-lilac-100 text-lilac-700'
          : 'border-ink-200 bg-white text-ink-600 hover:border-lilac-200 hover:text-lilac-700',
      )}
    >
      {children}
    </button>
  );
}
