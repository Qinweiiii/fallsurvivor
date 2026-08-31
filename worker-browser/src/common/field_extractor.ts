/**
 * 表单字段提取。
 *
 * 只读取字段的结构与标签信息，绝不读取已有的值，
 * 也不会把页面上的任何用户数据回传给后端或模型。
 */

import type { Page } from 'playwright';
import type { FormField } from '../types.js';
import { isSensitiveText, isSensitiveInputType } from './sensitive_guard.js';

/** 需要跳过的 input type：这些不是可填写的文本字段。 */
const IGNORED_TYPES = new Set(['hidden', 'submit', 'button', 'reset', 'image', 'file']);

/**
 * 提取当前页面的表单字段。
 *
 * ref 由 Worker 生成，形如 "f12"，并写入 DOM 的 data 属性。
 * 后端只能通过 ref 指定要填写的字段，无法传入任意选择器，
 * 从而避免「后端被诱导操作任意页面元素」的风险。
 */
export async function extractFields(page: Page): Promise<FormField[]> {
  const raw = await page.evaluate(() => {
    /** 属性名：用于给元素打上稳定句柄。 */
    const REF_ATTR = 'data-jobos-ref';

    /** 从多种来源推断字段标签。 */
    function findLabel(el: Element): string {
      const input = el as HTMLElement;

      // 1. aria-label
      const aria = input.getAttribute('aria-label');
      if (aria && aria.trim()) return aria.trim();

      // 2. <label for="id">
      const id = input.getAttribute('id');
      if (id) {
        // 使用 CSS.escape 防止 id 中的特殊字符破坏选择器。
        const escaped = typeof CSS !== 'undefined' && CSS.escape ? CSS.escape(id) : id;
        try {
          const lbl = document.querySelector(`label[for="${escaped}"]`);
          if (lbl?.textContent?.trim()) return lbl.textContent.trim();
        } catch {
          // 选择器异常时忽略，继续用其他方式推断。
        }
      }

      // 3. 祖先 <label>
      const parentLabel = input.closest('label');
      if (parentLabel?.textContent?.trim()) {
        return parentLabel.textContent.trim().slice(0, 100);
      }

      // 4. 同一表单项容器内的标题文本
      const wrapper = input.closest(
        '.form-item, .form-group, .field, .ant-form-item, .el-form-item, [class*="form-item"]',
      );
      if (wrapper) {
        const labelNode = wrapper.querySelector('label, .label, .form-label, [class*="label"]');
        if (labelNode?.textContent?.trim()) {
          return labelNode.textContent.trim().slice(0, 100);
        }
      }

      // 5. 前一个兄弟节点的文本
      const prev = input.previousElementSibling;
      if (prev?.textContent?.trim()) {
        return prev.textContent.trim().slice(0, 100);
      }

      return '';
    }

    const nodes = Array.from(
      document.querySelectorAll<HTMLElement>('input, textarea, select'),
    );

    let counter = 0;
    const out: Array<{
      ref: string;
      name: string;
      label: string;
      placeholder: string;
      type: string;
      required: boolean;
      options: string[];
      maxLength: number;
      visible: boolean;
    }> = [];

    for (const el of nodes) {
      const tag = el.tagName.toLowerCase();
      const type =
        tag === 'input'
          ? ((el as HTMLInputElement).type || 'text').toLowerCase()
          : tag; // textarea / select

      // 可见性判断：不可见字段不参与填写。
      const style = window.getComputedStyle(el);
      const rect = el.getBoundingClientRect();
      const visible =
        style.display !== 'none' &&
        style.visibility !== 'hidden' &&
        Number(style.opacity) > 0.01 &&
        rect.width > 0 &&
        rect.height > 0;

      const ref = `f${counter++}`;
      el.setAttribute(REF_ATTR, ref);

      let options: string[] = [];
      if (tag === 'select') {
        options = Array.from((el as HTMLSelectElement).options)
          .map((o) => o.textContent?.trim() ?? '')
          .filter((s) => s.length > 0)
          .slice(0, 50);
      }

      const maxLengthAttr = el.getAttribute('maxlength');
      const maxLength = maxLengthAttr ? Number.parseInt(maxLengthAttr, 10) : 0;

      out.push({
        ref,
        name: el.getAttribute('name') ?? '',
        label: findLabel(el),
        placeholder: el.getAttribute('placeholder') ?? '',
        type,
        required:
          el.hasAttribute('required') || el.getAttribute('aria-required') === 'true',
        options,
        maxLength: Number.isFinite(maxLength) && maxLength > 0 ? maxLength : 0,
        visible,
      });
    }
    return out;
  });

  const fields: FormField[] = [];
  for (const f of raw) {
    if (!f.visible) continue;
    if (IGNORED_TYPES.has(f.type)) continue;
    // 完全没有任何标识的字段无法安全映射，直接忽略。
    if (!f.label && !f.name && !f.placeholder) continue;

    const field: FormField = {
      ref: f.ref,
      name: f.name,
      label: f.label,
      placeholder: f.placeholder,
      type: f.type,
      required: f.required,
    };
    if (f.options.length > 0) field.options = f.options;
    if (f.maxLength > 0) field.max_length = f.maxLength;
    fields.push(field);
  }
  return fields;
}

/**
 * 统计页面上的敏感字段数量，用于日志与提示。
 * 不返回任何字段值。
 */
export function countSensitive(fields: FormField[]): number {
  return fields.filter(
    (f) =>
      isSensitiveInputType(f.type) ||
      isSensitiveText(f.label, f.name, f.placeholder),
  ).length;
}

/**
 * 检测页面是否存在明显的自动化限制。
 *
 * 命中时 Worker 会停止操作并交回用户处理，
 * 绝不尝试绕过验证码或风控。
 */
export async function detectBlocked(page: Page): Promise<boolean> {
  return page.evaluate(() => {
    const text = document.body?.innerText?.toLowerCase() ?? '';
    const signals = [
      '请完成验证',
      '安全验证',
      '滑动验证',
      '人机验证',
      '访问受限',
      '请求过于频繁',
      'access denied',
      'are you a robot',
      'verify you are human',
      'unusual traffic',
      'cloudflare',
      'captcha',
    ];
    if (signals.some((s) => text.includes(s))) return true;

    // 常见验证码组件容器。
    const selectors = [
      'iframe[src*="recaptcha"]',
      'iframe[src*="hcaptcha"]',
      'iframe[title*="challenge"]',
      '.geetest_panel',
      '#nc_1_wrapper',
      '[class*="captcha"]',
    ];
    return selectors.some((s) => document.querySelector(s) !== null);
  });
}
