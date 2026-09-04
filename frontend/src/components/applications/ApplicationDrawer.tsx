'use client';

import useSWR from 'swr';
import { useState } from 'react';
import { api, fetcher, ApiError } from '@/lib/api';
import type { ApplicationDetail, StatusOption } from '@/lib/types';
import { APP_STATUS_LABELS, firstDisplayValue, formatRelative, orDash } from '@/lib/utils';
import { Drawer } from '@/components/ui/Drawer';
import { Button } from '@/components/ui/Button';
import { Badge, appStatusToneOf } from '@/components/ui/Badge';
import { ErrorState, Skeleton } from '@/components/ui/States';

interface ApplicationDrawerProps {
  applicationId: string | null;
  onClose: () => void;
  onChanged: () => void;
}

/**
 * 投递任务详情抽屉。
 *
 * 重要：此处**没有**「代替我提交」的按钮。
 * 只有「我已在网站完成提交」这个由用户确认的动作。
 */
export function ApplicationDrawer({ applicationId, onClose, onChanged }: ApplicationDrawerProps) {
  const { data, error, isLoading, mutate } = useSWR<ApplicationDetail>(
    applicationId ? `/applications/${applicationId}` : null,
    fetcher,
    { refreshInterval: 5000 },
  );
  const { data: statusOptions } = useSWR<StatusOption[]>('/applications/status-options', fetcher);

  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);

  async function run(fn: () => Promise<string>) {
    setBusy(true);
    setMsg(null);
    try {
      setMsg(await fn());
      await mutate();
      onChanged();
    } catch (err) {
      if (err instanceof ApiError) setMsg(err.message);
      else setMsg(err instanceof Error ? err.message : '操作失败');
    } finally {
      setBusy(false);
    }
  }

  const app = data?.application;
  const bt = data?.browser_task;
  const summary = data?.field_summary;

  return (
    <Drawer
      open={Boolean(applicationId)}
      onClose={onClose}
      title={data?.job?.title ?? '投递任务'}
      subtitle={
        data?.job
          ? `${data.job.company_name}${firstDisplayValue(data.job.department) ? ` · ${firstDisplayValue(data.job.department)}` : ''}`
          : undefined
      }
      footer={
        app ? (
          <div className="space-y-2.5">
            <p className="text-[11px] leading-relaxed text-ink-400">
              系统只负责辅助填写。最终提交按钮必须由你在招聘网站上亲自点击，
              确认无误后再回到这里标记为已投递。
            </p>
            <div className="flex flex-wrap items-center justify-end gap-2">
              {app.application_url ? (
                <a
                  href={app.application_url}
                  target="_blank"
                  rel="noopener noreferrer nofollow"
                  className="inline-flex h-10 items-center rounded-pill border border-lilac-200 bg-white px-4 text-sm font-medium text-lilac-700 transition hover:bg-lilac-50"
                >
                  打开申请页面
                </a>
              ) : null}

              {!isSubmittedOrBeyond(app.status) ? (
                <Button
                  variant="secondary"
                  loading={busy}
                  onClick={() =>
                    void run(async () => {
                      const res = await api.post<{ message: string; needs_login: boolean }>(
                        `/applications/${app.id}/browser/start`,
                      );
                      return res.message;
                    })
                  }
                >
                  {bt ? '继续辅助填写' : '开始辅助填写'}
                </Button>
              ) : null}

              {!isSubmittedOrBeyond(app.status) ? (
                <Button
                  loading={busy}
                  onClick={() =>
                    void run(async () => {
                      await api.post(`/applications/${app.id}/mark-submitted`);
                      return '已标记为已投递';
                    })
                  }
                >
                  我已在网站完成提交
                </Button>
              ) : null}
            </div>
          </div>
        ) : null
      }
    >
      {error ? (
        <ErrorState message="加载投递任务失败" onRetry={() => void mutate()} />
      ) : isLoading || !data || !app ? (
        <div className="space-y-4">
          <Skeleton className="h-24 rounded-card" />
          <Skeleton className="h-40 rounded-card" />
        </div>
      ) : (
        <div className="space-y-5">
          {msg ? (
            <div className="rounded-card border border-lilac-200 bg-lilac-50 px-4 py-3 text-sm text-lilac-800">
              {msg}
            </div>
          ) : null}

          {/* 当前状态与进度 */}
          <section className="rounded-card border border-ink-100 bg-white/70 px-4 py-4">
            <div className="flex items-center justify-between">
              <Badge tone={appStatusToneOf(app.status)}>
                {APP_STATUS_LABELS[app.status] ?? app.status}
              </Badge>
              <span className="text-xs text-ink-400">
                最近操作 {formatRelative(app.last_action_at ?? app.updated_at)}
              </span>
            </div>
            <div className="mt-3 h-1.5 overflow-hidden rounded-pill bg-ink-100">
              <div
                className="h-full rounded-pill bg-gradient-to-r from-lilac-400 to-blossom-300 transition-all"
                style={{ width: `${app.progress}%` }}
              />
            </div>
          </section>

          {/* 需要人工处理的字段：安全关键提示 */}
          {bt && bt.status === 'WAITING_USER' && summary && summary.skipped > 0 ? (
            <section className="rounded-card border border-cream-300 bg-cream-100 px-4 py-4">
              <p className="text-sm font-medium text-cream-700">
                已自动填写 {summary.filled} 项，有 {summary.skipped} 项需要你手动填写
              </p>
              {summary.sensitive > 0 ? (
                <p className="mt-1 text-xs text-cream-700">
                  其中 {summary.sensitive} 项属于敏感信息（如身份证、验证码），
                  系统按设计始终留空，必须由你亲自填写。
                </p>
              ) : null}

              {summary.pending_labels.length > 0 ? (
                <ul className="mt-2.5 flex flex-wrap gap-1.5">
                  {summary.pending_labels.map((label, i) => (
                    <li key={i}>
                      <Badge tone="cream">{label}</Badge>
                    </li>
                  ))}
                </ul>
              ) : null}

              <div className="mt-3 flex gap-2">
                <Button
                  size="sm"
                  loading={busy}
                  onClick={() =>
                    void run(async () => {
                      await api.post(`/browser-tasks/${bt.id}/resume`);
                      return '已继续处理';
                    })
                  }
                >
                  我已填好，继续
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  loading={busy}
                  onClick={() =>
                    void run(async () => {
                      await api.post(`/browser-tasks/${bt.id}/pause`);
                      return '已暂停';
                    })
                  }
                >
                  暂停
                </Button>
              </div>
            </section>
          ) : null}

          {/* 被网站阻止 */}
          {bt && bt.status === 'BLOCKED' ? (
            <section className="rounded-card border border-blossom-200 bg-blossom-50 px-4 py-4">
              <p className="text-sm font-medium text-blossom-800">页面限制了自动化操作</p>
              <p className="mt-1 text-xs text-blossom-700">
                系统已停止自动填写，不会尝试绕过验证。请在浏览器中手动完成申请。
              </p>
            </section>
          ) : null}

          {/* 表单完成度 */}
          {summary && summary.total > 0 ? (
            <section>
              <h3 className="mb-2.5 text-sm font-semibold text-ink-800">表单完成度</h3>
              <div className="rounded-card border border-ink-100 bg-white/70 px-4 py-3.5">
                <div className="flex gap-6 text-sm">
                  <Stat label="识别字段" value={summary.total} />
                  <Stat label="已自动填写" value={summary.filled} tone="text-mint-700" />
                  <Stat label="待人工处理" value={summary.skipped} tone="text-cream-700" />
                  <Stat label="敏感字段" value={summary.sensitive} tone="text-blossom-700" />
                </div>

                {data.fields.length > 0 ? (
                  <ul className="mt-3 max-h-52 space-y-1.5 overflow-y-auto border-t border-ink-100 pt-3">
                    {data.fields.map((f) => (
                      <li key={f.id} className="flex items-center justify-between gap-2 text-xs">
                        <span className="truncate text-ink-700">
                          {orDash(f.field_label || f.field_name)}
                          {f.is_required ? <span className="text-blossom-500"> *</span> : null}
                        </span>
                        {f.is_filled ? (
                          <Badge tone="mint">已填写</Badge>
                        ) : f.is_sensitive ? (
                          <Badge tone="blossom">敏感 · 需你填写</Badge>
                        ) : (
                          <Badge tone="neutral">需你填写</Badge>
                        )}
                      </li>
                    ))}
                  </ul>
                ) : null}
              </div>
            </section>
          ) : null}

          {/* 状态流转 */}
          {statusOptions && data.allowed_next.length > 0 ? (
            <section>
              <h3 className="mb-2.5 text-sm font-semibold text-ink-800">更新状态</h3>
              <div className="flex flex-wrap gap-1.5">
                {data.allowed_next
                  .filter((s) => s !== 'FORM_ANALYZING' && s !== 'FORM_FILLING')
                  .map((s) => (
                    <Button
                      key={s}
                      size="sm"
                      variant="secondary"
                      loading={busy}
                      onClick={() =>
                        void run(async () => {
                          await api.put(`/applications/${app.id}/status`, { status: s });
                          return `已更新为「${APP_STATUS_LABELS[s] ?? s}」`;
                        })
                      }
                    >
                      {APP_STATUS_LABELS[s] ?? s}
                    </Button>
                  ))}
              </div>
            </section>
          ) : null}

          {/* 事件流 */}
          {data.events.length > 0 ? (
            <section>
              <h3 className="mb-2.5 text-sm font-semibold text-ink-800">操作记录</h3>
              <ol className="space-y-2.5 rounded-card border border-ink-100 bg-white/70 px-4 py-3.5">
                {data.events.map((e) => (
                  <li key={e.id} className="flex gap-3">
                    <span className="mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full bg-lilac-300" />
                    <div className="min-w-0">
                      <p className="text-xs text-ink-700">{e.description}</p>
                      <p className="text-[11px] text-ink-400">{formatRelative(e.created_at)}</p>
                    </div>
                  </li>
                ))}
              </ol>
            </section>
          ) : null}
        </div>
      )}
    </Drawer>
  );
}

function Stat({ label, value, tone }: { label: string; value: number; tone?: string }) {
  return (
    <div>
      <p className="text-[11px] text-ink-400">{label}</p>
      <p className={`mt-0.5 text-lg font-semibold tabular-nums ${tone ?? 'text-ink-800'}`}>
        {value}
      </p>
    </div>
  );
}

const SUBMITTED_OR_BEYOND = new Set([
  'SUBMITTED',
  'WRITTEN_TEST',
  'INTERVIEW_1',
  'INTERVIEW_2',
  'HR_INTERVIEW',
  'OFFER',
  'REJECTED',
  'WITHDRAWN',
]);

function isSubmittedOrBeyond(status: string): boolean {
  return SUBMITTED_OR_BEYOND.has(status);
}
