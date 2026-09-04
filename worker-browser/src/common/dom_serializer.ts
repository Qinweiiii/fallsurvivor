/**
 * DOM 序列化：把渲染后的页面转成适合 LLM 阅读的 Markdown。
 *
 * 设计来源与取舍（对标 browser-use 的 extract_structured_data）：
 *   browser-use 的关键洞察是——**不要逆向接口，读渲染后的结果**。
 *   它把 DOM 转成 Markdown 交给 LLM 二次阅读，而不是用规则解析器。
 *   代价是慢一些、耗 token，但对任意站点通用，尤其是重度 SPA。
 *
 * 为什么本项目必须补上这条路：
 *   原先只有「观测网络请求 → 脱离浏览器复现接口」一条路。
 *   遇到快手这类站点就会卡住：列表接口带鉴权/上下文参数，
 *   脱离浏览器复现会返回 code=40014，**但页面本身无需登录就渲染出了全部岗位**。
 *   绕远路去逆向接口，反而丢掉了唾手可得的数据。
 *
 * 同时纠正一个具体错误：
 *   旧的 takeSnapshot 用硬编码信号词（'工作地点'/'岗位职责'…）猜岗位卡片。
 *   实测在快手上它抓出的 3 条「卡片」全是筛选器面板
 *   （面板里恰好也有「工作地点」字样），真岗位一条都没识别出来。
 *   本模块不再猜「哪个容器是岗位」，只负责如实转出可读正文，
 *   把「哪些是岗位」的判断交给 LLM——它比关键词表可靠得多。
 *
 * 安全约束（继承既有边界）：
 *   1. 只读：不修改页面、不执行站点脚本；
 *   2. 内容脱敏：输出前统一经 redact 处理；
 *   3. 有界：字符数与深度都有上限，避免上下文爆炸或爆栈。
 */

import type { Page } from 'playwright';
import { redact } from './sensitive_guard.js';

/** Markdown 正文的最大字符数。 */
const MARKDOWN_LIMIT = 24_000;

/**
 * 单个文本节点的最大字符数。
 *
 * 超长文本节点通常是被压平的整段页面（如内联 JSON 或未分段的说明），
 * 保留全文对识别岗位无益，只会挤占上下文。
 */
const NODE_TEXT_LIMIT = 600;

/**
 * 需要整棵子树丢弃的标签。
 *
 * 这些要么不可见（script/style/template），要么与岗位无关。
 * 丢弃它们能显著提高信噪比。
 */
const DROP_TAGS = 'script,style,noscript,template,svg,canvas,iframe,link,meta,head';

/**
 * 构建浏览器端序列化脚本（**字符串形式的 IIFE**）。
 *
 * 为什么不用 `page.evaluate(() => {...})` 传函数：
 *   本项目用 tsx/esbuild 运行 TypeScript，编译器会给源码中的具名函数
 *   注入 `__name(...)` 之类 helper 以保留函数名。这些 helper 只存在于
 *   Node 侧模块作用域，序列化到浏览器后就会
 *   `ReferenceError: __name is not defined`。
 *
 * 为什么必须是 IIFE、且参数内联而非通过 evaluate 传参：
 *   Playwright 传字符串时**按表达式求值，不会当函数调用**。
 *   实测 `page.evaluate('(arg) => arg.a + arg.b', {a:2,b:3})` 返回
 *   `undefined`——因为表达式的值就是那个函数对象本身，第二个参数被忽略。
 *   这正是早先三版实现都产出空字符串的根因。
 *   写成 `(function(){ ... })()` 才会真正执行并返回结果。
 *
 * 参数内联的安全性：
 *   内联的只有本模块定义的数值/标签常量，不含任何外部输入，
 *   因此不存在把用户数据拼进脚本的路径。
 */
function buildSerializeScript(opts: {
  dropTags: string;
  nodeTextLimit: number;
  maxDepth: number;
}): string {
  // JSON.stringify 保证常量以合法字面量嵌入，不会破坏脚本结构。
  const dropTags = JSON.stringify(opts.dropTags);
  const nodeLimit = JSON.stringify(opts.nodeTextLimit);
  const maxDepth = JSON.stringify(opts.maxDepth);

  return `
(function () {
  var dropTags = new Set(${dropTags}.split(','));
  var nodeLimit = ${nodeLimit};
  var maxDepth = ${maxDepth};
  var out = [];

  var visible = function (el) {
    var style = window.getComputedStyle(el);
    // 只信任这三种「明确隐藏」信号。
    //
    // 刻意不用 getBoundingClientRect 判尺寸：容器为了做滚动/动画
    // 常自身零尺寸而子节点完全可见，按尺寸剪枝会把整棵子树误杀。
    return !(style.display === 'none' || style.visibility === 'hidden' || style.opacity === '0');
  };

  var norm = function (s) {
    var t = (s == null ? '' : String(s)).replace(/\\s+/g, ' ').trim();
    return t.length > nodeLimit ? t.slice(0, nodeLimit) : t;
  };

  // 去重策略：只跳过与上一行**完全相同**的内容。
  //
  // 刻意不做「被上一行包含就跳过」这类更激进的去重：
  // 岗位卡片里标题常与属性同处一个容器，激进去重会把标题整行吞掉
  // ——实测就出现过「只剩 全职/算法类/北京/日期，标题不见了」。
  // 宁可留少量重复（LLM 能容忍），也不能丢关键字段。
  var push = function (line) {
    var t = String(line).trim();
    if (!t) return;
    if (out.length > 0 && out[out.length - 1] === t) return;
    out.push(t);
  };

  var HEADINGS = { h1: 1, h2: 2, h3: 3, h4: 4, h5: 5, h6: 6 };
  var BLOCKS = { div: 1, section: 1, article: 1, ul: 1, ol: 1, table: 1, p: 1 };

  // 用显式栈迭代而非递归：行为确定，且不会因 DOM 过深爆栈。
  var stack = [];
  if (document.body) stack.push({ node: document.body, depth: 0 });

  while (stack.length > 0) {
    var frame = stack.pop();
    var node = frame.node;
    var depth = frame.depth;
    if (depth > maxDepth) continue;

    if (node.nodeType === 3) { // TEXT_NODE
      push(norm(node.textContent));
      continue;
    }
    if (node.nodeType !== 1) continue; // 只处理元素节点

    var el = node;
    var tag = el.tagName.toLowerCase();
    if (dropTags.has(tag)) continue;
    if (!visible(el)) continue;

    // 标题：保留层级，内部不再展开以避免重复。
    if (HEADINGS[tag]) {
      push(new Array(HEADINGS[tag] + 1).join('#') + ' ' + norm(el.innerText));
      continue;
    }
    if (tag === 'a') {
      var text = norm(el.innerText);
      var href = el.getAttribute('href') || '';
      // 保留 href：岗位详情链接常含 ID，是后续拼详情页 URL 的关键线索。
      if (text) push(href ? '[' + text + '](' + href + ')' : text);
      continue;
    }
    if (tag === 'button') {
      var btn = norm(el.innerText);
      if (btn) push('<button>' + btn + '</button>');
      continue;
    }
    if (tag === 'li') {
      var li = norm(el.innerText);
      if (li) push('- ' + li);
      continue;
    }
    if (tag === 'tr') {
      var cells = [];
      for (var i = 0; i < el.children.length; i++) {
        var c = norm(el.children[i].innerText);
        if (c) cells.push(c);
      }
      if (cells.length) push('| ' + cells.join(' | ') + ' |');
      continue;
    }
    if (tag === 'br') continue;
    if (tag === 'img') {
      var alt = norm(el.getAttribute('alt'));
      if (alt) push('![' + alt + ']');
      continue;
    }
    if (tag === 'input' || tag === 'select' || tag === 'textarea') {
      var label = norm(el.placeholder || el.getAttribute('aria-label'));
      if (label) push('<input placeholder="' + label + '">');
      continue;
    }

    // 块级元素之间插入空行，帮助 LLM 区分「一条岗位」的边界。
    if (BLOCKS[tag] && out.length > 0 && out[out.length - 1] !== '') {
      out.push('');
    }

    // 栈是后进先出：倒序入栈才能保持文档顺序输出。
    var kids = el.childNodes;
    for (var k = kids.length - 1; k >= 0; k--) {
      stack.push({ node: kids[k], depth: depth + 1 });
    }
  }

  return out.join('\\n');
})()
`;
}

/**
 * 遍历深度上限。
 *
 * 只用于防御异常 DOM（如自引用结构导致爆栈），取值必须足够宽松：
 * 现代 SPA 从 body 到岗位卡片文本常有 30-60 层包装
 * （框架容器 + 布局 div + 组件嵌套）。早先设为 40 时快手页面
 * 被整体截断成空输出——上限过紧的代价是「静默丢全部内容」。
 */
const MAX_DEPTH = 200;

/**
 * serializeDOM 把当前页面正文转成 Markdown。
 *
 * 保留的语义：标题层级、列表、链接（含 href）、表格行、按钮文案。
 * 这些恰好是岗位列表页承载信息的主要结构——
 * 岗位标题常是链接或标题元素，属性（城市/类型/日期）常是相邻文本或列表项。
 */
export async function serializeDOM(page: Page): Promise<{
  markdown: string;
  currentUrl: string;
  title: string;
  truncated: boolean;
}> {
  const currentUrl = page.url();
  const title = await page.title().catch(() => '');

  // 刻意不吞异常：序列化失败必须让调用方看到原因。
  // 早先版本用 .catch(() => '') 静默返回空串，表现为「Markdown 长度 0」，
  // 完全看不出是脚本报错还是页面没内容——排查成本远高于让错误冒出来。
  let raw: string;
  try {
    raw = (await page.evaluate(
      buildSerializeScript({
        dropTags: DROP_TAGS,
        nodeTextLimit: NODE_TEXT_LIMIT,
        maxDepth: MAX_DEPTH,
      }),
    )) as string;
  } catch (err) {
    throw new Error(`DOM 序列化失败: ${err instanceof Error ? err.message : String(err)}`);
  }

  // 压缩连续空行：多余空行只是噪声。
  const compact = (raw ?? '').replace(/\n{3,}/g, '\n\n').trim();
  const truncated = compact.length > MARKDOWN_LIMIT;

  return {
    markdown: redact(truncated ? compact.slice(0, MARKDOWN_LIMIT) : compact),
    currentUrl,
    title,
    truncated,
  };
}
