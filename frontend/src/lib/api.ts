/**
 * API 客户端。
 *
 * 说明：所有渲染都走 React 的自动转义，不使用 dangerouslySetInnerHTML，
 * 因此后端返回的 JD 文本可以安全展示。
 */

import type {
  ApiEnvelope,
  JobDetail,
  SiteCrawlRequest,
  SiteCrawlResult,
  SiteRecipe,
  SiteRecipeRun,
  SiteRecipeVerifyResult,
} from './types';

const BASE_URL =
  process.env.NEXT_PUBLIC_API_BASE_URL?.replace(/\/$/, '') ?? 'http://localhost:9090/api';

/** 业务错误。data 可能携带服务端返回的结构化信息（如冲突时的当前任务状态）。 */
export class ApiError extends Error {
  constructor(
    message: string,
    public readonly code: number,
    public readonly status: number,
    public readonly data?: unknown,
  ) {
    super(message);
    this.name = 'ApiError';
  }
}

interface RequestOptions {
  method?: 'GET' | 'POST' | 'PUT' | 'DELETE';
  body?: unknown;
  signal?: AbortSignal;
}

async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const { method = 'GET', body, signal } = options;

  const init: RequestInit = {
    method,
    headers: { Accept: 'application/json' },
    signal,
    // 本项目为本地单用户工具，不使用 Cookie 凭据，避免 CSRF 面。
    credentials: 'omit',
    cache: 'no-store',
  };

  if (body !== undefined) {
    init.headers = { ...init.headers, 'Content-Type': 'application/json' };
    init.body = JSON.stringify(body);
  }

  const res = await fetch(`${BASE_URL}${path}`, init);

  let payload: ApiEnvelope<T> | null = null;
  try {
    payload = (await res.json()) as ApiEnvelope<T>;
  } catch {
    throw new ApiError('服务器返回了无法解析的内容', -1, res.status);
  }

  if (!res.ok || payload.code !== 0) {
    throw new ApiError(payload.message || '请求失败', payload.code ?? -1, res.status, payload.data);
  }
  return payload.data;
}

/** 上传文件（multipart）。 */
async function upload<T>(path: string, file: File): Promise<T> {
  const form = new FormData();
  form.append('file', file);

  const res = await fetch(`${BASE_URL}${path}`, {
    method: 'POST',
    body: form,
    credentials: 'omit',
  });

  let payload: ApiEnvelope<T> | null = null;
  try {
    payload = (await res.json()) as ApiEnvelope<T>;
  } catch {
    throw new ApiError('服务器返回了无法解析的内容', -1, res.status);
  }
  if (!res.ok || payload.code !== 0) {
    throw new ApiError(payload.message || '上传失败', payload.code ?? -1, res.status);
  }
  return payload.data;
}

/** 构造查询串，自动跳过空值。 */
export function buildQuery(params: Record<string, unknown>): string {
  const sp = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null || value === '') continue;
    if (Array.isArray(value)) {
      for (const v of value) {
        if (v !== undefined && v !== null && v !== '') sp.append(key, String(v));
      }
      continue;
    }
    sp.set(key, String(value));
  }
  const s = sp.toString();
  return s ? `?${s}` : '';
}

export const api = {
  get: <T>(path: string, signal?: AbortSignal) => request<T>(path, { method: 'GET', signal }),
  post: <T>(path: string, body?: unknown) => request<T>(path, { method: 'POST', body }),
  put: <T>(path: string, body?: unknown) => request<T>(path, { method: 'PUT', body }),
  del: <T>(path: string, body?: unknown) => request<T>(path, { method: 'DELETE', body }),
  upload,
};

/** SWR 的默认 fetcher。 */
export const fetcher = <T>(path: string): Promise<T> => api.get<T>(path);

/** 用已登录浏览器抓取真实 JD 的结果。 */
export interface ScrapeResult {
  needs_login: boolean;
  task_id: string;
  message: string;
}

/** 岗位相关接口封装（Drawer 抓取 JD 使用）。 */
export const jobsApi = {
  detail: (id: string) => api.get<JobDetail>('/jobs/' + id),
  scrape: (id: string) => api.post<ScrapeResult>('/jobs/' + id + '/scrape'),
  scrapeResume: (id: string, taskId: string) =>
    api.post<ScrapeResult>('/jobs/' + id + '/scrape/resume', { task_id: taskId }),
  crawl: (body: SiteCrawlRequest) => api.post<SiteCrawlResult>('/jobs/crawl', body),
};

/** 站点 Recipe 配置管理接口封装。 */
export const siteRecipesApi = {
  list: () => api.get<{ recipes: SiteRecipe[]; count: number }>('/site-recipes'),
  create: (body: Partial<SiteRecipe>) => api.post<SiteRecipe>('/site-recipes', body),
  update: (id: string, body: Partial<SiteRecipe>) => api.put<SiteRecipe>(`/site-recipes/${id}`, body),
  remove: (id: string) => api.del<{ deleted: boolean }>(`/site-recipes/${id}`),
  runs: (id: string) => api.get<{ runs: SiteRecipeRun[]; count: number }>(`/site-recipes/${id}/runs`),
  /**
   * 用真实请求验证该配置能否采到岗位（仅 api 策略）。
   * 结果会写回健康度，因此调用后应刷新列表。
   */
  verify: (id: string, keyword?: string) =>
    api.post<SiteRecipeVerifyResult>(`/site-recipes/${id}/verify`, { keyword: keyword ?? '' }),
};

/** JD 补全结果。 */
export interface EnrichJobResult {
  job_id: string;
  title: string;
  /** enriched / skipped / failed */
  status: string;
  reason: string;
  runes: number;
  new_score: number;
  old_score: number;
  desc_quality: string;
}

/** JD 补全汇总。 */
export interface EnrichSummary {
  total: number;
  enriched: number;
  skipped: number;
  failed: number;
  duration_ms: number;
  results: EnrichJobResult[];
}

/** JD 补全接口：为摘要型岗位（如 BOSS）补齐完整 JD 并重算评分。 */
export const enrichmentApi = {
  /**
   * 补全 JD。
   * @param body 可选：{ job_ids?: string[]; max_jobs?: number }
   *             传空对象表示自动挑选全部 JD 不完整的岗位。
   */
  enrich: (body: { job_ids?: string[]; max_jobs?: number } = {}) =>
    api.post<EnrichSummary>('/jobs/enrich-jd', body),
};
