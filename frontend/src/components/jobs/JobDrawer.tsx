'use client';

import { useState } from 'react';
import useSWR from 'swr';
import { Drawer } from '@/components/ui/Drawer';
import { Button } from '@/components/ui/Button';
import { Badge } from '@/components/ui/Badge';
import { MatchBadge } from './MatchBadge';
import { ErrorState, Skeleton } from '@/components/ui/States';
import { fetcher, jobsApi } from '@/lib/api';
import {
  cleanDisplayList,
  cleanDisplayText,
  firstDisplayValue,
  formatDate,
  orDash,
  SOURCE_LABELS,
} from '@/lib/utils';
import type { JobDetail } from '@/lib/types';

interface JobDrawerProps {
  jobId: string | null;
  onClose: () => void;
  onAddToCart: (jobId: string) => Promise<void>;
  busy?: boolean;
}

/** 岗位详情抽屉。完整 JD 在此展示，不跳转新页面。 */
export function JobDrawer({ jobId, onClose, onAddToCart, busy }: JobDrawerProps) {
  const { data, error, isLoading, mutate } = useSWR<JobDetail>(
    jobId ? `/jobs/${jobId}` : null,
    fetcher,
  );

  const [scraping, setScraping] = useState(false);
  const [loginTaskId, setLoginTaskId] = useState<string | null>(null);
  const [scrapeMsg, setScrapeMsg] = useState<string | null>(null);

  const analysis = data?.match_analysis ?? {};
  const analysisSummary = cleanDisplayText(analysis.summary);
  const analysisReasons = cleanDisplayList(analysis.reasons);
  const analysisRisks = cleanDisplayList(analysis.risks);

  // 用已登录浏览器抓取真实 JD。若需登录则保留 task_id 等待用户登录后继续。
  async function handleScrape() {
    if (!data) return;
    setScraping(true);
    setScrapeMsg(null);
    try {
      const res = await jobsApi.scrape(data.id);
      if (res.needs_login) {
        setLoginTaskId(res.task_id);
        setScrapeMsg(res.message || '页面需要登录后才能查看，请在浏览器中完成登录。');
      } else {
        setLoginTaskId(null);
        setScrapeMsg('已用本机浏览器抓取到真实 JD，详情已更新。');
        await mutate();
      }
    } catch (e) {
      setScrapeMsg(e instanceof Error ? e.message : '抓取失败');
    } finally {
      setScraping(false);
    }
  }

  async function handleResume() {
    if (!data || !loginTaskId) return;
    setScraping(true);
    setScrapeMsg(null);
    try {
      const res = await jobsApi.scrapeResume(data.id, loginTaskId);
      if (res.needs_login) {
        setScrapeMsg(res.message || '仍未登录，请在浏览器中完成登录后重试。');
      } else {
        setLoginTaskId(null);
        setScrapeMsg('已用本机浏览器抓取到真实 JD，详情已更新。');
        await mutate();
      }
    } catch (e) {
      setScrapeMsg(e instanceof Error ? e.message : '继续抓取失败');
    } finally {
      setScraping(false);
    }
  }

  return (
    <Drawer
      open={Boolean(jobId)}
      onClose={onClose}
      title={data?.title ?? '岗位详情'}
      subtitle={
        data
          ? `${data.company_name}${firstDisplayValue(data.department) ? ` · ${firstDisplayValue(data.department)}` : ''}`
          : undefined
      }
      footer={
        data ? (
          <div className="flex items-center justify-between gap-3">
            <p className="text-xs text-ink-400">
              {data.in_cart ? '已在岗位车中' : '确认后可加入岗位车'}
            </p>
            <div className="flex gap-2">
              {data.source_url ? (
                <a
                  href={data.source_url}
                  target="_blank"
                  rel="noopener noreferrer nofollow"
                  className="inline-flex h-10 items-center rounded-pill border border-lilac-200 bg-white px-4 text-sm font-medium text-lilac-700 transition hover:bg-lilac-50"
                >
                  查看原网页
                </a>
              ) : null}
              {data.source_url && (data.desc_quality === 'empty' || data.desc_quality === 'snippet') ? (
                <Button
                  variant="primary"
                  loading={scraping}
                  disabled={busy}
                  onClick={() => void handleScrape()}
                >
                  🌐 用本机浏览器抓取真实 JD
                </Button>
              ) : data.source_url ? (
                <Button
                  variant="secondary"
                  loading={scraping}
                  disabled={busy}
                  onClick={() => void handleScrape()}
                >
                  用本机浏览器抓取真实 JD
                </Button>
              ) : null}
              <Button
                loading={busy}
                disabled={data.in_cart}
                onClick={async () => {
                  await onAddToCart(data.id);
                  await mutate();
                }}
              >
                {data.in_cart ? '已加入岗位车' : '加入岗位车'}
              </Button>
            </div>
          </div>
        ) : null
      }
    >
      {error ? (
        <ErrorState message="加载岗位详情失败" onRetry={() => void mutate()} />
      ) : isLoading || !data ? (
        <div className="space-y-4">
          <Skeleton className="h-32 rounded-card" />
          <Skeleton className="h-40 rounded-card" />
          <Skeleton className="h-64 rounded-card" />
        </div>
      ) : (
        <div className="space-y-6">
          {/* 浏览器抓取状态提示 */}
          {(loginTaskId || scrapeMsg) && (
            <div className="rounded-card border border-lilac-200 bg-lilac-50 px-4 py-3 text-sm text-lilac-800">
              <p className="flex items-start gap-2">
                <span>{loginTaskId ? '🔐' : '✨'}</span>
                <span>{scrapeMsg ?? '正在抓取…'}</span>
              </p>
              {loginTaskId ? (
                <Button
                  variant="secondary"
                  size="sm"
                  loading={scraping}
                  className="mt-2"
                  onClick={() => void handleResume()}
                >
                  我已在浏览器中登录，继续抓取
                </Button>
              ) : null}
            </div>
          )}

          {/* 基础信息 */}
          <Section title="基础信息">
            <dl className="grid grid-cols-2 gap-x-6 gap-y-3">
              <Field label="公司" value={orDash(data.company_name)} />
              <Field label="部门 / 业务" value={orDash(firstDisplayValue(data.department, data.business))} />
              <Field label="岗位" value={orDash(data.title)} />
              <Field
                label="工作地点"
                value={data.locations.length > 0 ? data.locations.join('、') : orDash(data.location)}
              />
              <Field
                label="招聘届次"
                value={data.graduation_year ? `${data.graduation_year} 届` : '——'}
              />
              <Field label="岗位类型" value={orDash(data.job_type)} />
              <Field label="来源" value={SOURCE_LABELS[data.source_type] ?? data.source_type} />
              <Field label="发布时间" value={formatDate(data.published_at)} />
              <Field label="截止时间" value={formatDate(data.deadline)} />
              <Field label="发现时间" value={formatDate(data.crawled_at)} />
            </dl>

            {data.sources.length > 1 ? (
              <div className="mt-3 flex flex-wrap items-center gap-1.5">
                <span className="text-xs text-ink-400">该岗位出现于：</span>
                {data.sources.map((s) => (
                  <Badge key={s.id} tone="sky">
                    {SOURCE_LABELS[s.source_type] ?? s.source_name}
                  </Badge>
                ))}
              </div>
            ) : null}
          </Section>

          {/* AI 匹配分析 */}
          <Section
            title="AI 匹配分析"
            extra={<MatchBadge score={data.match_score} />}
          >
            {analysisSummary ? (
              <p className="mb-3 text-sm text-ink-700">{analysisSummary}</p>
            ) : null}

            {analysisReasons.length > 0 ? (
              <ul className="space-y-1.5">
                {analysisReasons.map((r, i) => (
                  <li key={i} className="flex gap-2 text-sm text-ink-700">
                    <span className="mt-0.5 text-mint-500">✓</span>
                    <span>{r}</span>
                  </li>
                ))}
              </ul>
            ) : (
              <p className="text-sm text-ink-400">暂无匹配分析</p>
            )}

            {analysisRisks.length > 0 ? (
              <>
                <p className="mb-1.5 mt-4 text-xs font-medium text-ink-500">需要注意</p>
                <ul className="space-y-1.5">
                  {analysisRisks.map((r, i) => (
                    <li key={i} className="flex gap-2 text-sm text-ink-700">
                      <span className="mt-0.5 text-cream-500">△</span>
                      <span>{r}</span>
                    </li>
                  ))}
                </ul>
              </>
            ) : null}

            {analysis.analyzer ? (
              <p className="mt-3 text-[11px] text-ink-400">
                分析方式：{analysis.analyzer === 'rule+llm' ? '规则 + AI 语义' : '规则评分'}
                {typeof analysis.rule_score === 'number' && typeof analysis.llm_score === 'number'
                  ? `（规则 ${analysis.rule_score} / AI ${analysis.llm_score}）`
                  : ''}
              </p>
            ) : null}
          </Section>

          {/* 技术栈 */}
          {data.technical_stack.length > 0 || data.language_requirements.length > 0 ? (
            <Section title="技术要求">
              <div className="flex flex-wrap gap-1.5">
                {data.language_requirements.map((l) => (
                  <Badge key={`lang-${l}`} tone="lilac">
                    {l}
                  </Badge>
                ))}
                {data.technical_stack.map((t) => (
                  <Badge key={`tech-${t}`} tone="sky">
                    {t}
                  </Badge>
                ))}
              </div>
            </Section>
          ) : null}

          {/* 岗位职责 */}
          {data.responsibilities.length > 0 ? (
            <Section title="岗位职责">
              <ul className="list-inside list-disc space-y-1.5 text-sm leading-relaxed text-ink-700">
                {data.responsibilities.map((r, i) => (
                  <li key={i}>{r}</li>
                ))}
              </ul>
            </Section>
          ) : null}

          {/* 任职要求 */}
          {data.requirements.length > 0 ? (
            <Section title="任职要求">
              <ul className="list-inside list-disc space-y-1.5 text-sm leading-relaxed text-ink-700">
                {data.requirements.map((r, i) => (
                  <li key={i}>{r}</li>
                ))}
              </ul>
            </Section>
          ) : null}

          {/* 原始 JD：保留原文，不只展示 AI 总结 */}
          <Section
            title="岗位描述原文"
            extra={
              data.desc_quality === 'empty' ? (
                <span className="text-[11px] font-medium text-coral-500">数据缺失</span>
              ) : data.desc_quality === 'snippet' ? (
                <span className="text-[11px] font-medium text-cream-600">摘要</span>
              ) : null
            }
          >
            {data.desc_quality === 'empty' || !data.description ? (
              <div className="space-y-3 py-1">
                <div className="flex items-start gap-2 rounded bg-coral-50 px-3 py-2.5 text-sm text-coral-700">
                  <span className="mt-0.5">⚠️</span>
                  <div>
                    <p className="font-medium">该岗位的详细描述未能获取</p>
                    <p className="mt-0.5 text-xs text-coral-600">
                      搜索引擎只返回了网页标题，未抓取到完整岗位内容（可能需要登录或 JS 渲染）。
                    </p>
                  </div>
                </div>
                {data.source_url && (
                  <p className="text-center text-xs text-ink-400">
                    可使用下方「用本机浏览器抓取真实 JD」按钮获取完整信息
                  </p>
                )}
              </div>
            ) : data.desc_quality === 'snippet' ? (
              <div className="space-y-2">
                {/* 使用 whitespace-pre-wrap 保留换行；React 自动转义，无 XSS 风险 */}
                <p className="whitespace-pre-wrap break-words text-sm leading-relaxed text-ink-600">
                  {data.description}
                </p>
                <p className="text-center text-xs text-cream-600">
                  以上为搜索摘要，可能不完整。可用浏览器抓取获取完整 JD。
                </p>
              </div>
            ) : (
              /* full quality: 正常展示 */
              <p className="whitespace-pre-wrap break-words text-sm leading-relaxed text-ink-600">
                {data.description}
              </p>
            )}
          </Section>
        </div>
      )}
    </Drawer>
  );
}

function Section({
  title,
  extra,
  children,
}: {
  title: string;
  extra?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <section>
      <div className="mb-2.5 flex items-center justify-between">
        <h3 className="text-sm font-semibold text-ink-800">{title}</h3>
        {extra}
      </div>
      <div className="rounded-card border border-ink-100 bg-white/70 px-4 py-3.5">{children}</div>
    </section>
  );
}

function Field({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0">
      <dt className="text-[11px] text-ink-400">{label}</dt>
      <dd className="mt-0.5 truncate text-sm text-ink-800">{value}</dd>
    </div>
  );
}
