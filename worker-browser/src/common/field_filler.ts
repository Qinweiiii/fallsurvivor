/**
 * 表单填写。
 *
 * 安全约束：
 *   1. 只能通过 extractFields 生成的 ref 定位元素，
 *      后端无法传入任意 CSS 选择器或 JS 代码；
 *   2. 填写前对字段再做一次敏感性判定，命中即跳过；
 *   3. 绝不点击任何提交类按钮。
 */

import type { Page } from 'playwright';
import type { FillItem } from '../types.js';
import { isFillableField, looksLikeSubmit, redact } from './sensitive_guard.js';

/** ref 的属性名，与 field_extractor 保持一致。 */
const REF_ATTR = 'data-jobos-ref';

/** ref 只允许字母数字，防止被用来注入选择器。 */
function isValidRef(ref: string): boolean {
  return /^f\d{1,5}$/.test(ref);
}

/** 单个字段的填写结果。 */
export interface FillOutcome {
  ref: string;
  ok: boolean;
  reason?: string;
}

/**
 * 批量填写字段。
 *
 * 逐个处理，单个失败不影响其他字段。
 */
export async function fillFields(page: Page, items: FillItem[]): Promise<FillOutcome[]> {
  const outcomes: FillOutcome[] = [];

  for (const item of items) {
    if (!isValidRef(item.ref)) {
      outcomes.push({ ref: item.ref, ok: false, reason: 'invalid_ref' });
      continue;
    }
    if (typeof item.value !== 'string' || item.value.length === 0) {
      outcomes.push({ ref: item.ref, ok: false, reason: 'empty_value' });
      continue;
    }
    // 单字段值长度上限，防止异常长文本卡住页面。
    if (item.value.length > 5000) {
      outcomes.push({ ref: item.ref, ok: false, reason: 'value_too_long' });
      continue;
    }

    try {
      const outcome = await fillOne(page, item);
      outcomes.push(outcome);
    } catch (err) {
      // 日志脱敏，绝不输出字段值。
      console.warn(`[fill] 字段 ${item.ref} 填写失败: ${redact(String(err)).slice(0, 200)}`);
      outcomes.push({ ref: item.ref, ok: false, reason: 'exception' });
    }
  }
  return outcomes;
}

/** 填写单个字段。 */
async function fillOne(page: Page, item: FillItem): Promise<FillOutcome> {
  const locator = page.locator(`[${REF_ATTR}="${item.ref}"]`);
  const count = await locator.count();
  if (count === 0) {
    return { ref: item.ref, ok: false, reason: 'not_found' };
  }

  const el = locator.first();

  // 再次读取字段元信息并做敏感性判定：最后一道防线。
  const meta = await el.evaluate((node) => {
    const e = node as HTMLElement;
    const tag = e.tagName.toLowerCase();
    return {
      tag,
      type: tag === 'input' ? ((e as HTMLInputElement).type || 'text').toLowerCase() : tag,
      name: e.getAttribute('name') ?? '',
      placeholder: e.getAttribute('placeholder') ?? '',
      ariaLabel: e.getAttribute('aria-label') ?? '',
      disabled: (e as HTMLInputElement).disabled === true,
      readOnly: (e as HTMLInputElement).readOnly === true,
      isSubmitLike:
        tag === 'button' ||
        (tag === 'input' && ['submit', 'button', 'image'].includes(((e as HTMLInputElement).type || '').toLowerCase())),
    };
  });

  if (meta.isSubmitLike) {
    // 绝不操作任何按钮类元素。
    return { ref: item.ref, ok: false, reason: 'refused_button' };
  }
  if (meta.disabled || meta.readOnly) {
    return { ref: item.ref, ok: false, reason: 'not_editable' };
  }
  if (!isFillableField({ name: meta.name, placeholder: meta.placeholder, label: meta.ariaLabel, type: meta.type })) {
    return { ref: item.ref, ok: false, reason: 'sensitive' };
  }
  if (looksLikeSubmit(meta.ariaLabel)) {
    return { ref: item.ref, ok: false, reason: 'refused_submit_like' };
  }

  await el.scrollIntoViewIfNeeded({ timeout: 5000 });

  switch (meta.tag) {
    case 'select': {
      // 下拉框优先按可见文本匹配，失败则按 value。
      try {
        await el.selectOption({ label: item.value }, { timeout: 5000 });
      } catch {
        try {
          await el.selectOption(item.value, { timeout: 5000 });
        } catch {
          return { ref: item.ref, ok: false, reason: 'option_not_found' };
        }
      }
      return { ref: item.ref, ok: true };
    }

    case 'textarea':
    case 'input': {
      if (meta.type === 'checkbox' || meta.type === 'radio') {
        // 勾选类字段涉及协议同意等语义，交由用户确认，不自动操作。
        return { ref: item.ref, ok: false, reason: 'refused_choice_field' };
      }
      await el.fill(item.value, { timeout: 8000 });
      // 触发框架的变更监听（Vue / React 受控组件）。
      await el.dispatchEvent('input');
      await el.dispatchEvent('change');
      return { ref: item.ref, ok: true };
    }

    default:
      return { ref: item.ref, ok: false, reason: 'unsupported_element' };
  }
}

/**
 * 滚动到提交按钮附近并加上高亮，提醒用户检查后自行提交。
 *
 * 本函数只做滚动与视觉标记，**不执行任何点击**。
 * 这是产品的硬性边界：最终提交必须由用户亲自完成。
 */
export async function highlightSubmitArea(page: Page): Promise<boolean> {
  return page.evaluate(() => {
    const keywords = ['提交', '投递', '申请', 'submit', 'apply'];
    const candidates = Array.from(
      document.querySelectorAll<HTMLElement>('button, input[type="submit"], a[role="button"]'),
    );

    const target = candidates.find((el) => {
      const text = (el.innerText || (el as HTMLInputElement).value || '').toLowerCase();
      return keywords.some((k) => text.includes(k));
    });

    if (!target) return false;

    target.scrollIntoView({ behavior: 'smooth', block: 'center' });
    // 只加视觉提示，不改变任何行为，也不绑定任何事件。
    target.style.outline = '3px solid #f0a5c0';
    target.style.outlineOffset = '4px';
    return true;
  });
}
