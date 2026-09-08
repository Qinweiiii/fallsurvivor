import { clsx, type ClassValue } from 'clsx';
import { twMerge } from 'tailwind-merge';

/** 合并 className。 */
export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs));
}

/** 岗位状态的中文名。 */
export const JOB_STATUS_LABELS: Record<string, string> = {
  NEW: '未处理',
  IN_CART: '已在岗位车',
  PREPARING: '已准备投递',
  SUBMITTED: '已投递',
  CLOSED: '已结束',
};

/** JD 补全状态的中文名。 */
export const ENRICHMENT_STATUS_LABELS: Record<string, string> = {
  none: '未补全',
  pending_enrichment: '待补全',
  enriching: '补全中',
  enriched: '已补全',
  enrich_failed: '补全失败',
};

/** 投递状态的中文名。 */
export const APP_STATUS_LABELS: Record<string, string> = {
  PREPARING: '待准备',
  LOGIN_REQUIRED: '需要登录',
  FORM_ANALYZING: '识别表单中',
  FORM_FILLING: '填写中',
  WAITING_USER: '等待人工操作',
  READY_TO_SUBMIT: '待最终审核',
  SUBMITTED: '已投递',
  WRITTEN_TEST: '笔试',
  INTERVIEW_1: '一面',
  INTERVIEW_2: '二面',
  HR_INTERVIEW: 'HR 面',
  OFFER: 'Offer',
  REJECTED: '已拒绝',
  WITHDRAWN: '已撤回',
  FAILED: '异常',
  BLOCKED: '被阻止',
};

/** 来源的中文名。 */
export const SOURCE_LABELS: Record<string, string> = {
  OFFICIAL: '官方招聘站',
  BOSS: 'BOSS直聘',
  TAVILY: '全网搜索',
  MANUAL: '手动添加',
};

/** 搜索任务状态的中文名。 */
export const SEARCH_STATUS_LABELS: Record<string, string> = {
  PENDING: '排队中',
  RUNNING: '正在获取岗位',
  COMPLETED: '已完成',
  COMPLETED_WITH_WARNING: '已完成（部分来源失败）',
  FAILED: '获取失败',
  TERMINATED: '已终止（服务关闭/重启）',
};

/** 结束态的投递状态。 */
const TERMINAL_STATUSES = new Set(['OFFER', 'REJECTED', 'WITHDRAWN']);

/** 判断投递状态是否已结束。 */
export function isTerminalStatus(status: string): boolean {
  return TERMINAL_STATUSES.has(status);
}

/** 格式化日期为 YYYY-MM-DD。 */
export function formatDate(iso: string | null | undefined): string {
  if (!iso) return '——';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '——';
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

/** 格式化为相对时间。 */
export function formatRelative(iso: string | null | undefined): string {
  if (!iso) return '——';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '——';

  const diffMs = Date.now() - d.getTime();
  const minutes = Math.floor(diffMs / 60000);
  if (minutes < 1) return '刚刚';
  if (minutes < 60) return `${minutes} 分钟前`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} 小时前`;
  const days = Math.floor(hours / 24);
  if (days < 30) return `${days} 天前`;
  return formatDate(iso);
}

/** 剩余天数的展示文案。 */
export function formatDaysLeft(days: number): string {
  if (days < 0) return '已截止';
  if (days === 0) return '今天截止';
  if (days === 1) return '明天截止';
  return `还剩 ${days} 天`;
}

function pad(n: number): string {
  return n < 10 ? `0${n}` : String(n);
}

/** 匹配度对应的语义色。 */
export function scoreTone(score: number): 'high' | 'good' | 'mid' | 'low' {
  if (score >= 90) return 'high';
  if (score >= 80) return 'good';
  if (score >= 60) return 'mid';
  return 'low';
}

/** 显示值，空则返回占位符。 */
export function orDash(v: string | null | undefined): string {
  const t = cleanDisplayValue(v);
  return t === '' ? '——' : t;
}

export function cleanDisplayValue(v: string | null | undefined): string {
  const t = (v ?? '').trim();
  return ['', '<nil>', 'nil', 'null', 'undefined'].includes(t.toLowerCase()) ? '' : t;
}

export function firstDisplayValue(...values: Array<string | null | undefined>): string {
  for (const v of values) {
    const t = cleanDisplayValue(v);
    if (t) return t;
  }
  return '';
}

export function cleanDisplayText(v: string | null | undefined): string {
  const t = cleanDisplayValue(v);
  if (!t) return '';
  if (/[：:]\s*(<nil>|nil|null|undefined)\s*$/i.test(t)) return '';
  return t
    .replace(/\s*(<nil>|nil|null|undefined)\s*/gi, ' ')
    .replace(/[：:，,、；;。.\s]+$/g, '')
    .trim();
}

export function cleanDisplayList(values: Array<string | null | undefined> | null | undefined): string[] {
  const out: string[] = [];
  const seen = new Set<string>();
  for (const v of values ?? []) {
    const cleaned = cleanDisplayText(v);
    if (!cleaned) continue;
    const key = cleaned.toLowerCase();
    if (seen.has(key)) continue;
    seen.add(key);
    out.push(cleaned);
  }
  return out;
}
