/**
 * Network Ranker：对观测到的网络请求打分，筛出「可能是岗位列表接口」的候选。
 *
 * 为什么需要规则预筛：
 *   Exploration Agent 每步都会产生若干请求，全量交给 LLM 会浪费大量 token，
 *   且长上下文会稀释注意力。这里先用确定性规则打分，
 *   只把 Top N 候选交给 LLM 做语义判断——省钱且更准。
 *
 * 打分维度（对应设计文档的 Network Ranker）：
 *   - URL 关键词命中（search/list/position/job/query 等）
 *   - 响应是 JSON 且体积适中（岗位列表通常几 KB ~ 几百 KB）
 *   - 响应片段中包含数组结构与岗位相关字段名
 *   - HTTP 200
 *   - 惩罚：体积过小（多半是空结果或配置）、命中无关关键词（推荐/广告/热搜）
 */

import type { ObservedRequest } from './network_observer.js';

/** 打分后的候选。 */
export interface RankedRequest {
  request: ObservedRequest;
  score: number;
  /** 命中原因，便于日志与 LLM 理解。 */
  reasons: string[];
}

/** URL 中出现这些词，加分。 */
const URL_POSITIVE = [
  { re: /search/i, score: 18, why: 'URL 含 search' },
  { re: /position/i, score: 18, why: 'URL 含 position' },
  { re: /\bjob(s)?\b/i, score: 16, why: 'URL 含 job' },
  { re: /list/i, score: 12, why: 'URL 含 list' },
  { re: /query/i, score: 12, why: 'URL 含 query' },
  { re: /recruit/i, score: 16, why: 'URL 含 recruit' },
  { re: /campus/i, score: 16, why: 'URL 含 campus' },
  { re: /post/i, score: 10, why: 'URL 含 post' },
  { re: /\/api\//i, score: 8, why: 'URL 是 API 路径' },
  { re: /page/i, score: 4, why: 'URL 含分页参数' },
];

/** URL 中出现这些词，减分。 */
const URL_NEGATIVE = [
  { re: /recommend/i, score: -10, why: '疑似推荐位' },
  { re: /hot|trending|popular/i, score: -10, why: '疑似热榜' },
  { re: /banner|ad(s)?[./_-]/i, score: -12, why: '疑似广告' },
  { re: /user(profile|info)?[./_-]/i, score: -8, why: '疑似用户信息' },
  { re: /resume/i, score: -8, why: '疑似简历相关' },
  { re: /dict|enum|option|config/i, score: -12, why: '疑似字典/配置' },
  { re: /upload|download/i, score: -12, why: '疑似上传下载' },
  { re: /login|logout|captcha|sms/i, score: -15, why: '疑似登录验证' },
];

/** 响应片段中出现这些字段名，加分（说明返回的是岗位数据）。 */
const BODY_POSITIVE = [
  { re: /"positionList"\s*:/i, score: 30, why: '响应含 positionList' },
  { re: /"jobs?"\s*:\s*\[/i, score: 30, why: '响应含 jobs 数组' },
  { re: /"list"\s*:\s*\[/i, score: 22, why: '响应含 list 数组' },
  { re: /"data"\s*:\s*\[/i, score: 20, why: '响应含 data 数组' },
  { re: /"total"\s*:/i, score: 12, why: '响应含 total（分页）' },
  { re: /"postId"|"jobId"|"positionId"/i, score: 25, why: '响应含岗位 ID' },
  { re: /"jobName"|"positionName"|"positionTitle"|"jobTitle"/i, score: 25, why: '响应含岗位标题' },
  { re: /"cityName"|"workCity"|"location"/i, score: 12, why: '响应含城市字段' },
  { re: /"requirement"|"responsibilit|"description"/i, score: 14, why: '响应含 JD 字段' },
];

/** 合理的岗位列表响应体积区间（字节）。 */
const SIZE_MIN = 200;
const SIZE_MAX = 2_000_000;

/**
 * 对请求打分。
 *
 * @param req 观测到的请求
 * @returns 打分结果与命中原因
 */
export function scoreRequest(req: ObservedRequest): RankedRequest {
  let score = 0;
  const reasons: string[] = [];

  // 只考虑成功响应。
  if (req.status >= 200 && req.status < 300) {
    score += 10;
  } else {
    score -= 20;
    reasons.push(`HTTP ${req.status}`);
  }

  // URL 关键词。
  for (const p of URL_POSITIVE) {
    if (p.re.test(req.url)) {
      score += p.score;
      reasons.push(p.why);
    }
  }
  for (const n of URL_NEGATIVE) {
    if (n.re.test(req.url)) {
      score += n.score;
      reasons.push(n.why);
    }
  }

  // 响应类型：JSON 优先。
  if (req.contentType.includes('json')) {
    score += 20;
    reasons.push('响应是 JSON');
  } else if (req.contentType.includes('html')) {
    score -= 10;
    reasons.push('响应是 HTML 页面');
  }

  // 响应体字段名。
  for (const p of BODY_POSITIVE) {
    if (p.re.test(req.sample)) {
      score += p.score;
      reasons.push(p.why);
    }
  }

  // 体积合理性。
  if (req.size >= SIZE_MIN && req.size <= SIZE_MAX) {
    score += 8;
    reasons.push(`体积合理（${req.size} 字节）`);
  } else if (req.size < SIZE_MIN) {
    score -= 15;
    reasons.push(`响应过小（${req.size} 字节），多半是空结果`);
  }

  // POST 请求通常是搜索/查询动作，略微加分。
  if (req.method.toUpperCase() === 'POST') {
    score += 6;
    reasons.push('POST 查询请求');
  }

  return { request: req, score, reasons };
}

/**
 * 对一批请求打分并排序，返回 Top N。
 *
 * @param requests 候选请求
 * @param topN 返回条数，默认 5
 * @param minScore 最低分阈值，低于此值不返回（默认 20）
 */
export function rankRequests(
  requests: ObservedRequest[],
  topN = 5,
  minScore = 20,
): RankedRequest[] {
  return requests
    .map(scoreRequest)
    .filter((r) => r.score >= minScore)
    .sort((a, b) => b.score - a.score)
    .slice(0, topN);
}
