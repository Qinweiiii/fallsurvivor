/**
 * 安全导航动作执行器。
 *
 * 设计原则（与既有表单填写一致的安全边界）：
 *   1. 绝不执行任意 JS / 任意选择器；
 *   2. click_ref 只点击当前探索快照里的元素句柄；
 *   3. navigate 只接受 http/https 公网地址，禁止指向本机或内网。
 */

import type { ElementHandle, Locator, Page } from 'playwright';
import { getExploreRef, type Session } from './browser_manager.js';
import type { NavAction } from '../types.js';

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

async function findSearchInput(page: Page): Promise<Locator | null> {
  const selector =
    'input:not([type]), input[type="text"], input[type="search"], textarea, ' +
    '[contenteditable="true"]';
  const candidates = page.locator(selector);
  const count = Math.min(await candidates.count(), 80);

  let fallback: Locator | null = null;
  for (let i = 0; i < count; i++) {
    const el = candidates.nth(i);
    if (!(await el.isVisible().catch(() => false))) continue;
    const meta = await el
      .evaluate((node) => {
        const html = node as HTMLElement;
        const input = node as HTMLInputElement;
        return [
          input.placeholder ?? '',
          html.getAttribute('aria-label') ?? '',
          html.getAttribute('name') ?? '',
          html.getAttribute('id') ?? '',
          html.className ? String(html.className) : '',
        ].join(' ');
      })
      .catch(() => '');
    if (/搜索|关键词|岗位|职位|search|keyword|position|job/i.test(meta)) {
      return el;
    }
    if (!fallback) fallback = el;
  }
  return fallback;
}

async function submitSearch(
  page: Page,
  input: ElementHandle<HTMLElement>,
  keyword: string,
  target: string,
): Promise<{ ok: boolean; message: string }> {
  try {
    await input.click({ timeout: 5_000 });
    await page.keyboard.press(process.platform === 'darwin' ? 'Meta+A' : 'Control+A');
    await page.keyboard.press('Backspace');
    await page.keyboard.type(keyword, { delay: 30 });
  } catch {
    return { ok: false, message: `搜索框输入失败: ${target}` };
  }
  const inputValue = await input
    .evaluate((node) => {
      const field = node as HTMLInputElement;
      return field.value ?? node.textContent ?? '';
    })
    .catch(() => '');
  if (normalize(inputValue) !== normalize(keyword)) {
    return { ok: false, message: `搜索关键词未写入输入框: ${target}` };
  }
  const enterSent = await input.press('Enter').then(() => true).catch(() => false);
  await page.waitForLoadState('domcontentloaded', { timeout: 10_000 }).catch(() => undefined);
  await page.waitForTimeout(1_500).catch(() => undefined);
  if (!enterSent) {
    return { ok: false, message: `关键词已输入，但未能发送回车: ${target}` };
  }
  return {
    ok: true,
    message: `已向 ${target} 输入关键词并发送回车；是否提交成功由后续页面观测确认`,
  };
}

/**
 * 按可见文案定位可点击元素。
 * 在按钮 / 链接 / 标签页 / 列表项范围内匹配，优先精确匹配，
 * 其次子串包含匹配。
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

    let score = 0;
    if (label === norm) score = 100;
    else if (label.includes(norm)) score = 80 - (label.length - norm.length);
    else if (norm.includes(label) && label.length >= 2) score = 60 - (norm.length - label.length);
    if (score > 0 && (!best || score > best.score)) best = { loc: el, score };
  }
  return best ? best.loc : null;
}

function isEditableElement(tag: string, inputType: string, editable: string | null): boolean {
  if (editable === 'true') return true;
  if (tag === 'textarea') return true;
  if (tag !== 'input') return false;
  return !['button', 'submit', 'reset', 'checkbox', 'radio', 'file', 'hidden'].includes(inputType);
}

async function describeHandle(handle: ElementHandle<HTMLElement>): Promise<{
  tag: string;
  label: string;
  inputType: string;
  contentEditable: string | null;
}> {
  return handle.evaluate((node) => {
    const el = node as HTMLElement;
    const input = node as HTMLInputElement;
    const tag = el.tagName.toLowerCase();
    const text = (el.innerText ?? el.textContent ?? '').replace(/\s+/g, ' ').trim();
    return {
      tag,
      label: text || input.placeholder || el.getAttribute('aria-label') || el.getAttribute('title') || tag,
      inputType: tag === 'input' ? input.type : tag,
      contentEditable: el.getAttribute('contenteditable'),
    };
  });
}

async function clickHandle(page: Page, handle: ElementHandle<HTMLElement>, ref: string): Promise<{ ok: boolean; message: string }> {
  const desc = await describeHandle(handle);
  await handle.scrollIntoViewIfNeeded().catch(() => undefined);
  await handle.click({ timeout: 10_000 }).catch(async () => {
    const box = await handle.boundingBox().catch(() => null);
    if (!box) throw new Error('元素不可点击或已失效');
    await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2);
  });
  await page.waitForLoadState('domcontentloaded', { timeout: 15_000 }).catch(() => undefined);
  await page.waitForTimeout(800).catch(() => undefined);
  return { ok: true, message: `已点击 ${ref}: ${desc.label.slice(0, 80)}` };
}

async function inputHandle(
  page: Page,
  handle: ElementHandle<HTMLElement>,
  ref: string,
  text: string,
): Promise<{ ok: boolean; message: string }> {
  const desc = await describeHandle(handle);
  if (!isEditableElement(desc.tag, desc.inputType, desc.contentEditable)) {
    return { ok: false, message: `${ref} 不是可输入元素` };
  }
  await handle.scrollIntoViewIfNeeded().catch(() => undefined);
  await handle.click({ timeout: 5_000 }).catch(() => undefined);
  await page.keyboard.press(process.platform === 'darwin' ? 'Meta+A' : 'Control+A').catch(() => undefined);
  await page.keyboard.type(text, { delay: 20 }).catch(() => undefined);
  await page.waitForTimeout(300).catch(() => undefined);
  return { ok: true, message: `已向 ${ref} 输入: ${text}` };
}

/** 执行单个导航动作，返回是否成功及说明。 */
export async function performNavAction(
  session: Session,
  action: NavAction,
): Promise<{ ok: boolean; message: string }> {
  const page = session.page;
  switch (action.type) {
    case 'click_ref': {
      const ref = (action.ref ?? '').trim();
      if (!ref) return { ok: false, message: 'click_ref 缺少 ref' };
      const handle = getExploreRef(session, ref);
      if (!handle) return { ok: false, message: `ref 不存在或已过期: ${ref}` };
      return clickHandle(page, handle, ref);
    }
    case 'input_ref': {
      const ref = (action.ref ?? '').trim();
      const text = (action.text ?? action.keyword ?? '').trim();
      if (!ref) return { ok: false, message: 'input_ref 缺少 ref' };
      if (!text) return { ok: false, message: 'input_ref 缺少输入文本' };
      const handle = getExploreRef(session, ref);
      if (!handle) return { ok: false, message: `ref 不存在或已过期: ${ref}` };
      return inputHandle(page, handle, ref, text);
    }
    case 'click': {
      const text = (action.text ?? '').trim();
      if (!text) return { ok: false, message: 'click 缺少目标文案' };
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
    case 'search': {
      const keyword = (action.keyword ?? '').trim();
      if (!keyword) return { ok: false, message: 'search 缺少关键词' };
      const ref = (action.ref ?? '').trim();
      if (ref) {
        const handle = getExploreRef(session, ref);
        if (!handle) return { ok: false, message: `搜索框 ref 不存在或已过期: ${ref}` };
        const desc = await describeHandle(handle);
        if (!isEditableElement(desc.tag, desc.inputType, desc.contentEditable)) {
          return { ok: false, message: `${ref} 不是可搜索的输入元素` };
        }
        return submitSearch(page, handle, keyword, `${ref}: ${desc.label.slice(0, 80)}`);
      }
      // 已保存的旧 Browser Plan 没有瞬时 DOM ref；仅为兼容该路径保留定位回退。
      const input = await findSearchInput(page);
      if (!input) return { ok: false, message: '未找到可见搜索框' };
      const handle = (await input.elementHandle()) as ElementHandle<HTMLElement> | null;
      if (!handle) return { ok: false, message: '兼容定位的搜索框已失效' };
      return submitSearch(page, handle, keyword, '兼容定位的搜索框');
    }
    case 'back': {
      const response = await page.goBack({ timeout: 20_000, waitUntil: 'domcontentloaded' }).catch(() => null);
      if (!response) return { ok: false, message: '浏览器没有可返回的上一页' };
      await page.waitForTimeout(1_000).catch(() => undefined);
      return { ok: true, message: '已返回上一页' };
    }
    default:
      return { ok: false, message: `不支持的导航动作: ${action.type}` };
  }
}
