/**
 * 安全导航动作执行器。
 *
 * 设计原则（与既有表单填写一致的安全边界）：
 *   1. 绝不执行任意 JS / 任意选择器；点击只能按「可见文案」匹配元素；
 *   2. 任何命中提交/投递/确认类文案的元素都拒绝点击；
 *   3. navigate 只接受 http/https 公网地址，禁止指向本机或内网。
 *
 * 本模块不触发任何「最终提交」，投递动作始终由用户亲自完成。
 */

import type { Page, Locator } from 'playwright';
import type { NavAction } from '../types.js';

/** 最终提交类关键词，绝对禁止点击。 */
const FINAL_SUBMIT_KEYWORDS = [
  '提交', '投递', '确认', '保存并提交', '立即投递',
  'submit', 'confirm', 'apply now', 'send application',
];

function looksLikeFinalSubmit(text: string): boolean {
  const t = text.replace(/\s+/g, '').toLowerCase();
  return FINAL_SUBMIT_KEYWORDS.some((k) => t.includes(k));
}

/** 导航目标 URL 合法性与公网约束。 */
export function isSafeNavURL(raw: string): boolean {
  let u: URL;
  try {
    u = new URL(raw);
  } catch {
    return false;
  }
  if (u.protocol !== 'http:' && u.protocol !== 'https:') return false;
  const host = u.hostname.toLowerCase();
  if (host === 'localhost' || host.endsWith('.local') || host.endsWith('.internal')) return false;
  if (
    /^(127\.|10\.|192\.168\.|169\.254\.|0\.0\.0\.0|::1|fe80:)/.test(host) ||
    host.startsWith('[')
  ) {
    return false;
  }
  return true;
}

function normalize(s: string): string {
  return s.replace(/\s+/g, '').trim();
}

/**
 * 按可见文案定位可点击元素。
 * 在按钮 / 链接 / 标签页 / 列表项范围内匹配，优先精确匹配，
 * 其次子串包含匹配；命中提交类文案的元素一律跳过。
 */
async function findClickableByText(page: Page, target: string): Promise<Locator | null> {
  const norm = normalize(target);
  if (!norm) return null;

  const selector =
    'a, button, [role="button"], .tab, [class*="tab"], [class*="Tab"], ' +
    'li[class*="item"], div[class*="item"], [class*="entry"]';
  const candidates = page.locator(selector);
  const count = Math.min(await candidates.count(), 300);

  let best: { loc: Locator; score: number } | null = null;
  for (let i = 0; i < count; i++) {
    const el = candidates.nth(i);
    const raw = (await el.innerText().catch(() => '')) || '';
    const label = normalize(raw);
    if (!label) continue;
    if (looksLikeFinalSubmit(label)) continue;

    let score = 0;
    if (label === norm) score = 100;
    else if (label.includes(norm)) score = 80 - (label.length - norm.length);
    else if (norm.includes(label) && label.length >= 2) score = 60 - (norm.length - label.length);
    if (score > 0 && (!best || score > best.score)) best = { loc: el, score };
  }
  return best ? best.loc : null;
}

/** 执行单个导航动作，返回是否成功及说明。 */
export async function performNavAction(
  page: Page,
  action: NavAction,
): Promise<{ ok: boolean; message: string }> {
  switch (action.type) {
    case 'click': {
      const text = (action.text ?? '').trim();
      if (!text) return { ok: false, message: 'click 缺少目标文案' };
      if (looksLikeFinalSubmit(text)) {
        return { ok: false, message: '拒绝点击疑似提交按钮的元素' };
      }
      const loc = await findClickableByText(page, text);
      if (!loc) return { ok: false, message: `未找到可点击元素: ${text}` };
      await loc.click({ timeout: 10_000 }).catch(() => undefined);
      await page.waitForLoadState('domcontentloaded', { timeout: 15_000 }).catch(() => undefined);
      // SPA 可能不会触发 load 事件，补一段等待让内容渲染。
      await page.waitForTimeout(800).catch(() => undefined);
      return { ok: true, message: `已点击: ${text}` };
    }
    case 'scroll': {
      const dir = action.direction === 'up' ? -1 : 1;
      const amount = Math.min(Math.max(action.amount ?? 600, 100), 5000);
      await page.mouse.wheel(0, dir * amount).catch(() => undefined);
      await page.waitForTimeout(400).catch(() => undefined);
      return { ok: true, message: `已滚动 ${dir > 0 ? '下' : '上'} ${amount}px` };
    }
    case 'navigate': {
      const url = action.url ?? '';
      if (!isSafeNavURL(url)) {
        return { ok: false, message: '目标 URL 不合法或指向非公网地址' };
      }
      await page
        .goto(url, { timeout: 20_000, waitUntil: 'domcontentloaded' })
        .catch(() => undefined);
      await page.waitForTimeout(800).catch(() => undefined);
      return { ok: true, message: `已导航到 ${url}` };
    }
    case 'wait': {
      const sec = Math.min(Math.max(action.seconds ?? 1, 0), 10);
      await page.waitForTimeout(sec * 1000).catch(() => undefined);
      return { ok: true, message: `已等待 ${sec}s` };
    }
    default:
      return { ok: false, message: `不支持的导航动作: ${action.type}` };
  }
}
