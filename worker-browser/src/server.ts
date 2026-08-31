/**
 * Playwright Worker HTTP 服务。
 *
 * 安全边界：
 *   1. 只监听 127.0.0.1，不对外暴露；
 *   2. 所有请求需携带与后端一致的 X-Worker-Token；
 *   3. 只提供四类能力：打开页面、读取表单结构、填写指定字段、读取当前页面正文（仅 innerText，只读）；
 *   4. 不提供执行任意选择器 / JS 的接口；
 *   5. 不存在任何点击提交按钮的代码路径；抓取 JD 亦不会触发任何导航或点击。
 */

import http from 'node:http';
import { timingSafeEqual } from 'node:crypto';
import {
  openSession,
  getSession,
  closeSession,
  closeAll,
  startIdleReaper,
} from './common/browser_manager.js';
import { extractFields, detectBlocked, countSensitive } from './common/field_extractor.js';
import { fillFields, highlightSubmitArea } from './common/field_filler.js';
import { getAdapter, type SiteAdapter } from './sites/adapters.js';
import type { Page } from 'playwright';
import { performNavAction } from './common/nav_act.js';
import { takeSnapshot } from './common/explore_act.js';
import { getNetworkObserver } from './common/browser_manager.js';
import { rankRequests } from './common/network_ranker.js';
import { redact } from './common/sensitive_guard.js';
import type {
  ExtractResponse,
  ExtractJobsResponse,
  FillRequest,
  FillResponse,
  NavActRequest,
  NavActResponse,
  ObserveDiffRequest,
  ObserveDiffResponse,
  ObserveStartResponse,
  OpenSessionRequest,
  OpenSessionResponse,
  ScrapeResponse,
  SnapshotResponse,
  StatusResponse,
} from './types.js';

const PORT = Number.parseInt(process.env.WORKER_PORT ?? '8390', 10);
const HOST = '127.0.0.1';
const TOKEN = process.env.BROWSER_WORKER_TOKEN ?? '';

/** 请求体大小上限：1 MiB。 */
const MAX_BODY_BYTES = 1 << 20;

/** 恒定时间比较 token，避免时序侧信道。 */
function tokenMatches(provided: string): boolean {
  // 未配置 token 时仅允许本机开发使用，并给出明确警告。
  if (!TOKEN) return true;
  const a = Buffer.from(provided);
  const b = Buffer.from(TOKEN);
  if (a.length !== b.length) return false;
  return timingSafeEqual(a, b);
}

/** 读取并解析 JSON 请求体。 */
async function readJSON(req: http.IncomingMessage): Promise<unknown> {
  return new Promise((resolve, reject) => {
    let size = 0;
    const chunks: Buffer[] = [];
    req.on('data', (chunk: Buffer) => {
      size += chunk.length;
      if (size > MAX_BODY_BYTES) {
        reject(new Error('请求体过大'));
        req.destroy();
        return;
      }
      chunks.push(chunk);
    });
    req.on('end', () => {
      if (chunks.length === 0) {
        resolve({});
        return;
      }
      try {
        resolve(JSON.parse(Buffer.concat(chunks).toString('utf8')));
      } catch {
        reject(new Error('请求体不是合法 JSON'));
      }
    });
    req.on('error', reject);
  });
}

function sendJSON(res: http.ServerResponse, status: number, body: unknown): void {
  const payload = JSON.stringify(body);
  res.writeHead(status, {
    'Content-Type': 'application/json; charset=utf-8',
    'Content-Length': Buffer.byteLength(payload),
    'X-Content-Type-Options': 'nosniff',
    'Cache-Control': 'no-store',
  });
  res.end(payload);
}

/** 登录态判定的最长等待时间。 */
const LOGIN_WAIT_MAX_MS = 20_000;

/** 登录态判定的轮询间隔。 */
const LOGIN_WAIT_INTERVAL_MS = 1_000;

/**
 * 轮询等待页面渲染出实质内容后再判定登录态。
 *
 * 为什么需要等待：
 *   openSession 只保证 domcontentloaded，SPA 正文要等 JS 执行完才出现。
 *   立即判定会因正文过短，把【公开可访问的岗位详情页】误判为登录墙。
 *
 * 为什么用轮询而不是固定等待：
 *   实测字节岗位页（campus/position/xxx/detail）正文出现时间不稳定，
 *   5 秒时可能仍只有 88 字符，6 秒后才到 1000+。固定等待无论取 2.5s 还是 6s
 *   都会在某些次命中临界点，表现为「时灵时不灵」。
 *   轮询能自适应：渲染好了立刻放行，慢的站点多等一会儿。
 *
 * 为什么要有上限：
 *   真实登录墙（跳转 login 页、出现密码框）再等也不会变化；
 *   无限等待只会拖死流程。超时后按未登录处理，给出明确提示。
 */
async function waitForLoginState(page: Page, adapter: SiteAdapter): Promise<boolean> {
  const started = Date.now();

  // 先立即判定一次：已登录态或同步渲染的页面可以马上放行。
  if (await adapter.isLoggedIn(page).catch(() => false)) {
    return true;
  }

  while (Date.now() - started < LOGIN_WAIT_MAX_MS) {
    await page.waitForTimeout(LOGIN_WAIT_INTERVAL_MS).catch(() => undefined);
    if (await adapter.isLoggedIn(page).catch(() => false)) {
      console.log(`[session/open] 等待 ${Date.now() - started}ms 后判定为可继续`);
      return true;
    }
  }

  console.log(`[session/open] 等待 ${Date.now() - started}ms 仍未渲染出内容，判定为需要登录`);
  return false;
}

/** 校验字符串型 task_id。 */
function requireTaskId(body: unknown): string {
  const obj = body as { task_id?: unknown };
  const id = typeof obj?.task_id === 'string' ? obj.task_id.trim() : '';
  // 只接受 UUID 格式，避免被用作路径或键的注入载体。
  if (!/^[0-9a-fA-F-]{36}$/.test(id)) {
    throw new Error('task_id 格式不合法');
  }
  return id;
}

/** POST /session/open */
async function handleOpenSession(body: unknown): Promise<OpenSessionResponse> {
  const taskId = requireTaskId(body);
  const req = body as OpenSessionRequest;
  const siteKey = typeof req.site_key === 'string' ? req.site_key : 'generic';
  const url = typeof req.url === 'string' ? req.url : '';
  if (!url) throw new Error('url 不能为空');

  const session = await openSession(taskId, siteKey, url);
  const adapter = getAdapter(session.siteKey);

  // 登录态判定必须等页面渲染出内容才有意义。
  //
  // 早期版本在 goto(domcontentloaded) 之后立即判定，此时 SPA 还没渲染，
  // 正文长度往往不足，导致【公开可访问的岗位详情页】（如字节 campus/position/xxx/detail）
  // 被误判为 needs_login，进而中断流程——用户甚至来不及登录就被关闭了会话。
  // 这里先等待渲染，首次判定未通过时再等一轮重试，兼顾慢站点与真实登录墙。
  const loggedIn = await waitForLoginState(session.page, adapter);
  session.step = loggedIn ? 'logged_in' : 'login_required';

  return {
    task_id: taskId,
    logged_in: loggedIn,
    needs_login: !loggedIn,
    current_url: session.page.url(),
    step: session.step,
    message: loggedIn
      ? '已复用登录态'
      : '请在浏览器中完成登录与验证，完成后回到系统点击继续',
  };
}

/** POST /form/extract */
async function handleExtract(body: unknown): Promise<ExtractResponse> {
  const taskId = requireTaskId(body);
  const session = getSession(taskId);
  if (!session || session.page.isClosed()) {
    throw new Error('会话不存在或浏览器已关闭');
  }

  const adapter = getAdapter(session.siteKey);
  if (adapter.prepareForm) {
    await adapter.prepareForm(session.page).catch(() => undefined);
  }

  const blocked = await detectBlocked(session.page).catch(() => false);
  if (blocked) {
    session.step = 'blocked';
    return {
      task_id: taskId,
      current_url: session.page.url(),
      fields: [],
      blocked: true,
      message: '页面存在验证码或风控限制，已停止自动化操作，请手动完成',
    };
  }

  const fields = await extractFields(session.page);
  session.step = 'extracted';

  // 日志只输出数量，不输出任何字段内容。
  console.log(
    `[extract] task=${taskId} 共 ${fields.length} 个可填字段，其中敏感 ${countSensitive(fields)} 个`,
  );

  return {
    task_id: taskId,
    current_url: session.page.url(),
    fields,
    blocked: false,
    message: `识别到 ${fields.length} 个表单字段`,
  };
}

/** POST /form/fill */
async function handleFill(body: unknown): Promise<FillResponse> {
  const taskId = requireTaskId(body);
  const req = body as FillRequest;
  const session = getSession(taskId);
  if (!session || session.page.isClosed()) {
    throw new Error('会话不存在或浏览器已关闭');
  }

  const items = Array.isArray(req.items) ? req.items : [];
  if (items.length > 300) {
    throw new Error('单次填写的字段数量超出上限');
  }

  const outcomes = await fillFields(session.page, items);
  const failedRefs = outcomes.filter((o) => !o.ok).map((o) => o.ref);
  const filled = outcomes.length - failedRefs.length;
  session.step = 'filled';

  console.log(`[fill] task=${taskId} 成功 ${filled} 项，失败 ${failedRefs.length} 项`);

  return {
    task_id: taskId,
    filled,
    failed: failedRefs.length,
    failed_refs: failedRefs,
    current_url: session.page.url(),
    message: `已填写 ${filled} 项`,
  };
}

/**
 * POST /form/highlight-submit
 *
 * 只滚动并高亮提交按钮，提醒用户自行检查与提交。
 * 本接口不会点击任何按钮。
 */
async function handleHighlight(body: unknown): Promise<{ found: boolean }> {
  const taskId = requireTaskId(body);
  const session = getSession(taskId);
  if (!session || session.page.isClosed()) {
    throw new Error('会话不存在或浏览器已关闭');
  }
  const found = await highlightSubmitArea(session.page).catch(() => false);
  session.step = 'awaiting_user_submit';
  return { found };
}

/** POST /session/status */
async function handleStatus(body: unknown): Promise<StatusResponse> {
  const taskId = requireTaskId(body);
  const session = getSession(taskId);
  if (!session || session.page.isClosed()) {
    throw new Error('会话不存在');
  }
  return {
    task_id: taskId,
    alive: true,
    current_url: session.page.url(),
    step: session.step,
  };
}

/** POST /session/close */
async function handleClose(body: unknown): Promise<{ closed: boolean }> {
  const taskId = requireTaskId(body);
  await closeSession(taskId);
  return { closed: true };
}

/**
 * POST /nav/act
 *
 * 执行后端 AI 决策出的单步导航动作（点击 / 滚动 / 导航 / 等待）。
 * 安全约束全部在 performNavAction 内部强制：不执行任意 JS、禁止点击提交按钮、
 * navigate 仅允许 http/https 公网地址。最终投递动作仍由用户亲自完成。
 */
async function handleNavAct(body: unknown): Promise<NavActResponse> {
  const taskId = requireTaskId(body);
  const req = body as NavActRequest;
  const session = getSession(taskId);
  if (!session || session.page.isClosed()) {
    throw new Error('会话不存在或浏览器已关闭');
  }
  const outcome = await performNavAction(session.page, req.action ?? {});
  session.step = 'navigating';
  return {
    task_id: taskId,
    ok: outcome.ok,
    current_url: session.page.url(),
    message: outcome.message,
  };
}

/**
 * POST /page/scrape
 *
 * 读取已登录浏览器当前可见页面的 JD 正文。
 * 这是只读操作：只提取 document.body.innerText，不导航、不点击、不执行任意脚本。
 * 若页面仍处于登录墙之后，则返回 needs_login，由用户先在浏览器中登录再继续。
 */
async function handleScrape(body: unknown): Promise<ScrapeResponse> {
  const taskId = requireTaskId(body);
  const session = getSession(taskId);
  if (!session || session.page.isClosed()) {
    throw new Error('会话不存在或浏览器已关闭');
  }
  const page = session.page;
  await page.waitForLoadState('domcontentloaded', { timeout: 20000 }).catch(() => undefined);

  const adapter = getAdapter(session.siteKey);
  const loggedIn = await adapter.isLoggedIn(page).catch(() => false);
  if (!loggedIn) {
    session.step = 'login_required';
    return {
      task_id: taskId,
      needs_login: true,
      current_url: page.url(),
      page_text: '',
      title: await page.title().catch(() => ''),
      message: '页面需要登录后才能查看 JD，请在浏览器中完成登录后点击「继续抓取」',
    };
  }

  const pageText = await page
    .evaluate(() => document.body.innerText ?? '')
    .catch(() => '');
  const title = await page.title().catch(() => '');
  session.step = 'scraped';
  console.log(`[scrape] task=${taskId} 抓取正文 ${pageText.length} 字符`);
  return {
    task_id: taskId,
    needs_login: false,
    current_url: page.url(),
    page_text: pageText.slice(0, 24000),
    title,
    message: 'ok',
  };
}

/** POST /page/extract-jobs */
async function handleExtractJobs(body: unknown): Promise<ExtractJobsResponse> {
  const taskId = requireTaskId(body);
  const session = getSession(taskId);
  if (!session || session.page.isClosed()) {
    throw new Error('会话不存在或浏览器已关闭');
  }
  const page = session.page;
  const keyword = typeof (body as { keyword?: unknown })?.keyword === 'string'
    ? (body as { keyword: string }).keyword.trim()
    : '';
  await page.waitForLoadState('domcontentloaded', { timeout: 20000 }).catch(() => undefined);

  const adapter = getAdapter(session.siteKey);
  const loggedIn = await adapter.isLoggedIn(page).catch(() => false);
  if (!loggedIn) {
    session.step = 'login_required';
    return {
      task_id: taskId,
      current_url: page.url(),
      jobs: [],
      count: 0,
      message: '页面需要登录后才能查看岗位，请在浏览器中完成登录后重试',
    };
  }

  if (!adapter.extractJobs) {
    return {
      task_id: taskId,
      current_url: page.url(),
      jobs: [],
      count: 0,
      message: '当前站点不支持结构化岗位抽取',
    };
  }

  const jobs = await adapter.extractJobs(page, keyword).catch(() => []);
  session.step = 'extracted_jobs';
  console.log(`[extract-jobs] task=${taskId} 抽取到 ${jobs.length} 个岗位卡片`);
  return {
    task_id: taskId,
    current_url: page.url(),
    jobs,
    count: jobs.length,
    message: `抽取到 ${jobs.length} 个岗位`,
  };
}

/**
 * POST /explore/observe-start
 *
 * 开始观测网络请求，返回当前游标作为后续 Diff 的基线。
 * 幂等：重复调用只是重置游标，不会重复绑定监听器。
 */
async function handleObserveStart(body: unknown): Promise<ObserveStartResponse> {
  const taskId = requireTaskId(body);
  const session = getSession(taskId);
  if (!session || session.page.isClosed()) {
    throw new Error('会话不存在或浏览器已关闭');
  }
  const obs = getNetworkObserver(session);
  // 重新开始时清空历史，避免上一步的残留干扰本次判断。
  obs.reset();
  session.step = 'observing';
  return {
    task_id: taskId,
    cursor: obs.cursor(),
    current_url: session.page.url(),
  };
}

/**
 * POST /explore/observe-diff
 *
 * 返回自 since 以来新增的网络请求，并给出规则打分后的候选。
 * 这是 Exploration Agent 判断「刚才那步操作是否触发了数据请求」的主要依据。
 */
async function handleObserveDiff(body: unknown): Promise<ObserveDiffResponse> {
  const taskId = requireTaskId(body);
  const req = body as ObserveDiffRequest;
  const session = getSession(taskId);
  if (!session || session.page.isClosed()) {
    throw new Error('会话不存在或浏览器已关闭');
  }
  const obs = getNetworkObserver(session);

  const since = typeof req.since === 'number' ? req.since : 0;
  const limit = typeof req.limit === 'number' && req.limit > 0 ? req.limit : 50;
  const topN = typeof req.top_n === 'number' && req.top_n > 0 ? req.top_n : 5;
  const rankedOnly = req.ranked_only !== false;

  const recent = obs.since(since);
  const sliced = recent.length > limit ? recent.slice(recent.length - limit) : recent;

  // 转为对外契约（字段名对齐 Go 侧 DTO）。
  const newRequests = sliced.map((r) => ({
    seq: r.seq,
    method: r.method,
    url: r.url,
    status: r.status,
    content_type: r.contentType,
    size: r.size,
    sample: r.sample,
    at: r.at,
  }));

  // 规则打分：只把高价值候选交给 LLM，省 token 也提高注意力。
  const ranked = rankRequests(sliced, topN).map((r) => ({
    record: {
      seq: r.request.seq,
      method: r.request.method,
      url: r.request.url,
      status: r.request.status,
      content_type: r.request.contentType,
      size: r.request.size,
      sample: r.request.sample,
      at: r.request.at,
    },
    score: r.score,
    reasons: r.reasons,
  }));

  console.log(
    `[observe-diff] task=${taskId} since=${since} 新增 ${sliced.length} 条，候选 ${ranked.length} 条`,
  );

  return {
    task_id: taskId,
    cursor: obs.cursor(),
    current_url: session.page.url(),
    new_requests: rankedOnly ? [] : newRequests,
    candidates: ranked,
  };
}

/**
 * POST /explore/snapshot
 *
 * 读取当前页面的可交互元素与正文片段。
 * 只读操作，不执行任何点击或脚本。
 */
async function handleSnapshot(body: unknown): Promise<SnapshotResponse> {
  const taskId = requireTaskId(body);
  const session = getSession(taskId);
  if (!session || session.page.isClosed()) {
    throw new Error('会话不存在或浏览器已关闭');
  }
  const snap = await takeSnapshot(session.page);
  session.step = 'snapshot';
  return {
    task_id: taskId,
    current_url: snap.currentUrl,
    title: snap.title,
    elements: snap.elements,
    text_sample: snap.textSample,
    card_samples: snap.cardSamples,
  };
}

/** 路由表。所有可用能力都在此显式列出。 */
const routes: Record<string, (body: unknown) => Promise<unknown>> = {
  '/session/open': handleOpenSession,
  '/session/status': handleStatus,
  '/session/close': handleClose,
  '/form/extract': handleExtract,
  '/form/fill': handleFill,
  '/form/highlight-submit': handleHighlight,
  '/page/scrape': handleScrape,
  '/page/extract-jobs': handleExtractJobs,
  '/nav/act': handleNavAct,
  // 探索能力：只读，供 Exploration Agent 感知页面与网络。
  '/explore/observe-start': handleObserveStart,
  '/explore/observe-diff': handleObserveDiff,
  '/explore/snapshot': handleSnapshot,
};

const server = http.createServer((req, res) => {
  void (async () => {
    const url = req.url ?? '/';

    if (req.method === 'GET' && url === '/health') {
      sendJSON(res, 200, { status: 'ok' });
      return;
    }

    // 根路径 GET：返回友好提示（用户可能直接在浏览器中打开）。
    if (req.method === 'GET' && url === '/') {
      sendJSON(res, 200, {
        service: 'Playwright Worker',
        version: '1.0',
        note: '这是后端 API 服务端，不是给人浏览的网页。请通过前端界面或 API 调用触发爬取任务，Worker 会自动弹出浏览器窗口。',
        endpoints: Object.keys(routes),
        health: '/health (GET)',
      });
      return;
    }

    if (req.method !== 'POST') {
      sendJSON(res, 405, { error: '只支持 POST' });
      return;
    }

    if (!tokenMatches(req.headers['x-worker-token']?.toString() ?? '')) {
      sendJSON(res, 401, { error: '认证失败' });
      return;
    }

    const handler = routes[url];
    if (!handler) {
      sendJSON(res, 404, { error: '接口不存在' });
      return;
    }

    try {
      const body = await readJSON(req);
      const result = await handler(body);
      sendJSON(res, 200, result);
    } catch (err) {
      // 错误信息脱敏后返回，避免泄漏页面内容。
      const msg = redact(err instanceof Error ? err.message : String(err)).slice(0, 200);
      console.warn(`[worker] ${url} 处理失败: ${msg}`);
      sendJSON(res, 200, { error: msg, blocked: false, fields: [] });
    }
  })();
});

const reaper = startIdleReaper();

server.listen(PORT, HOST, () => {
  console.log(`Playwright Worker 已启动: http://${HOST}:${PORT}`);
  if (!TOKEN) {
    console.warn('[worker] 未设置 BROWSER_WORKER_TOKEN，仅可用于本机开发环境');
  }
  console.log('[worker] 提示：本 Worker 不会点击任何提交按钮，最终提交须由你亲自完成');
});

/** 优雅退出：关闭全部浏览器会话。 */
async function shutdown(signal: string): Promise<void> {
  console.log(`[worker] 收到 ${signal}，正在关闭`);
  clearInterval(reaper);
  server.close();
  await closeAll();
  process.exit(0);
}

process.on('SIGINT', () => void shutdown('SIGINT'));
process.on('SIGTERM', () => void shutdown('SIGTERM'));
