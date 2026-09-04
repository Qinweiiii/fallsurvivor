/**
 * Network Observer：记录页面发出的网络请求，供 Exploration Agent 发现数据接口。
 *
 * 设计目标（对应设计文档 Phase 3）：
 *   1. 通用记录：不针对任何站点硬编码 URL，记录所有 XHR/Fetch 请求；
 *   2. 噪声过滤：静态资源（图片/字体/CSS/JS）与埋点上报不进入候选；
 *   3. 只读安全：只读取请求/响应元信息与**截断后的**内容片段，不执行响应内容；
 *   4. 有界内存：每个会话只保留最近 N 条，避免长时间探索撑爆内存。
 *
 * 为什么必须记录「请求」而不只是「响应」：
 *   POST 型列表接口（相当常见）的可复现关键信息全在请求侧——
 *   请求体的结构、Content-Type（JSON 还是 form）、以及少量必需的头部。
 *   只看响应时，Agent 无法自己推断出「这个接口要发什么才能拿到数据」，
 *   最终只能靠人工试错去补分支。记录请求后，Agent 才具备「照原样复现」的能力。
 *
 * 重要：请求体与响应体都可能包含个人隐私或敏感业务数据，
 * 因此返回给后端的片段一律经过脱敏并严格截断；
 * 请求头只保留**语义白名单**内的字段（绝不外泄 Cookie / Authorization）。
 */

import type { Page, Response } from 'playwright';
import { redact } from './sensitive_guard.js';

/** 单个被观测到的网络请求。 */
export interface ObservedRequest {
  /** 请求序号，按观测顺序递增，用于 Diff 时判断「新增」。 */
  seq: number;
  method: string;
  url: string;
  status: number;
  /** 响应 content-type（已去掉参数部分，如 charset）。 */
  contentType: string;
  /** 响应体字节数（未截断前的大小）。 */
  size: number;
  /** 响应体片段，已脱敏并截断，可能为空字符串。 */
  sample: string;
  /** inspect 时使用的较大响应片段，已脱敏并截断。 */
  fullSample: string;
  /** JSON 响应结构摘要，帮助 LLM 看字段语义与层级。 */
  schemaSummary: string;
  /** 请求体片段（已脱敏截断）。GET 请求为空串。 */
  requestBody: string;
  /** 请求的 Content-Type（已去掉参数部分）。GET 请求为空串。 */
  requestContentType: string;
  /**
   * 请求头白名单快照，只含影响接口行为的语义头部
   * （如 accept / x-requested-with / 站点自定义的语言与渠道头）。
   * 凭证类头部（cookie / authorization / token 等）一律不记录。
   */
  requestHeaders: Record<string, string>;
  /** 观测时间戳（毫秒）。 */
  at: number;
}

/** 每个会话最多保留的请求条数。 */
const MAX_RECORDS = 300;

/** 单个请求最多保留的响应片段字符数。 */
const SAMPLE_LIMIT = 500;

/** inspect 单个请求时最多返回的响应片段字符数。 */
const FULL_SAMPLE_LIMIT = 20_000;

/**
 * 单个请求最多保留的请求体字符数。
 *
 * 比响应片段给得更宽：请求体通常很小（几十到几百字符），
 * 但它是「能否原样复现该接口」的决定性信息，不该被截断到看不出结构。
 */
const REQUEST_BODY_LIMIT = 2_000;

/**
 * 请求头白名单：只记录会影响接口返回内容的语义头部。
 *
 * 刻意不含 cookie / authorization / x-csrf-token 等凭证类头部：
 *   1. 它们会被写入 Recipe 并长期留存，等于把登录态明文落库；
 *   2. 凭证会过期，沉淀下来只会让 Recipe 在下次执行时莫名失败；
 *   3. 需要登录才能读的接口本就应走浏览器策略，而不是脱离会话直连。
 */
const REQUEST_HEADER_ALLOWLIST = [
  'accept',
  'accept-language',
  'content-type',
  'origin',
  'referer',
  'user-agent',
  'x-requested-with',
  'sec-fetch-dest',
  'sec-fetch-mode',
  'sec-fetch-site',
];

/**
 * 请求头白名单的前缀规则。
 *
 * 很多站点用自定义头传「渠道 / 语言 / 门户类型」等非凭证参数
 * （如 portal-channel、x-portal-type），缺了它们接口会返回空列表。
 * 这里按前缀放行，但仍然逐个排除凭证类关键词（见 isSensitiveHeader）。
 */
const REQUEST_HEADER_PREFIX_ALLOWLIST = ['portal-', 'x-portal-', 'x-site-', 'x-lang', 'sec-ch-ua'];

/** 凭证类关键词：命中即拒绝记录，优先级高于任何白名单。 */
const SENSITIVE_HEADER_PATTERN =
  /(cookie|auth|token|secret|password|session|csrf|signature|sign|credential|key)/i;

/** 单个请求最多用于结构分析的响应体字符数。 */
const SCHEMA_BODY_LIMIT = 80_000;

/** 结构摘要最多保留字符数。 */
const SCHEMA_SUMMARY_LIMIT = 1_500;

/** 只观测这些响应类型的请求（其余视为静态资源或噪声）。 */
const OBSERVED_CONTENT_TYPES = [
  'application/json',
  'text/json',
  'application/javascript',
  'text/javascript',
  'text/plain',
  'application/xml',
  'text/xml',
  'text/html',
];

/** URL 中的噪声特征：命中则不观测。 */
const NOISE_URL_PATTERNS = [
  // 埋点与监控
  /beacon/i,
  /analytics/i,
  /track(ing)?[./_-]/i,
  /log(store|ger|ging)?[./_-]/i,
  /report(ing)?[./_-]/i,
  /monitor/i,
  /sentry/i,
  /rum[./_-]/i,
  /metrics/i,
  // 静态资源
  /\.(png|jpe?g|gif|webp|svg|ico|bmp)(\?|$)/i,
  /\.(woff2?|ttf|otf|eot)(\?|$)/i,
  /\.(css|less|scss)(\?|$)/i,
  /\.(mp4|webm|mp3|wav)(\?|$)/i,
  // 配置与推送
  /config\.json/i,
  /manifest\.json/i,
  /\/sockjs\//i,
  /\/websocket/i,
];

/**
 * 脱敏响应片段：抹除可能的手机号、身份证、邮箱、银行卡号与令牌。
 *
 * 卡号判断复用 sensitive_guard 的 Luhn 校验，避免把招聘站业务 ID
 * 当作银行卡号抹掉，破坏后续岗位身份与详情 URL 拼接。
 */
function redactSample(text: string): string {
  return redact(text)
    .replace(/\b1[3-9]\d{9}\b/g, '[REDACTED_PHONE]')
    .replace(/\b[\w.+-]+@[\w-]+\.[\w.-]+\b/g, '[REDACTED_EMAIL]')
    .replace(/(token|secret|password|passwd|pwd|authorization)["'\s:=]+[\w.-]{8,}/gi, '$1=[REDACTED]');
}

/** 提取 content-type 的主类型（去掉 charset 等参数并转小写）。 */
function normalizeContentType(raw: string): string {
  return (raw || '').split(';')[0]!.trim().toLowerCase();
}

/** 判断该请求头是否属于凭证类（命中即不记录）。 */
function isSensitiveHeader(name: string): boolean {
  return SENSITIVE_HEADER_PATTERN.test(name);
}

/**
 * 过滤请求头：只保留白名单内、且非凭证类的头部。
 *
 * 这一步是「可复现」与「不泄密」之间的分界线：
 * 保留的是接口语义参数，丢弃的是身份凭证。
 */
function pickRequestHeaders(raw: Record<string, string>): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [rawName, rawValue] of Object.entries(raw)) {
    const name = rawName.toLowerCase().trim();
    if (!name || isSensitiveHeader(name)) continue;

    const allowed =
      REQUEST_HEADER_ALLOWLIST.includes(name) ||
      REQUEST_HEADER_PREFIX_ALLOWLIST.some((p) => name.startsWith(p));
    if (!allowed) continue;

    const value = String(rawValue ?? '').trim();
    if (!value || value.length > 200) continue; // 超长值多半是编码后的凭证或指纹
    out[name] = value;
  }
  return out;
}

function primitiveKind(v: unknown): string {
  if (v === null) return 'null';
  if (Array.isArray(v)) return 'array';
  return typeof v;
}

function shortenValue(v: unknown): string {
  if (typeof v !== 'string' && typeof v !== 'number' && typeof v !== 'boolean') {
    return primitiveKind(v);
  }
  const s = String(v).replace(/\s+/g, ' ').trim();
  if (!s) return primitiveKind(v);
  return s.length > 32 ? `${s.slice(0, 32)}...` : s;
}

function summarizeJSONValue(value: unknown, path: string, depth: number, out: string[]): void {
  if (out.length >= 24 || depth > 4) return;

  if (Array.isArray(value)) {
    const first = value[0];
    if (first && typeof first === 'object' && !Array.isArray(first)) {
      const keys = Object.keys(first as Record<string, unknown>).slice(0, 28);
      out.push(`${path}: array[${value.length}] item{${keys.join(', ')}}`);
      for (const key of keys.slice(0, 12)) {
        summarizeJSONValue((first as Record<string, unknown>)[key], `${path}[0].${key}`, depth + 1, out);
      }
      return;
    }
    out.push(`${path}: array[${value.length}] item=${primitiveKind(first)}`);
    return;
  }

  if (value && typeof value === 'object') {
    const obj = value as Record<string, unknown>;
    const keys = Object.keys(obj).slice(0, 28);
    out.push(`${path}: object{${keys.join(', ')}}`);
    for (const key of keys.slice(0, 14)) {
      summarizeJSONValue(obj[key], `${path}.${key}`, depth + 1, out);
    }
    return;
  }

  out.push(`${path}: ${primitiveKind(value)}=${shortenValue(value)}`);
}

function summarizeJSON(text: string): string {
  try {
    const value = JSON.parse(text);
    const out: string[] = [];
    summarizeJSONValue(value, '$', 0, out);
    return redactSample(out.join('\n')).slice(0, SCHEMA_SUMMARY_LIMIT);
  } catch {
    return '';
  }
}

/** 判断该响应是否需要观测。 */
function shouldObserve(url: string, contentType: string): boolean {
  if (OBSERVED_CONTENT_TYPES.indexOf(contentType) === -1) return false;
  for (const p of NOISE_URL_PATTERNS) {
    if (p.test(url)) return false;
  }
  return true;
}

/**
 * 请求记录器。每个浏览器会话持有一个实例。
 *
 * 用法：
 *   const obs = new NetworkObserver();
 *   obs.attach(page);          // 开始观测
 *   const all = obs.list();    // 列出全部
 *   const diff = obs.since(5); // 列出 seq > 5 的新增请求
 */
export class NetworkObserver {
  private records: ObservedRequest[] = [];
  private seq = 0;
  private detached = false;
  private onResponse: ((r: Response) => void) | null = null;

  /** 绑定到页面，开始观测。可重复调用（幂等）。 */
  attach(page: Page): void {
    if (this.onResponse) return; // 已挂载
    this.detached = false;
    const handler = (resp: Response) => {
      void this.capture(resp);
    };
    this.onResponse = handler;
    page.on('response', handler);
  }

  /** 停止观测。 */
  detach(page: Page): void {
    if (this.onResponse) {
      page.off('response', this.onResponse);
      this.onResponse = null;
    }
    this.detached = true;
  }

  /** 清空记录（探索新站点或执行新 Action 前调用）。 */
  reset(): void {
    this.records = [];
    this.seq = 0;
  }

  /** 采集单个响应。失败静默——观测不应影响主流程。 */
  private async capture(resp: Response): Promise<void> {
    if (this.detached) return;

    const url = resp.url();
    const contentType = normalizeContentType(resp.headers()['content-type'] ?? '');
    if (!shouldObserve(url, contentType)) return;

    let sample = '';
    let fullSample = '';
    let schemaSummary = '';
    let size = 0;
    try {
      const body = await resp.text();
      size = body.length;
      sample = redactSample(body.slice(0, SAMPLE_LIMIT));
      fullSample = redactSample(body.slice(0, FULL_SAMPLE_LIMIT));
      if (contentType.includes('json')) {
        schemaSummary = summarizeJSON(body.slice(0, SCHEMA_BODY_LIMIT));
      }
    } catch {
      // 响应体可能已被释放或重定向，忽略即可。
    }

    // 请求侧信息：决定「怎么发才能拿到这份响应」，是 Recipe 可复现的关键。
    const request = resp.request();
    let requestBody = '';
    let requestContentType = '';
    let requestHeaders: Record<string, string> = {};
    try {
      requestContentType = normalizeContentType(
        (await request.headerValue('content-type')) ?? '',
      );
      requestHeaders = pickRequestHeaders(await request.allHeaders());
      const post = request.postData();
      if (post) {
        requestBody = redactSample(post.slice(0, REQUEST_BODY_LIMIT));
      }
    } catch {
      // 请求可能已被回收（如重定向后的中间请求），缺失即视为无请求体。
    }

    this.seq += 1;
    this.records.push({
      seq: this.seq,
      method: request.method(),
      url,
      status: resp.status(),
      contentType,
      size,
      sample,
      fullSample,
      schemaSummary,
      requestBody,
      requestContentType,
      requestHeaders,
      at: Date.now(),
    });

    // 有界：超出上限时丢弃最旧的记录。
    if (this.records.length > MAX_RECORDS) {
      this.records.splice(0, this.records.length - MAX_RECORDS);
    }
  }

  /** 列出全部记录。limit<=0 时返回全部。 */
  list(limit = 0): ObservedRequest[] {
    if (limit > 0 && this.records.length > limit) {
      return this.records.slice(this.records.length - limit);
    }
    return [...this.records];
  }

  /** 列出 seq 严格大于 afterSeq 的记录，用于 Action 前后做 Diff。 */
  since(afterSeq: number): ObservedRequest[] {
    return this.records.filter((r) => r.seq > afterSeq);
  }

  /** 当前已观测到的最大 seq（用作下一次 Diff 的基线）。 */
  cursor(): number {
    return this.seq;
  }

  /** 按 URL 子串查找记录（不区分大小写）。 */
  findByURL(substr: string): ObservedRequest[] {
    const needle = substr.toLowerCase();
    return this.records.filter((r) => r.url.toLowerCase().includes(needle));
  }

  /** 按 seq 精确查找记录，供 inspect 动作读取较大响应片段。 */
  findBySeq(seq: number): ObservedRequest | null {
    return this.records.find((r) => r.seq === seq) ?? null;
  }
}
