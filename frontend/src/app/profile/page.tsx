'use client';

import { useEffect, useState } from 'react';
import useSWR from 'swr';
import { api, fetcher } from '@/lib/api';
import type { JobProfile, Resume } from '@/lib/types';
import { formatRelative } from '@/lib/utils';
import { PageHeader } from '@/components/layout/PageHeader';
import { Button } from '@/components/ui/Button';
import { Badge } from '@/components/ui/Badge';
import { ErrorState, Skeleton } from '@/components/ui/States';
import { cn } from '@/lib/utils';

const ROLE_PRESETS = ['后端', 'AI后端', 'AI全栈', 'Agent', 'Infra', '算法'];
const LANGUAGE_PRESETS = ['Go', 'Python', 'Java', 'C++', 'Rust', 'TypeScript'];
const CITY_PRESETS = ['深圳', '东莞', '广州', '北京', '上海', '杭州', '成都', '南京'];
const COMPANY_PRESETS = ['大厂', '外企', 'AI公司', '独角兽', '国企'];

export default function ProfilePage() {
  const { data, error, isLoading, mutate } = useSWR<JobProfile>('/profile', fetcher);
  const { data: resumes, mutate: mutateResumes } = useSWR<{ items: Resume[] }>(
    '/resumes',
    fetcher,
  );

  const [draft, setDraft] = useState<JobProfile | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);

  useEffect(() => {
    if (data) setDraft(data);
  }, [data]);

  async function save() {
    if (!draft) return;
    setBusy(true);
    setMsg(null);
    try {
      await api.put('/profile', {
        target_roles: draft.target_roles,
        preferred_languages: draft.preferred_languages,
        preferred_locations: draft.preferred_locations,
        company_preferences: draft.company_preferences,
        target_industries: draft.target_industries,
        graduation_year: draft.graduation_year,
      });
      setMsg('求职画像已保存，下次获取岗位时会按新条件搜索');
      await mutate();
    } catch (err) {
      setMsg(err instanceof Error ? err.message : '保存失败');
    } finally {
      setBusy(false);
    }
  }

  function patch(partial: Partial<JobProfile>) {
    setDraft((prev) => (prev ? { ...prev, ...partial } : prev));
  }

  function toggleTag(key: keyof JobProfile, tag: string) {
    if (!draft) return;
    const list = draft[key] as string[];
    const next = list.includes(tag) ? list.filter((x) => x !== tag) : [...list, tag];
    patch({ [key]: next } as Partial<JobProfile>);
  }

  /** 调整数组中某项的顺序（顺序即优先级）。 */
  function move(key: keyof JobProfile, index: number, delta: number) {
    if (!draft) return;
    const list = [...(draft[key] as string[])];
    const target = index + delta;
    if (target < 0 || target >= list.length) return;
    const a = list[index];
    const b = list[target];
    if (a === undefined || b === undefined) return;
    list[index] = b;
    list[target] = a;
    patch({ [key]: list } as Partial<JobProfile>);
  }

  return (
    <>
      <PageHeader
        title="求职画像"
        subtitle="这些条件决定了系统帮你搜什么岗位、怎么算匹配度。"
        actions={
          <Button loading={busy} onClick={() => void save()}>
            保存画像
          </Button>
        }
      />

      <div className="flex-1 overflow-y-auto px-8 py-6">
        {error ? (
          <ErrorState message="加载求职画像失败" onRetry={() => void mutate()} />
        ) : isLoading || !draft ? (
          <div className="space-y-4">
            {Array.from({ length: 4 }).map((_, i) => (
              <Skeleton key={i} className="h-40 rounded-card" />
            ))}
          </div>
        ) : (
          <div className="space-y-5">
            {msg ? (
              <div className="rounded-card border border-lilac-200 bg-lilac-50 px-4 py-3 text-sm text-lilac-800">
                {msg}
              </div>
            ) : null}

            {/* 毕业届次 */}
            <Card title="毕业届次" hint="用于过滤非本届的招聘信息">
              <input
                type="number"
                className="input max-w-32"
                min={2000}
                max={2100}
                value={draft.graduation_year}
                onChange={(e) => patch({ graduation_year: Number(e.target.value) })}
              />
            </Card>

            {/* 求职方向 */}
            <Card title="求职方向" hint="用于生成搜索关键词与判断岗位方向是否匹配">
              <TagPicker
                presets={ROLE_PRESETS}
                selected={draft.target_roles}
                onToggle={(t) => toggleTag('target_roles', t)}
                onAdd={(t) =>
                  patch({ target_roles: [...draft.target_roles, t] })
                }
              />
            </Card>

            {/* 技术语言：顺序即优先级 */}
            <Card
              title="技术语言"
              hint="列表顺序即优先级。第一位是你的主力语言，匹配度权重最高。"
            >
              <OrderedList
                items={draft.preferred_languages}
                onMove={(i, d) => move('preferred_languages', i, d)}
                onRemove={(i) =>
                  patch({
                    preferred_languages: draft.preferred_languages.filter((_, x) => x !== i),
                  })
                }
              />
              <div className="mt-3">
                <TagPicker
                  presets={LANGUAGE_PRESETS}
                  selected={draft.preferred_languages}
                  onToggle={(t) => toggleTag('preferred_languages', t)}
                  onAdd={(t) =>
                    patch({ preferred_languages: [...draft.preferred_languages, t] })
                  }
                />
              </div>
            </Card>

            {/* 城市：顺序即优先级 */}
            <Card
              title="城市优先级"
              hint="列表顺序即优先级。越靠前的城市，岗位匹配度加分越高。"
            >
              <OrderedList
                items={draft.preferred_locations}
                onMove={(i, d) => move('preferred_locations', i, d)}
                onRemove={(i) =>
                  patch({
                    preferred_locations: draft.preferred_locations.filter((_, x) => x !== i),
                  })
                }
              />
              <div className="mt-3">
                <TagPicker
                  presets={CITY_PRESETS}
                  selected={draft.preferred_locations}
                  onToggle={(t) => toggleTag('preferred_locations', t)}
                  onAdd={(t) =>
                    patch({ preferred_locations: [...draft.preferred_locations, t] })
                  }
                />
              </div>
            </Card>

            {/* 公司偏好 */}
            <Card title="公司偏好" hint="不会限制搜索范围，只影响匹配度加分">
              <TagPicker
                presets={COMPANY_PRESETS}
                selected={draft.company_preferences}
                onToggle={(t) => toggleTag('company_preferences', t)}
                onAdd={(t) =>
                  patch({ company_preferences: [...draft.company_preferences, t] })
                }
              />
            </Card>

            {/* 简历 */}
            <ResumeCard resumes={resumes?.items ?? []} onChanged={() => void mutateResumes()} />
          </div>
        )}
      </div>
    </>
  );
}

function Card({
  title,
  hint,
  children,
}: {
  title: string;
  hint?: string;
  children: React.ReactNode;
}) {
  return (
    <section className="card px-5 py-4">
      <h2 className="text-sm font-semibold text-ink-800">{title}</h2>
      {hint ? <p className="mt-0.5 text-xs text-ink-400">{hint}</p> : null}
      <div className="mt-3">{children}</div>
    </section>
  );
}

function TagPicker({
  presets,
  selected,
  onToggle,
  onAdd,
}: {
  presets: string[];
  selected: string[];
  onToggle: (tag: string) => void;
  onAdd: (tag: string) => void;
}) {
  const [custom, setCustom] = useState('');

  return (
    <div className="space-y-2.5">
      <div className="flex flex-wrap gap-1.5">
        {presets.map((p) => (
          <button
            key={p}
            type="button"
            onClick={() => onToggle(p)}
            className={cn(
              'rounded-pill border px-2.5 py-1 text-xs font-medium transition',
              selected.includes(p)
                ? 'border-lilac-300 bg-lilac-100 text-lilac-700'
                : 'border-ink-200 bg-white text-ink-600 hover:text-lilac-700',
            )}
          >
            {p}
          </button>
        ))}
      </div>

      <div className="flex gap-2">
        <input
          className="input max-w-48"
          placeholder="自定义添加"
          value={custom}
          onChange={(e) => setCustom(e.target.value)}
          onKeyDown={(e) => {
            if (e.key !== 'Enter') return;
            e.preventDefault();
            const t = custom.trim();
            if (t && !selected.includes(t)) onAdd(t);
            setCustom('');
          }}
        />
        <Button
          size="sm"
          variant="secondary"
          onClick={() => {
            const t = custom.trim();
            if (t && !selected.includes(t)) onAdd(t);
            setCustom('');
          }}
        >
          添加
        </Button>
      </div>
    </div>
  );
}

function OrderedList({
  items,
  onMove,
  onRemove,
}: {
  items: string[];
  onMove: (index: number, delta: number) => void;
  onRemove: (index: number) => void;
}) {
  if (items.length === 0) {
    return <p className="text-xs text-ink-400">还没有选择，点击下方标签添加</p>;
  }

  return (
    <ol className="space-y-1.5">
      {items.map((item, i) => (
        <li
          key={`${item}-${i}`}
          className="flex items-center gap-2 rounded-xl border border-ink-100 bg-white px-3 py-2"
        >
          <span className="w-5 text-xs font-semibold text-lilac-500">{i + 1}</span>
          <span className="flex-1 text-sm text-ink-800">{item}</span>
          <button
            type="button"
            aria-label="上移"
            disabled={i === 0}
            onClick={() => onMove(i, -1)}
            className="rounded p-1 text-ink-400 transition hover:text-lilac-600 disabled:opacity-30"
          >
            ↑
          </button>
          <button
            type="button"
            aria-label="下移"
            disabled={i === items.length - 1}
            onClick={() => onMove(i, 1)}
            className="rounded p-1 text-ink-400 transition hover:text-lilac-600 disabled:opacity-30"
          >
            ↓
          </button>
          <button
            type="button"
            aria-label="移除"
            onClick={() => onRemove(i)}
            className="rounded p-1 text-ink-400 transition hover:text-blossom-600"
          >
            ✕
          </button>
        </li>
      ))}
    </ol>
  );
}

function ResumeCard({ resumes, onChanged }: { resumes: Resume[]; onChanged: () => void }) {
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [text, setText] = useState('');

  async function upload(file: File) {
    setBusy(true);
    setMsg(null);
    try {
      const rec = await api.upload<Resume>('/resumes', file);
      setMsg('上传成功。请粘贴简历文本以便生成结构化画像。');
      setEditingId(rec.id);
      onChanged();
    } catch (err) {
      setMsg(err instanceof Error ? err.message : '上传失败');
    } finally {
      setBusy(false);
    }
  }

  async function submitText(id: string) {
    setBusy(true);
    setMsg(null);
    try {
      await api.put(`/resumes/${id}/text`, { text });
      setMsg('已解析简历，匹配度分析会更贴合你的经历');
      setEditingId(null);
      setText('');
      onChanged();
    } catch (err) {
      setMsg(err instanceof Error ? err.message : '解析失败');
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card
      title="简历"
      hint="上传后粘贴简历正文，系统会生成结构化候选人画像，用于更准确的匹配分析。"
    >
      {msg ? (
        <p className="mb-3 rounded-xl bg-lilac-50 px-3 py-2 text-xs text-lilac-800">{msg}</p>
      ) : null}

      <label className="inline-flex cursor-pointer items-center gap-2 rounded-pill border border-lilac-200 bg-white px-4 py-2 text-sm font-medium text-lilac-700 transition hover:bg-lilac-50">
        <input
          type="file"
          accept=".pdf,.docx,.txt,.md"
          className="hidden"
          disabled={busy}
          onChange={(e) => {
            const f = e.target.files?.[0];
            if (f) void upload(f);
            e.target.value = '';
          }}
        />
        {busy ? '处理中…' : '上传简历'}
      </label>
      <p className="mt-1.5 text-[11px] text-ink-400">
        支持 PDF / DOCX / TXT / Markdown，单个文件不超过 10 MB
      </p>

      {resumes.length > 0 ? (
        <ul className="mt-4 space-y-2">
          {resumes.map((r) => (
            <li key={r.id} className="rounded-xl border border-ink-100 bg-white px-3 py-2.5">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div className="min-w-0">
                  <p className="truncate text-sm font-medium text-ink-800">{r.file_name}</p>
                  <p className="text-[11px] text-ink-400">
                    {formatRelative(r.created_at)} · {(r.file_size / 1024).toFixed(0)} KB
                  </p>
                </div>
                <div className="flex items-center gap-1.5">
                  {r.is_current ? <Badge tone="mint">当前简历</Badge> : null}
                  <Badge
                    tone={
                      r.parse_status === 'COMPLETED'
                        ? 'mint'
                        : r.parse_status === 'FAILED'
                          ? 'blossom'
                          : 'neutral'
                    }
                  >
                    {parseStatusLabel(r.parse_status)}
                  </Badge>
                </div>
              </div>

              <div className="mt-2 flex flex-wrap gap-1.5">
                {!r.is_current ? (
                  <Button
                    size="sm"
                    variant="ghost"
                    loading={busy}
                    onClick={async () => {
                      await api.put(`/resumes/${r.id}/current`);
                      onChanged();
                    }}
                  >
                    设为当前
                  </Button>
                ) : null}
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() => {
                    setEditingId(editingId === r.id ? null : r.id);
                    setText('');
                  }}
                >
                  {editingId === r.id ? '取消' : '粘贴 / 更新文本'}
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  loading={busy}
                  onClick={async () => {
                    await api.del(`/resumes/${r.id}`);
                    onChanged();
                  }}
                >
                  删除
                </Button>
              </div>

              {editingId === r.id ? (
                <div className="mt-2.5">
                  <textarea
                    className="input min-h-32 font-mono text-xs"
                    placeholder="把简历正文粘贴到这里。请不要包含身份证号、银行卡等敏感信息。"
                    value={text}
                    onChange={(e) => setText(e.target.value)}
                  />
                  <div className="mt-2 flex items-center justify-between">
                    <p className="text-[11px] text-ink-400">
                      系统会在解析前自动抹除可识别的证件号等敏感串
                    </p>
                    <Button
                      size="sm"
                      loading={busy}
                      disabled={text.trim().length === 0}
                      onClick={() => void submitText(r.id)}
                    >
                      解析简历
                    </Button>
                  </div>
                </div>
              ) : null}
            </li>
          ))}
        </ul>
      ) : null}
    </Card>
  );
}

function parseStatusLabel(s: string): string {
  return (
    { PENDING: '待解析', RUNNING: '解析中', COMPLETED: '已解析', FAILED: '解析失败' }[s] ?? s
  );
}
