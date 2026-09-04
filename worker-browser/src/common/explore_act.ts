/**
 * 探索动作执行器：为 Exploration Agent 提供「读页面 + 读网络」的能力。
 *
 * 与 nav_act.ts 的关系：
 *   - nav_act 负责「写」（点击/滚动/导航），已具备完整安全约束；
 *   - 本模块负责「读」（页面快照 / 网络请求），是只读操作。
 *   两者配合构成 Agent 的感知-行动闭环。
 *
 * 安全约束（严格继承既有边界）：
 *   1. 只读：绝不修改页面，不执行任意 JS；
 *   2. 元素句柄 opaque：对外只暴露 ref，内部结构不泄漏；
 *   3. 内容脱敏：网络响应片段与页面文本都经脱敏与截断；
 *   4. 有界：元素与请求条数都有上限，避免上下文爆炸。
 */

import type { ElementHandle, Page } from 'playwright';
import type { SnapshotElement } from '../types.js';
import { redact } from './sensitive_guard.js';

/** 快照最多返回的元素数。 */
const MAX_ELEMENTS = 80;

/** 页面文本片段的最大字符数。 */
const TEXT_SAMPLE_LIMIT = 1500;

/** 岗位卡片样本的最大条数。 */
const MAX_CARD_SAMPLES = 5;

/** 单个卡片样本的最大字符数。 */
const CARD_SAMPLE_LIMIT = 300;

/** 生成不透明句柄。用序号即可——句柄只在本次会话内有效，无需全局唯一。 */
function makeRef(prefix: string, index: number): string {
  return `${prefix}:${index}`;
}

/** 可见性判断：有尺寸且未被隐藏。 */
function isVisible(rect: { width: number; height: number }): boolean {
  return rect.width > 0 && rect.height > 0;
}

/**
 * 读取当前页面的可交互元素快照。
 *
 * 返回给 LLM 的信息刻意保持精简：只有 文案 / 标签 / 类型 / 句柄，
 * 不含 DOM 结构或选择器——避免 LLM 试图构造选择器（安全边界）。
 */
export async function takeSnapshot(page: Page): Promise<{
  title: string;
  currentUrl: string;
  elements: SnapshotElement[];
  refs: Map<string, ElementHandle<HTMLElement>>;
  textSample: string;
  cardSamples: string[];
}> {
  const currentUrl = page.url();
  const title = await page.title().catch(() => '');

  const elements: SnapshotElement[] = [];
  const refs = new Map<string, ElementHandle<HTMLElement>>();
  const selector =
    'a, button, input, select, textarea, [contenteditable="true"], ' +
    '[role="button"], [role="tab"], [role="link"], [role="combobox"], [onclick]';

  const handles = await page.$$(selector).catch(() => []);
  for (const rawHandle of handles) {
    const handle = rawHandle as ElementHandle<HTMLElement>;
    if (elements.length >= MAX_ELEMENTS) {
      await handle.dispose().catch(() => undefined);
      continue;
    }
    const meta = await handle
      .evaluate((node: HTMLElement) => {
        const htmlEl = node;
        const rect = htmlEl.getBoundingClientRect();
        const style = window.getComputedStyle(htmlEl);
        const visible =
          rect.width > 0 &&
          rect.height > 0 &&
          style.display !== 'none' &&
          style.visibility !== 'hidden' &&
          style.opacity !== '0';
        if (!visible) return null;

        const input = node as HTMLInputElement;
        const text = (htmlEl.innerText ?? htmlEl.textContent ?? '').replace(/\s+/g, ' ').trim();
        const tag = htmlEl.tagName.toLowerCase();
        const role = htmlEl.getAttribute('role') ?? '';
        const inputType = tag === 'input' ? input.type : tag;
		const href = htmlEl instanceof HTMLAnchorElement ? htmlEl.href : '';
        return {
          tag,
          role,
          text: text.slice(0, 120),
          placeholder: (input.placeholder ?? '').slice(0, 80),
          aria_label: (htmlEl.getAttribute('aria-label') ?? '').slice(0, 120),
          input_type: inputType,
		  href,
          visible: true,
        };
      })
      .catch(() => null);

    if (!meta) {
      await handle.dispose().catch(() => undefined);
      continue;
    }
    const label = meta.text || meta.placeholder || meta.aria_label || meta.role;
    if (!label) {
      await handle.dispose().catch(() => undefined);
      continue;
    }

    const ref = makeRef('el', elements.length + 1);
    elements.push({ ref, ...meta });
    refs.set(ref, handle);
  }

  const data = await page
    .evaluate(
      (arg) => {
        const cards: string[] = [];
        const signals = ['工作地点', '招聘范围', '岗位职责', '岗位类别', 'Base地', '工作城市', '招聘人数', '发布日期'];
        const all = Array.from(document.querySelectorAll('*'));
        const seen = new Set<string>();
        for (const el of all) {
          if (cards.length >= arg.maxCards) break;
          const htmlEl = el as HTMLElement;
          const t = (htmlEl.innerText ?? '').replace(/\s+/g, ' ').trim();
          if (t.length < 20 || t.length > 600) continue;
          const hitCount = signals.filter((s) => t.includes(s)).length;
          if (hitCount < 1) continue;
          const key = t.slice(0, 50);
          if (seen.has(key)) continue;
          seen.add(key);
          cards.push(t.slice(0, arg.cardLimit));
        }

        const bodyText = (document.body?.innerText ?? '').replace(/\s+/g, ' ').trim();
        return { cards, bodyText: bodyText.slice(0, arg.textLimit) };
      },
      {
        maxCards: MAX_CARD_SAMPLES,
        cardLimit: CARD_SAMPLE_LIMIT,
        textLimit: TEXT_SAMPLE_LIMIT,
      },
    )
    .catch(() => ({ cards: [] as string[], bodyText: '' }));

  return {
    title,
    currentUrl,
    elements,
    refs,
    textSample: redact(data.bodyText),
    cardSamples: data.cards.map((c) => redact(c)),
  };
}
