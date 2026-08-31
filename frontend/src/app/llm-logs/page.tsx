'use client';

import { useState } from 'react';
import useSWR from 'swr';
import { fetcher } from '@/lib/api';
import { PageHeader } from '@/components/layout/PageHeader';
import { Skeleton, ErrorState } from '@/components/ui/States';
import { Button } from '@/components/ui/Button';

interface LLMCallLog {
  timestamp: string;
  model: string;
  kind: string;
  system_prompt: string;
  user_prompt: string;
  response: string;
  duration_ms: number;
  attempts: number;
  error: string;
}

interface LLMLogsData {
  logs: LLMCallLog[];
  count: number;
}

export default function LLMLogsPage() {
  const { data, error, isLoading, mutate } = useSWR<LLMLogsData>('/llm-logs', fetcher, {
    refreshInterval: 5000,
  });
  const [expanded, setExpanded] = useState<number | null>(null);

  return (
    <>
      <PageHeader
        title="LLM 调用日志"
        subtitle="最近 200 条大模型调用的完整 Request / Response（每 5 秒刷新）"
        actions={
          <Button variant="ghost" onClick={() => mutate()}>
            刷新
          </Button>
        }
      />

      <div className="flex-1 overflow-auto px-8 py-6">
        {isLoading ? (
          <Skeleton />
        ) : error ? (
          <ErrorState message="加载失败" onRetry={() => mutate()} />
        ) : !data || data.count === 0 ? (
          <div className="rounded-card border border-lilac-100 bg-white/60 p-8 text-center text-sm text-ink-400">
            暂无 LLM 调用记录。触发一次搜索或爬虫后这里会出现调用日志。
          </div>
        ) : (
          <div className="space-y-2">
            {data.logs.map((log, i) => (
              <LLMLogEntry
                key={i}
                log={log}
                index={i}
                expanded={expanded === i}
                onToggle={() => setExpanded(expanded === i ? null : i)}
              />
            ))}
          </div>
        )}
      </div>
    </>
  );
}

function LLMLogEntry({
  log,
  index,
  expanded,
  onToggle,
}: {
  log: LLMCallLog;
  index: number;
  expanded: boolean;
  onToggle: () => void;
}) {
  const hasError = log.error !== '';
  const time = new Date(log.timestamp).toLocaleTimeString('zh-CN', { hour12: false });

  return (
    <div
      className={`rounded-card border bg-white/70 shadow-card transition ${
        hasError ? 'border-rose-200' : 'border-lilac-100'
      }`}
    >
      {/* 摘要行 */}
      <button
        onClick={onToggle}
        className="flex w-full items-center gap-3 px-4 py-3 text-left"
      >
        <span className="shrink-0 text-xs font-mono text-ink-400">{time}</span>
        <span
          className={`shrink-0 rounded-pill px-2 py-0.5 text-[10px] font-semibold ${
            hasError
              ? 'bg-rose-100 text-rose-700'
              : 'bg-card-lilac text-lilac-700'
          }`}
        >
          {log.kind}
        </span>
        <span className="min-w-0 flex-1 truncate text-sm text-ink-700">
          {log.user_prompt.slice(0, 80) || '(空)'}
        </span>
        <span className="shrink-0 text-xs text-ink-400">
          {log.duration_ms}ms · {log.attempts}次
        </span>
        <svg
          className={`h-4 w-4 shrink-0 text-ink-300 transition-transform ${
            expanded ? 'rotate-180' : ''
          }`}
          viewBox="0 0 20 20"
          fill="none"
        >
          <path d="M5 8l5 5 5-5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
        </svg>
      </button>

      {/* 展开详情 */}
      {expanded && (
        <div className="space-y-3 border-t border-lilac-50 px-4 py-3">
          {hasError && (
            <div className="rounded-lg bg-rose-50 px-3 py-2 text-xs text-rose-700">
              错误：{log.error}
            </div>
          )}

          <PromptBlock label="System Prompt" content={log.system_prompt} />
          <PromptBlock label="User Prompt (Request)" content={log.user_prompt} />
          <PromptBlock label="Response" content={log.response} />
        </div>
      )}
    </div>
  );
}

function PromptBlock({ label, content }: { label: string; content: string }) {
  return (
    <div>
      <p className="mb-1 text-[11px] font-medium text-ink-400">{label}</p>
      <pre className="max-h-96 overflow-auto rounded-lg bg-ink-900/95 p-3 text-xs leading-relaxed text-emerald-50">
        {content || '(空)'}
      </pre>
    </div>
  );
}
