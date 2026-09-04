'use client';

import { useState } from 'react';
import useSWR from 'swr';
import { fetcher, jobsApi, siteRecipesApi } from '@/lib/api';
import type {
  SiteCrawlResult,
  SiteRecipe,
  SiteRecipeFieldQuality,
  SiteRecipeRun,
  SiteStrategy,
  SiteVerifyStatus,
} from '@/lib/types';
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
  browser_observed: '浏览器页面观测',
  url_template: 'URL 模板',
};

const SOURCE_LABEL: Record<string, string> = {
  preset: '内置',
  manual: '手动',
  exploration: '探索',
};

/**
 * 验证状态的展示样式。
 *
 * 这一列是整个自愈闭环对用户最直观的体现：
 * 「已验证」= Agent 自己试跑通过；「已失效」= 下次会自动重新摸索。
 */
const VERIFY_STYLE: Record<SiteVerifyStatus, { label: string; className: string }> = {
  verified: { label: '已验证', className: 'bg-emerald-50 text-emerald-700' },
  unverified: { label: '未验证', className: 'bg-ink-100 text-ink-500' },
  invalid: { label: '已失效 · 待重探', className: 'bg-rose-50 text-rose-700' },
};

const FIELD_LABELS: Record<string, string> = {
  title: '岗位',
  description: '职责/要求',
  company: '公司',
  location: '地点',
  department: '部门',
  business: '业务',
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
    list_api: '',
    detail_api: '',
    detail_url_template: '',
    method: 'GET',
    id_field: '',
    title_field: '',
    list_path: '',
    keyword_param: '',
    field_map: {},
    request_body: '',
    request_content_type: '',
    request_headers: {},
    keyword_in_body: false,
    verify_status: 'unverified',
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
  const [lastVerify, setLastVerify] = useState<Record<string, SiteRecipeFieldQuality[]>>({});
  const [lastCrawl, setLastCrawl] = useState<Record<string, SiteCrawlResult>>({});

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

  /**
   * 用真实请求验证一条配置。
   *
   * 这是「配置能不能跑」的唯一可信答案——读配置猜不出来，
   * 只能发一次真实请求看是否解析出岗位。结果会写回健康度，
   * 因此完成后要刷新列表让状态徽标同步。
   */
  async function verify(recipe: SiteRecipe) {
    setBusy(true);
    setMsg(null);
    try {
      const res = await siteRecipesApi.verify(recipe.id);
      setLastVerify((prev) => ({ ...prev, [recipe.id]: res.field_quality ?? [] }));
      if (res.ok) {
        const samples = res.sample_titles?.length ? `：${res.sample_titles.join(' / ')}` : '';
        setMsg(`「${recipe.company_name}」验证通过，采到 ${res.jobs_found} 条${samples}`);
      } else {
        setMsg(`「${recipe.company_name}」验证失败：${res.error ?? '未知原因'}`);
      }
      await mutate();
    } catch (err) {
      setMsg(err instanceof Error ? err.message : '验证失败');
    } finally {
      setBusy(false);
    }
  }

  async function crawlRecipe(recipe: SiteRecipe, mode: 'explore' | 'crawl') {
    if (!recipe.campus_url) {
      setMsg(`「${recipe.company_name}」缺少校招入口 URL`);
      return;
    }
    const keyword = window.prompt('输入本次检索关键词', '算法');
    if (keyword === null) return;

    setBusy(true);
    setMsg(null);
    try {
      const res = await jobsApi.crawl({
        site_key: recipe.site_key,
        company: recipe.company_name,
        url: recipe.campus_url,
        keyword: keyword.trim(),
        strategy: mode === 'explore' ? 'explore' : undefined,
        save_as_recipe: mode === 'explore',
      });
      setLastCrawl((prev) => ({ ...prev, [recipe.id]: res }));
      if (res.field_quality) {
        setLastVerify((prev) => ({ ...prev, [recipe.id]: res.field_quality ?? [] }));
      }
      setMsg(formatCrawlMessage(recipe.company_name, res));
      await mutate();
    } catch (err) {
      setMsg(err instanceof Error ? err.message : '采集失败');
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
                fieldQuality={lastCrawl[r.id]?.field_quality ?? lastVerify[r.id]}
                crawlResult={lastCrawl[r.id]}
                onEdit={() => startEdit(r)}
                onDelete={() => remove(r.id, r.company_name)}
                onVerify={() => verify(r)}
                onExplore={() => void crawlRecipe(r, 'explore')}
                onCrawl={() => void crawlRecipe(r, 'crawl')}
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

function formatCrawlMessage(company: string, res: SiteCrawlResult): string {
  if (res.ingest_status === 'not_started') {
    const samples = res.verify_samples?.length ? `：${res.verify_samples.join(' / ')}` : '';
    return `「${company}」路径探索通过，验证采到 ${res.verified_jobs ?? res.count} 条${samples}`;
  }
  const parts = [`「${company}」采集完成：发现 ${res.count}`];
  if (res.ingested !== undefined) parts.push(`新增 ${res.ingested}`);
  if (res.duplicate !== undefined) parts.push(`重复 ${res.duplicate}`);
  if (res.skipped !== undefined && res.skipped > 0) parts.push(`跳过 ${res.skipped}`);
  return parts.join('，');
}

/** 单个站点配置卡片。 */
function RecipeCard({
  recipe,
  busy,
  fieldQuality,
  crawlResult,
  onEdit,
  onDelete,
  onVerify,
  onExplore,
  onCrawl,
}: {
  recipe: SiteRecipe;
  busy: boolean;
  fieldQuality?: SiteRecipeFieldQuality[];
  crawlResult?: SiteCrawlResult;
  onEdit: () => void;
  onDelete: () => void;
  onVerify: () => void;
  onExplore: () => void;
  onCrawl: () => void;
}) {
  const [showRuns, setShowRuns] = useState(false);
  const verify = VERIFY_STYLE[recipe.verify_status] ?? VERIFY_STYLE.unverified;

  return (
    <div
      className={`rounded-card border bg-white/70 p-4 shadow-card ${
        recipe.enabled ? 'border-lilac-100' : 'border-ink-100 opacity-60'
      }`}
    >
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <h3 className="text-base font-semibold text-ink-800">{recipe.company_name}</h3>
            <span className="rounded-pill bg-card-lilac px-2 py-0.5 text-[10px] font-semibold text-lilac-700">
              {STRATEGY_LABEL[recipe.strategy_type] ?? recipe.strategy_type}
            </span>
            <span className={`rounded-pill px-2 py-0.5 text-[10px] font-semibold ${verify.className}`}>
              {verify.label}
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

          {/* 接口策略：把「实际怎么发这个请求」摊开给用户看。
              这些值由 Agent 从真实请求沉淀，是排查采集问题的第一现场。 */}
          {(recipe.strategy_type === 'api' || recipe.strategy_type === 'browser_observed') && recipe.list_api && (
            <div className="mt-2 space-y-1 rounded-lg bg-ink-50/70 px-3 py-2">
              <p className="break-all font-mono text-[11px] text-ink-600">
                <span className="font-semibold text-ink-500">{recipe.method}</span> {recipe.list_api}
              </p>
              <p className="font-mono text-[11px] text-ink-500">
                list_path={recipe.list_path || '-'} · title={recipe.title_field || '-'}
                {recipe.keyword_param &&
                  ` · keyword=${recipe.keyword_param}${recipe.keyword_in_body ? '（请求体）' : '（query）'}`}
              </p>
              {recipe.request_body && (
                <p className="break-all font-mono text-[11px] text-ink-500">
                  body[{recipe.request_content_type || 'application/json'}]：{recipe.request_body}
                </p>
              )}
            </div>
          )}

          <div className="mt-2 flex flex-wrap gap-4 text-xs text-ink-500">
            <span>单次最多 {recipe.max_jobs_per_search} 条</span>
            <span>
              详情抓取{' '}
              {recipe.max_detail_fetches === 0
                ? '不抓（用抽取阶段 JD）'
                : `${recipe.max_detail_fetches} 条`}
            </span>
            {recipe.verify_status === 'verified' && recipe.verified_jobs > 0 && (
              <span className="text-emerald-600">验证时采到 {recipe.verified_jobs} 条</span>
            )}
            {recipe.consecutive_failures > 0 && (
              <span className="text-rose-600">连续失败 {recipe.consecutive_failures} 次</span>
            )}
          </div>

          {recipe.last_error && (
            <p className="mt-2 rounded-lg bg-rose-50 px-3 py-2 text-xs leading-relaxed text-rose-700">
              最近失败：{recipe.last_error}
            </p>
          )}

          {recipe.notes && (
            <p className="mt-2 rounded-lg bg-ink-50 px-3 py-2 text-xs leading-relaxed text-ink-600">
              {recipe.notes}
            </p>
          )}
        </div>

        <div className="flex shrink-0 flex-col gap-2">
          <Button variant="secondary" onClick={onCrawl} disabled={busy || !recipe.enabled}>
            采集入库
          </Button>
          <Button variant="ghost" onClick={onExplore} disabled={busy || !recipe.enabled}>
            探索路径
          </Button>
          {/* 仅接口策略可独立验证：浏览器策略依赖登录态与会话，
              无法脱离会话复现，只能通过实际执行一次采集来确认。 */}
          {recipe.strategy_type === 'api' && (
            <Button variant="ghost" onClick={onVerify} disabled={busy}>
              验证
            </Button>
          )}
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

      {crawlResult && <CrawlResultSummary result={crawlResult} />}
      {showRuns && <RunList recipeId={recipe.id} />}
      {fieldQuality && fieldQuality.length > 0 && <FieldQualityList items={fieldQuality} />}
    </div>
  );
}

function CrawlResultSummary({ result }: { result: SiteCrawlResult }) {
  const samples =
    result.verify_samples?.length ? result.verify_samples : result.jobs.map((job) => job.title).filter(Boolean);
  const trace = result.trace ?? [];
  const candidate = result.candidate;
  const fieldMap = candidate?.field_map ? Object.entries(candidate.field_map) : [];

  return (
    <div className="mt-3 border-t border-lilac-50 pt-3 text-xs text-ink-600">
      <div className="flex flex-wrap gap-x-4 gap-y-1">
        <span>{result.strategy}</span>
        <span>发现 {result.count}</span>
        {result.duration_ms !== undefined && <span>耗时 {(result.duration_ms / 1000).toFixed(1)}s</span>}
        {result.refine_rounds !== undefined && <span>修正 {result.refine_rounds} 轮</span>}
        {result.verified !== undefined && (
          <span className={result.verified ? 'text-emerald-600' : 'text-rose-600'}>
            {result.verified ? '已验证' : '未验证'}
          </span>
        )}
        {result.ingest_status === 'not_started' ? (
          <span className="text-amber-700">未入库</span>
        ) : (
          <>
            {result.ingested !== undefined && <span>新增 {result.ingested}</span>}
            {result.duplicate !== undefined && <span>重复 {result.duplicate}</span>}
            {result.skipped !== undefined && result.skipped > 0 && <span>跳过 {result.skipped}</span>}
          </>
        )}
      </div>
      {samples.length > 0 && (
        <p className="mt-1 truncate text-ink-500">样例：{samples.slice(0, 3).join(' / ')}</p>
      )}
      {result.next_action && <p className="mt-1 text-amber-700">{result.next_action}</p>}

      {(candidate || trace.length > 0) && (
        <div className="mt-3 space-y-2">
          {candidate && (
            <details className="border-l-2 border-lilac-100 pl-3">
              <summary className="cursor-pointer text-xs font-semibold text-ink-600">
                候选采集配置
                {candidate.confidence !== undefined && ` · 置信度 ${candidate.confidence}`}
              </summary>
              <div className="mt-2 space-y-1 font-mono text-[11px] text-ink-500">
                <p className="break-all">
                  <span className="font-semibold">{candidate.method || 'GET'}</span>{' '}
                  {candidate.list_api || '-'}
                </p>
                <p className="break-all">
                  list_path={candidate.list_path || '-'} · title={candidate.title_field || '-'}
                  {candidate.keyword_param &&
                    ` · keyword=${candidate.keyword_param}${candidate.keyword_in_body ? '（请求体）' : '（query）'}`}
                </p>
                {fieldMap.length > 0 && (
                  <p className="break-all">
                    field_map：
                    {fieldMap
                      .map(([key, value]) => `${key}=${value}`)
                      .slice(0, 8)
                      .join('；')}
                  </p>
                )}
                {candidate.notes && <p className="whitespace-pre-wrap font-sans text-ink-500">{candidate.notes}</p>}
              </div>
            </details>
          )}

          {trace.length > 0 && (
            <details className="border-l-2 border-lilac-100 pl-3">
              <summary className="cursor-pointer text-xs font-semibold text-ink-600">
                探索轨迹 · {trace.length} 步
              </summary>
              <ol className="mt-2 space-y-2">
                {trace.map((step) => (
                  <li key={`${step.step}-${step.action}-${step.target ?? ''}`}>
                    <div className="flex flex-wrap gap-x-3 gap-y-1 text-[11px] text-ink-500">
                      <span className="font-semibold text-ink-700">#{step.step}</span>
                      <span>{step.action}</span>
                      {step.target && <span className="break-all">target={step.target}</span>}
                      {step.confidence !== undefined && <span>confidence={step.confidence}</span>}
                      {step.new_requests !== undefined && <span>requests={step.new_requests}</span>}
                    </div>
                    {step.result && <p className="mt-0.5 text-[11px] text-ink-600">{step.result}</p>}
                    {step.reasoning && (
                      <p className="mt-0.5 text-[11px] leading-relaxed text-ink-400">{step.reasoning}</p>
                    )}
                  </li>
                ))}
              </ol>
            </details>
          )}
        </div>
      )}
    </div>
  );
}

function FieldQualityList({ items }: { items: SiteRecipeFieldQuality[] }) {
  return (
    <div className="mt-3 border-t border-lilac-50 pt-3">
      <p className="mb-2 text-xs font-semibold text-ink-500">最近验证字段质量</p>
      <div className="flex flex-wrap gap-2">
        {items.map((item) => (
          <span
            key={item.field}
            className={
              item.ok
                ? 'rounded-pill bg-emerald-50 px-2 py-1 text-[11px] text-emerald-700'
                : 'rounded-pill bg-rose-50 px-2 py-1 text-[11px] text-rose-700'
            }
            title={item.samples?.join(' / ') || '未命中样例'}
          >
            {FIELD_LABELS[item.field] ?? item.field} {item.hit}/{item.total}
          </span>
        ))}
      </div>
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
              <option value="browser">浏览器抽取（复用站点适配器）</option>
              <option value="api">接口直调（GET / POST 均支持）</option>
              <option value="browser_observed">浏览器页面观测（触发页面请求）</option>
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

          {(draft.strategy_type === 'api' || draft.strategy_type === 'browser_observed') && (
            <>
              <Field label="列表 API">
                <input
                  className="input"
                  value={draft.list_api ?? ''}
                  onChange={(e) => set('list_api', e.target.value)}
                  placeholder="https://example.com/api/jobs?keyword=后端"
                />
              </Field>

              <div className="grid grid-cols-2 gap-3">
                <Field label="方法">
                  <select
                    className="input"
                    value={draft.method ?? 'GET'}
                    onChange={(e) => set('method', e.target.value as 'GET' | 'POST')}
                  >
                    <option value="GET">GET</option>
                    <option value="POST">POST</option>
                  </select>
                </Field>
                <Field label="关键词参数">
                  <input
                    className="input"
                    value={draft.keyword_param ?? ''}
                    onChange={(e) => set('keyword_param', e.target.value)}
                    placeholder="keyword"
                  />
                </Field>
              </div>

              <label className="flex items-center gap-2 text-sm text-ink-700">
                <input
                  type="checkbox"
                  checked={draft.keyword_in_body ?? false}
                  onChange={(e) => set('keyword_in_body', e.target.checked)}
                />
                关键词放在请求体里（而非 URL query）
              </label>

              {/* POST 请求侧配置：这是让新站点「不用改代码就能接」的关键。
                  值通常由探索 Agent 自动填好，人工只在需要微调时才动。 */}
              {draft.method === 'POST' && (
                <>
                  <Field label="请求体类型">
                    <select
                      className="input"
                      value={draft.request_content_type ?? ''}
                      onChange={(e) => set('request_content_type', e.target.value)}
                    >
                      <option value="">application/json（默认）</option>
                      <option value="application/json">application/json</option>
                      <option value="application/x-www-form-urlencoded">
                        application/x-www-form-urlencoded
                      </option>
                    </select>
                  </Field>

                  <Field label="请求体（JSON；可用 {keyword} 占位关键词）">
                    <textarea
                      className="input min-h-[70px] font-mono text-xs"
                      value={draft.request_body ?? ''}
                      onChange={(e) => set('request_body', e.target.value)}
                      placeholder='{"pageSize":20,"keyword":"{keyword}"}'
                    />
                  </Field>
                </>
              )}

              <div className="grid grid-cols-2 gap-3">
                <Field label="列表路径">
                  <input
                    className="input"
                    value={draft.list_path ?? ''}
                    onChange={(e) => set('list_path', e.target.value)}
                    placeholder="data.positionList"
                  />
                </Field>
                <Field label="标题字段">
                  <input
                    className="input"
                    value={draft.title_field ?? ''}
                    onChange={(e) => set('title_field', e.target.value)}
                    placeholder="positionTitle"
                  />
                </Field>
              </div>

              <div className="grid grid-cols-2 gap-3">
                <Field label="ID 字段">
                  <input
                    className="input"
                    value={draft.id_field ?? ''}
                    onChange={(e) => set('id_field', e.target.value)}
                    placeholder="postId"
                  />
                </Field>
                <Field label="详情页模板">
                  <input
                    className="input"
                    value={draft.detail_url_template ?? ''}
                    onChange={(e) => set('detail_url_template', e.target.value)}
                    placeholder="https://example.com/job/{id}"
                  />
                </Field>
              </div>

              <Field label="详情 API 模板">
                <input
                  className="input"
                  value={draft.detail_api ?? ''}
                  onChange={(e) => set('detail_api', e.target.value)}
                  placeholder="https://example.com/api/job/{id}"
                />
              </Field>
            </>
          )}

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
