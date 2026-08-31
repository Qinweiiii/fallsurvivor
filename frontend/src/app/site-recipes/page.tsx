'use client';

import { useState } from 'react';
import useSWR from 'swr';
import { fetcher, siteRecipesApi } from '@/lib/api';
import type { SiteRecipe, SiteRecipeRun, SiteStrategy } from '@/lib/types';
import { PageHeader } from '@/components/layout/PageHeader';
import { Button } from '@/components/ui/Button';
import { ErrorState, Skeleton } from '@/components/ui/States';

interface ListData {
  recipes: SiteRecipe[];
  count: number;
}

const STRATEGY_LABEL: Record<SiteStrategy, string> = {
  browser: '浏览器抽取',
  api: '接口直调',
  url_template: 'URL 模板',
};

const SOURCE_LABEL: Record<string, string> = {
  preset: '内置',
  manual: '手动',
  exploration: '探索',
};

/** 新建配置的初始草稿。 */
function blankDraft(): Partial<SiteRecipe> {
  return {
    site_key: '',
    company_name: '',
    domain: '',
    campus_url: '',
    strategy_type: 'browser',
    adapter_key: '',
    enabled: true,
    max_jobs_per_search: 20,
    max_detail_fetches: 8,
    notes: '',
  };
}

export default function SiteRecipesPage() {
  const { data, error, isLoading, mutate } = useSWR<ListData>('/site-recipes', fetcher);

  const [editing, setEditing] = useState<Partial<SiteRecipe> | null>(null);
  const [isNew, setIsNew] = useState(false);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);

  function startCreate() {
    setEditing(blankDraft());
    setIsNew(true);
    setMsg(null);
  }

  function startEdit(r: SiteRecipe) {
    setEditing({ ...r });
    setIsNew(false);
    setMsg(null);
  }

  async function save() {
    if (!editing) return;
    setBusy(true);
    setMsg(null);
    try {
      if (isNew) {
        await siteRecipesApi.create(editing);
        setMsg('站点配置已创建');
      } else if (editing.id) {
        await siteRecipesApi.update(editing.id, editing);
        setMsg('站点配置已保存');
      }
      setEditing(null);
      await mutate();
    } catch (err) {
      setMsg(err instanceof Error ? err.message : '保存失败');
    } finally {
      setBusy(false);
    }
  }

  async function remove(id: string, name: string) {
    if (!confirm(`确定删除「${name}」的站点配置吗？`)) return;
    setBusy(true);
    try {
      await siteRecipesApi.remove(id);
      await mutate();
    } catch (err) {
      setMsg(err instanceof Error ? err.message : '删除失败');
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <PageHeader
        title="站点采集配置"
        subtitle="配置哪些公司走官网直采，以及每家公司的采集限制。新增一家公司只需加一条配置，无需改代码"
        actions={
          <Button onClick={startCreate} disabled={busy}>
            新增站点
          </Button>
        }
      />

      <div className="flex-1 overflow-auto px-8 py-6">
        {msg && (
          <div className="mb-4 rounded-lg bg-lilac-50 px-4 py-2 text-sm text-lilac-800">{msg}</div>
        )}

        {isLoading ? (
          <Skeleton />
        ) : error ? (
          <ErrorState message="加载失败" onRetry={() => mutate()} />
        ) : !data || data.count === 0 ? (
          <div className="rounded-card border border-lilac-100 bg-white/60 p-8 text-center text-sm text-ink-400">
            暂无站点配置。点击「新增站点」开始。
          </div>
        ) : (
          <div className="space-y-3">
            {data.recipes.map((r) => (
              <RecipeCard
                key={r.id}
                recipe={r}
                busy={busy}
                onEdit={() => startEdit(r)}
                onDelete={() => remove(r.id, r.company_name)}
              />
            ))}
          </div>
        )}

        {editing && (
          <EditModal
            draft={editing}
            isNew={isNew}
            busy={busy}
            onChange={setEditing}
            onSave={save}
            onClose={() => setEditing(null)}
          />
        )}
      </div>
    </>
  );
}

/** 单个站点配置卡片。 */
function RecipeCard({
  recipe,
  busy,
  onEdit,
  onDelete,
}: {
  recipe: SiteRecipe;
  busy: boolean;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const [showRuns, setShowRuns] = useState(false);

  return (
    <div
      className={`rounded-card border bg-white/70 p-4 shadow-card ${
        recipe.enabled ? 'border-lilac-100' : 'border-ink-100 opacity-60'
      }`}
    >
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <h3 className="text-base font-semibold text-ink-800">{recipe.company_name}</h3>
            <span className="rounded-pill bg-card-lilac px-2 py-0.5 text-[10px] font-semibold text-lilac-700">
              {STRATEGY_LABEL[recipe.strategy_type] ?? recipe.strategy_type}
            </span>
            <span className="rounded-pill bg-ink-50 px-2 py-0.5 text-[10px] text-ink-500">
              {SOURCE_LABEL[recipe.source] ?? recipe.source} v{recipe.version}
            </span>
            {!recipe.enabled && (
              <span className="rounded-pill bg-ink-100 px-2 py-0.5 text-[10px] text-ink-500">
                已禁用
              </span>
            )}
          </div>

          <p className="mt-1 font-mono text-xs text-ink-500">
            {recipe.domain} · {recipe.site_key}
            {recipe.adapter_key && ` · adapter=${recipe.adapter_key}`}
          </p>

          {recipe.campus_url && (
            <p className="mt-1 truncate text-xs text-ink-400">入口：{recipe.campus_url}</p>
          )}

          <div className="mt-2 flex gap-4 text-xs text-ink-500">
            <span>单次最多 {recipe.max_jobs_per_search} 条</span>
            <span>
              详情抓取{' '}
              {recipe.max_detail_fetches === 0
                ? '不抓（用抽取阶段 JD）'
                : `${recipe.max_detail_fetches} 条`}
            </span>
          </div>

          {recipe.notes && (
            <p className="mt-2 rounded-lg bg-ink-50 px-3 py-2 text-xs leading-relaxed text-ink-600">
              {recipe.notes}
            </p>
          )}
        </div>

        <div className="flex shrink-0 gap-2">
          <Button variant="ghost" onClick={() => setShowRuns(!showRuns)}>
            执行记录
          </Button>
          <Button variant="ghost" onClick={onEdit} disabled={busy}>
            编辑
          </Button>
          <Button variant="ghost" onClick={onDelete} disabled={busy}>
            删除
          </Button>
        </div>
      </div>

      {showRuns && <RunList recipeId={recipe.id} />}
    </div>
  );
}

/** 执行记录列表（按需加载）。 */
function RunList({ recipeId }: { recipeId: string }) {
  const { data, isLoading } = useSWR<{ runs: SiteRecipeRun[]; count: number }>(
    `/site-recipes/${recipeId}/runs`,
    fetcher,
  );

  if (isLoading) return <p className="mt-3 text-xs text-ink-400">加载中…</p>;
  if (!data || data.count === 0)
    return <p className="mt-3 text-xs text-ink-400">暂无执行记录</p>;

  return (
    <div className="mt-3 border-t border-lilac-50 pt-3">
      <table className="w-full text-xs">
        <thead>
          <tr className="text-left text-ink-400">
            <th className="py-1">时间</th>
            <th className="py-1">关键词</th>
            <th className="py-1">状态</th>
            <th className="py-1">采集</th>
            <th className="py-1">耗时</th>
          </tr>
        </thead>
        <tbody>
          {data.runs.slice(0, 10).map((run) => (
            <tr key={run.id} className="border-t border-ink-50">
              <td className="py-1 text-ink-500">
                {new Date(run.created_at).toLocaleString('zh-CN', { hour12: false })}
              </td>
              <td className="py-1 text-ink-600">{run.keyword || '-'}</td>
              <td className="py-1">
                <span
                  className={
                    run.status === 'success'
                      ? 'text-emerald-600'
                      : run.status === 'failed'
                        ? 'text-rose-600'
                        : 'text-ink-400'
                  }
                >
                  {run.status === 'success' ? '成功' : run.status === 'failed' ? '失败' : '空结果'}
                </span>
                {run.error_message && (
                  <span className="ml-1 text-ink-400" title={run.error_message}>
                    ⚠
                  </span>
                )}
              </td>
              <td className="py-1 text-ink-600">{run.jobs_found}</td>
              <td className="py-1 text-ink-500">{(run.duration_ms / 1000).toFixed(1)}s</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/** 新增/编辑弹窗。 */
function EditModal({
  draft,
  isNew,
  busy,
  onChange,
  onSave,
  onClose,
}: {
  draft: Partial<SiteRecipe>;
  isNew: boolean;
  busy: boolean;
  onChange: (d: Partial<SiteRecipe>) => void;
  onSave: () => void;
  onClose: () => void;
}) {
  const set = <K extends keyof SiteRecipe>(key: K, value: SiteRecipe[K] | undefined) =>
    onChange({ ...draft, [key]: value });

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-ink-900/40 p-4">
      <div className="max-h-[90vh] w-full max-w-lg overflow-auto rounded-card bg-white p-6 shadow-xl">
        <h2 className="mb-4 text-lg font-semibold text-ink-800">
          {isNew ? '新增站点' : '编辑站点'}
        </h2>

        <div className="space-y-3">
          <Field label="公司名">
            <input
              className="input"
              value={draft.company_name ?? ''}
              onChange={(e) => set('company_name', e.target.value)}
              placeholder="腾讯"
            />
          </Field>

          <Field label="站点标识（英文，唯一）">
            <input
              className="input"
              value={draft.site_key ?? ''}
              onChange={(e) => set('site_key', e.target.value)}
              placeholder="tencent"
              disabled={!isNew}
            />
          </Field>

          <Field label="域名（用于识别 URL 归属）">
            <input
              className="input"
              value={draft.domain ?? ''}
              onChange={(e) => set('domain', e.target.value)}
              placeholder="join.qq.com"
            />
          </Field>

          <Field label="校招入口 URL">
            <input
              className="input"
              value={draft.campus_url ?? ''}
              onChange={(e) => set('campus_url', e.target.value)}
              placeholder="https://join.qq.com/post.html?query=p_1"
            />
          </Field>

          <Field label="采集策略">
            <select
              className="input"
              value={draft.strategy_type ?? 'browser'}
              onChange={(e) => set('strategy_type', e.target.value as SiteStrategy)}
            >
              <option value="browser">浏览器抽取（已实现）</option>
              <option value="api">接口直调（未实现）</option>
              <option value="url_template">URL 模板（未实现）</option>
            </select>
          </Field>

          <Field label="适配器 key（浏览器策略必填）">
            <input
              className="input"
              value={draft.adapter_key ?? ''}
              onChange={(e) => set('adapter_key', e.target.value)}
              placeholder="tencent"
            />
          </Field>

          <div className="grid grid-cols-2 gap-3">
            <Field label="单次最多返回">
              <input
                className="input"
                type="number"
                min={1}
                value={draft.max_jobs_per_search ?? 20}
                onChange={(e) => set('max_jobs_per_search', Number(e.target.value))}
              />
            </Field>
            <Field label="详情抓取条数">
              <input
                className="input"
                type="number"
                min={0}
                value={draft.max_detail_fetches ?? 8}
                onChange={(e) => set('max_detail_fetches', Number(e.target.value))}
              />
            </Field>
          </div>

          <Field label="备注">
            <textarea
              className="input min-h-[80px]"
              value={draft.notes ?? ''}
              onChange={(e) => set('notes', e.target.value)}
              placeholder="站点特征、已知坑、登录要求…"
            />
          </Field>

          <label className="flex items-center gap-2 text-sm text-ink-700">
            <input
              type="checkbox"
              checked={draft.enabled ?? true}
              onChange={(e) => set('enabled', e.target.checked)}
            />
            启用该站点
          </label>
        </div>

        <div className="mt-6 flex justify-end gap-2">
          <Button variant="ghost" onClick={onClose} disabled={busy}>
            取消
          </Button>
          <Button onClick={onSave} disabled={busy}>
            {busy ? '保存中…' : '保存'}
          </Button>
        </div>
      </div>
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="block">
      <span className="mb-1 block text-xs font-medium text-ink-500">{label}</span>
      {children}
    </label>
  );
}
