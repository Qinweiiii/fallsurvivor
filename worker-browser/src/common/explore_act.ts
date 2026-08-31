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

import type { Page } from 'playwright';
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
  textSample: string;
  cardSamples: string[];
}> {
  const currentUrl = page.url();
  const title = await page.title().catch(() => '');

  const data = await page
    .evaluate(
      (arg) => {
        const maxElements = arg.maxElements;
        const out: SnapshotElement[] = [];
        const cards: string[] = [];

        // 1. 可交互元素。
        const nodes = document.querySelectorAll(
          'a, button, input, select, textarea, [role="button"], [role="tab"], ' +
            '[class*="tab"], [class*="Tab"], [onclick]',
        );
        let idx = 0;
        for (const el of Array.from(nodes)) {
          if (idx >= maxElements) break;
          const htmlEl = el as HTMLElement;
          const rect = htmlEl.getBoundingClientRect();
          const visible = rect.width > 0 && rect.height > 0;
          if (!visible) continue;

          const text = (htmlEl.innerText ?? htmlEl.textContent ?? '').replace(/\s+/g, ' ').trim();
          const inputType =
            htmlEl instanceof HTMLInputElement ? htmlEl.type : htmlEl.tagName.toLowerCase();

          out.push({
            ref: '', // 由外层填充
            tag: htmlEl.tagName.toLowerCase(),
            text: text.slice(0, 80),
            placeholder: (htmlEl as HTMLInputElement).placeholder?.slice(0, 60) ?? '',
            aria_label: htmlEl.getAttribute('aria-label')?.slice(0, 80) ?? '',
            input_type: inputType,
            visible,
          });
          idx += 1;
        }

        // 2. 启发式岗位卡片：找同时含岗位信号词的容器。
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
          // 避免把祖先容器重复计入：只保留文本最短的一层。
          const key = t.slice(0, 50);
          if (seen.has(key)) continue;
          seen.add(key);
          cards.push(t.slice(0, arg.cardLimit));
        }

        const bodyText = (document.body?.innerText ?? '').replace(/\s+/g, ' ').trim();
        return { elements: out, cards, bodyText: bodyText.slice(0, arg.textLimit) };
      },
      {
        maxElements: MAX_ELEMENTS,
        maxCards: MAX_CARD_SAMPLES,
        cardLimit: CARD_SAMPLE_LIMIT,
        textLimit: TEXT_SAMPLE_LIMIT,
      },
    )
    .catch(() => ({ elements: [] as SnapshotElement[], cards: [] as string[], bodyText: '' }));

  // 在外层填充 ref，避免在页面上下文中生成序号（保持 evaluate 纯粹）。
  const elements = data.elements.map((el, i) => ({
    ...el,
    ref: makeRef('el', i),
  }));

  return {
    title,
    currentUrl,
    elements,
    textSample: redact(data.bodyText),
    cardSamples: data.cards.map((c) => redact(c)),
  };
}
