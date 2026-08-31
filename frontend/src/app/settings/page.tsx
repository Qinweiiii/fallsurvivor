'use client';

import { useEffect, useState } from 'react';
import useSWR from 'swr';
import { api, fetcher } from '@/lib/api';
import type { ApplicationProfileData } from '@/lib/types';
import { PageHeader } from '@/components/layout/PageHeader';
import { Button } from '@/components/ui/Button';
import { ErrorState, Skeleton } from '@/components/ui/States';
import { MyFigures } from '@/components/decor/MyFigures';

const EMPTY: ApplicationProfileData = {
  basic: { name: '', phone: '', email: '', gender: '', hometown: '', homepage: '', self_intro: '' },
  education: { school: '', major: '', degree: '', start_date: '', graduation_date: '', gpa: '' },
  preference: { city: '', role: '' },
  skills: { summary: '', languages: '' },
  experience: { projects: '', internships: '', awards: '' },
};

export default function SettingsPage() {
  const { data, error, isLoading, mutate } = useSWR<ApplicationProfileData>(
    '/application-profile',
    fetcher,
  );
  const { data: health } = useSWR<{ status: string; llm_enabled: boolean; search_ready: boolean }>(
    '/health',
    fetcher,
  );

  const [draft, setDraft] = useState<ApplicationProfileData>(EMPTY);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);

  useEffect(() => {
    if (data) setDraft({ ...EMPTY, ...data });
  }, [data]);

  async function save() {
    setBusy(true);
    setMsg(null);
    try {
      await api.put('/application-profile', draft);
      setMsg('申请信息已保存，辅助填写时会自动使用这些内容');
      await mutate();
    } catch (err) {
      setMsg(err instanceof Error ? err.message : '保存失败');
    } finally {
      setBusy(false);
    }
  }

  function patchGroup<K extends keyof ApplicationProfileData>(
    group: K,
    partial: Partial<ApplicationProfileData[K]>,
  ) {
    setDraft((prev) => ({ ...prev, [group]: { ...prev[group], ...partial } }));
  }

  return (
    <>
      <PageHeader
        title="设置"
        subtitle="配置可复用的申请信息，减少重复填写。"
        actions={
          <Button loading={busy} onClick={() => void save()}>
            保存
          </Button>
        }
      />

      <div className="flex-1 overflow-y-auto px-8 py-6">
        {error ? (
          <ErrorState message="加载设置失败" onRetry={() => void mutate()} />
        ) : isLoading ? (
          <div className="space-y-4">
            {Array.from({ length: 3 }).map((_, i) => (
              <Skeleton key={i} className="h-48 rounded-card" />
            ))}
          </div>
        ) : (
          <div className="space-y-5">
            {msg ? (
              <div className="rounded-card border border-lilac-200 bg-lilac-50 px-4 py-3 text-sm text-lilac-800">
                {msg}
              </div>
            ) : null}

            {/* 安全说明：这是产品的核心边界，必须显眼 */}
            <section className="card border-blossom-200 bg-blossom-50/70 px-5 py-4">
              <h2 className="text-sm font-semibold text-blossom-800">关于安全与隐私</h2>
              <ul className="mt-2 space-y-1.5 text-xs leading-relaxed text-blossom-800">
                <li>· 身份证号、护照号、银行卡、密码、验证码、人脸识别等信息一律不保存、不填写。</li>
                <li>· 这些字段在辅助填写时会被自动跳过，需要你在浏览器中亲自填写。</li>
                <li>· 招聘网站登录态只保存在本机浏览器目录，不会进入数据库，也不会发送给 AI。</li>
                <li>· 系统不会代替你点击任何提交按钮，最终提交始终由你完成。</li>
              </ul>
            </section>

            {/* 服务状态 */}
            {health ? (
              <section className="card px-5 py-4">
                <h2 className="text-sm font-semibold text-ink-800">服务状态</h2>
                <div className="mt-2.5 flex flex-wrap gap-4 text-xs">
                  <StatusLine label="后端服务" ok={health.status === 'ok'} />
                  <StatusLine
                    label="AI 能力（DeepSeek）"
                    ok={health.llm_enabled}
                    offHint="未配置 DEEPSEEK_API_KEY，将使用纯规则评分"
                  />
                  <StatusLine
                    label="岗位搜索（Tavily）"
                    ok={health.search_ready}
                    offHint="未配置 TAVILY_API_KEY，无法在线获取岗位"
                  />
                </div>
              </section>
            ) : null}

            <Card title="基本信息" hint="用于自动填写申请表中的常规字段">
              <Grid>
                <Field
                  label="姓名"
                  value={draft.basic.name}
                  onChange={(v) => patchGroup('basic', { name: v })}
                />
                <Field
                  label="手机号"
                  value={draft.basic.phone}
                  onChange={(v) => patchGroup('basic', { phone: v })}
                />
                <Field
                  label="邮箱"
                  value={draft.basic.email}
                  onChange={(v) => patchGroup('basic', { email: v })}
                />
                <Field
                  label="性别"
                  value={draft.basic.gender}
                  onChange={(v) => patchGroup('basic', { gender: v })}
                />
                <Field
                  label="籍贯"
                  value={draft.basic.hometown}
                  onChange={(v) => patchGroup('basic', { hometown: v })}
                />
                <Field
                  label="个人主页 / GitHub"
                  value={draft.basic.homepage}
                  onChange={(v) => patchGroup('basic', { homepage: v })}
                />
              </Grid>
              <div className="mt-3">
                <label className="label-text" htmlFor="self-intro">
                  自我评价
                </label>
                <textarea
                  id="self-intro"
                  className="input min-h-20"
                  value={draft.basic.self_intro}
                  onChange={(e) => patchGroup('basic', { self_intro: e.target.value })}
                />
              </div>
            </Card>

            <Card title="教育经历">
              <Grid>
                <Field
                  label="学校"
                  value={draft.education.school}
                  onChange={(v) => patchGroup('education', { school: v })}
                />
                <Field
                  label="专业"
                  value={draft.education.major}
                  onChange={(v) => patchGroup('education', { major: v })}
                />
                <Field
                  label="学历"
                  value={draft.education.degree}
                  onChange={(v) => patchGroup('education', { degree: v })}
                  placeholder="如 硕士"
                />
                <Field
                  label="GPA / 成绩"
                  value={draft.education.gpa}
                  onChange={(v) => patchGroup('education', { gpa: v })}
                />
                <Field
                  label="入学时间"
                  value={draft.education.start_date}
                  onChange={(v) => patchGroup('education', { start_date: v })}
                  placeholder="如 2025-09"
                />
                <Field
                  label="毕业时间"
                  value={draft.education.graduation_date}
                  onChange={(v) => patchGroup('education', { graduation_date: v })}
                  placeholder="如 2027-06"
                />
              </Grid>
            </Card>

            <Card title="求职意向">
              <Grid>
                <Field
                  label="期望城市"
                  value={draft.preference.city}
                  onChange={(v) => patchGroup('preference', { city: v })}
                />
                <Field
                  label="期望岗位"
                  value={draft.preference.role}
                  onChange={(v) => patchGroup('preference', { role: v })}
                />
              </Grid>
            </Card>

            <Card title="技能与经历" hint="填写后可自动填入申请表的长文本字段">
              <div className="space-y-3">
                <TextArea
                  label="技术栈概述"
                  value={draft.skills.summary}
                  onChange={(v) => patchGroup('skills', { summary: v })}
                />
                <TextArea
                  label="语言能力"
                  value={draft.skills.languages}
                  onChange={(v) => patchGroup('skills', { languages: v })}
                  placeholder="如 CET-6，可英文技术文档阅读"
                />
                <TextArea
                  label="项目经历"
                  value={draft.experience.projects}
                  onChange={(v) => patchGroup('experience', { projects: v })}
                />
                <TextArea
                  label="实习经历"
                  value={draft.experience.internships}
                  onChange={(v) => patchGroup('experience', { internships: v })}
                />
                <TextArea
                  label="获奖情况"
                  value={draft.experience.awards}
                  onChange={(v) => patchGroup('experience', { awards: v })}
                />
              </div>
            </Card>

            <MyFigures />
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

function Grid({ children }: { children: React.ReactNode }) {
  return <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">{children}</div>;
}

function Field({
  label,
  value,
  onChange,
  placeholder,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
}) {
  return (
    <div>
      <span className="label-text">{label}</span>
      <input
        className="input"
        value={value}
        placeholder={placeholder}
        onChange={(e) => onChange(e.target.value)}
      />
    </div>
  );
}

function TextArea({
  label,
  value,
  onChange,
  placeholder,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
}) {
  return (
    <div>
      <span className="label-text">{label}</span>
      <textarea
        className="input min-h-20"
        value={value}
        placeholder={placeholder}
        onChange={(e) => onChange(e.target.value)}
      />
    </div>
  );
}

function StatusLine({
  label,
  ok,
  offHint,
}: {
  label: string;
  ok: boolean;
  offHint?: string;
}) {
  return (
    <div className="flex items-center gap-2">
      <span
        className={`h-2 w-2 rounded-full ${ok ? 'bg-mint-500' : 'bg-cream-500'}`}
        aria-hidden="true"
      />
      <span className="text-ink-700">{label}</span>
      <span className="text-ink-400">{ok ? '正常' : (offHint ?? '未启用')}</span>
    </div>
  );
}
