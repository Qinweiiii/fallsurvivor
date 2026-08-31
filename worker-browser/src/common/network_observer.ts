/**
 * Network Observer：记录页面发出的网络请求，供 Exploration Agent 发现数据接口。
 *
 * 设计目标（对应设计文档 Phase 3）：
 *   1. 通用记录：不针对任何站点硬编码 URL，记录所有 XHR/Fetch 请求；
 *   2. 噪声过滤：静态资源（图片/字体/CSS/JS）与埋点上报不进入候选；
 *   3. 只读安全：只读取响应元信息与**截断后的**响应片段，不执行响应内容；
 *   4. 有界内存：每个会话只保留最近 N 条，避免长时间探索撑爆内存。
 *
 * 重要：响应体可能包含个人隐私或敏感业务数据，
 * 因此返回给后端的 sample 一律经过脱敏并严格截断。
 */

import type { Page, Response } from 'playwright';

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
  /** 观测时间戳（毫秒）。 */
  at: number;
}

/** 每个会话最多保留的请求条数。 */
const MAX_RECORDS = 300;

/** 单个请求最多保留的响应片段字符数。 */
const SAMPLE_LIMIT = 500;

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
 * 这里做保守替换——宁可多抹，也不要把个人信息送进 LLM。
 */
function redactSample(text: string): string {
  return text
    .replace(/\b1[3-9]\d{9}\b/g, '[REDACTED_PHONE]')
    .replace(/\b\d{17}[\dXx]\b/g, '[REDACTED_ID]')
    .replace(/\b[\w.+-]+@[\w-]+\.[\w.-]+\b/g, '[REDACTED_EMAIL]')
    .replace(/\b\d{16,19}\b/g, '[REDACTED_CARD]')
    .replace(/(token|secret|password|passwd|pwd|authorization)["'\s:=]+[\w.-]{8,}/gi, '$1=[REDACTED]');
}

/** 提取 content-type 的主类型（去掉 charset 等参数并转小写）。 */
function normalizeContentType(raw: string): string {
  return (raw || '').split(';')[0]!.trim().toLowerCase();
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
    let size = 0;
    try {
      const body = await resp.text();
      size = body.length;
      sample = redactSample(body.slice(0, SAMPLE_LIMIT));
    } catch {
      // 响应体可能已被释放或重定向，忽略即可。
    }

    this.seq += 1;
    this.records.push({
      seq: this.seq,
      method: resp.request().method(),
      url,
      status: resp.status(),
      contentType,
      size,
      sample,
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
}
