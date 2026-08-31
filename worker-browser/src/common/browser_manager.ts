/**
 * 浏览器生命周期管理。
 *
 * 关键设计：
 *   - 使用 persistent context，让登录态（Cookie / localStorage）留在本地目录，
 *     用户只需第一次手动登录，之后自动复用；
 *   - headless 固定为 false：用户必须能看到页面、自己处理验证码与最终提交；
 *   - 绝不禁用任何浏览器沙箱相关参数；
 *   - 登录态目录权限 0700，且已被 .gitignore 排除。
 */

import { chromium, type BrowserContext, type Page } from 'playwright';
import { mkdir } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { NetworkObserver } from './network_observer.js';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

/** 登录态根目录，位于 worker-browser/.auth 下。 */
const AUTH_ROOT = path.resolve(__dirname, '../../.auth');

/** 单个会话。 */
export interface Session {
  taskId: string;
  siteKey: string;
  context: BrowserContext;
  page: Page;
  step: string;
  createdAt: number;
  /**
   * 网络请求观测器，供 Exploration Agent 发现站点的数据接口。
   * 惰性创建：只有探索流程用到时才 attach，普通爬取不产生额外开销。
   */
  network?: NetworkObserver;
}

/** 获取（必要时创建并绑定）会话的网络观测器。 */
export function getNetworkObserver(session: Session): NetworkObserver {
  if (!session.network) {
    const obs = new NetworkObserver();
    obs.attach(session.page);
    session.network = obs;
  }
  return session.network;
}

/** 会话空闲超时：2 小时后自动回收，避免浏览器长期挂着。 */
const SESSION_IDLE_MS = 2 * 60 * 60 * 1000;

const sessions = new Map<string, Session>();

/**
 * 校验 siteKey 只能是安全的标识符。
 * 这是防路径穿越的关键：siteKey 会成为目录名的一部分。
 */
function assertSafeSiteKey(siteKey: string): string {
  const key = (siteKey || 'generic').trim();
  if (!/^[a-z0-9_-]{1,32}$/.test(key)) {
    throw new Error('非法的 site_key');
  }
  return key;
}

/** 返回某站点的登录态目录（已做路径边界校验）。 */
async function authDirFor(siteKey: string): Promise<string> {
  const key = assertSafeSiteKey(siteKey);
  const dir = path.join(AUTH_ROOT, key);
  const resolved = path.resolve(dir);
  // 边界校验：必须位于 AUTH_ROOT 之内。
  if (!resolved.startsWith(AUTH_ROOT + path.sep)) {
    throw new Error('登录态目录越界');
  }
  await mkdir(resolved, { recursive: true, mode: 0o700 });
  return resolved;
}

/** 允许打开的协议。 */
const ALLOWED_PROTOCOLS = new Set(['http:', 'https:']);

/**
 * 校验待打开的 URL。
 *
 * 只允许 http/https，且拒绝指向本机与内网的地址，
 * 防止通过投递地址诱导 Worker 访问内部服务。
 */
export function assertSafeURL(rawURL: string): URL {
  let u: URL;
  try {
    u = new URL(rawURL);
  } catch {
    throw new Error('URL 格式不合法');
  }
  if (!ALLOWED_PROTOCOLS.has(u.protocol)) {
    throw new Error('只允许 http/https 协议');
  }

  const host = u.hostname.toLowerCase();
  const blockedHosts = ['localhost', '127.0.0.1', '0.0.0.0', '::1', '[::1]', 'metadata.google.internal'];
  if (blockedHosts.includes(host)) {
    throw new Error('禁止访问本机地址');
  }
  // 拒绝私有网段的字面量 IP。
  if (
    /^10\./.test(host) ||
    /^192\.168\./.test(host) ||
    /^172\.(1[6-9]|2\d|3[01])\./.test(host) ||
    /^169\.254\./.test(host) ||
    /^127\./.test(host)
  ) {
    throw new Error('禁止访问内网地址');
  }
  return u;
}

/** 获取已存在的会话。 */
export function getSession(taskId: string): Session | undefined {
  return sessions.get(taskId);
}

/**
 * 打开或复用会话。
 *
 * 同一个 taskId 重复调用时会复用已有浏览器窗口，
 * 这样用户手动登录后点击「继续」不会丢失登录状态。
 */
export async function openSession(
  taskId: string,
  siteKey: string,
  targetURL: string,
): Promise<Session> {
  const url = assertSafeURL(targetURL);

  const existing = sessions.get(taskId);
  if (existing && !existing.page.isClosed()) {
    // 若当前页面不是目标页，则导航过去。
    if (existing.page.url() !== url.toString()) {
      await existing.page.goto(url.toString(), { waitUntil: 'domcontentloaded', timeout: 60_000 });
    }
    return existing;
  }

  const userDataDir = await authDirFor(siteKey);

  // 注意：不传入任何 --no-sandbox / --disable-web-security 之类的参数，
  // 浏览器安全机制保持默认开启。
  const context = await chromium.launchPersistentContext(userDataDir, {
    headless: false,
    viewport: { width: 1440, height: 900 },
    locale: 'zh-CN',
    timezoneId: 'Asia/Shanghai',
    acceptDownloads: false,
    args: ['--start-maximized'],
  });

  const page = context.pages()[0] ?? (await context.newPage());
  page.setDefaultTimeout(30_000);

  await page.goto(url.toString(), { waitUntil: 'domcontentloaded', timeout: 60_000 });

  const session: Session = {
    taskId,
    siteKey: assertSafeSiteKey(siteKey),
    context,
    page,
    step: 'opened',
    createdAt: Date.now(),
  };
  sessions.set(taskId, session);
  return session;
}

/** 关闭会话。 */
export async function closeSession(taskId: string): Promise<void> {
  const s = sessions.get(taskId);
  if (!s) return;
  sessions.delete(taskId);
  try {
    await s.context.close();
  } catch {
    // 用户可能已手动关闭浏览器，忽略。
  }
}

/** 关闭全部会话，用于进程退出。 */
export async function closeAll(): Promise<void> {
  await Promise.all([...sessions.keys()].map((id) => closeSession(id)));
}

/** 回收空闲超时的会话。 */
export function startIdleReaper(): NodeJS.Timeout {
  return setInterval(() => {
    const now = Date.now();
    for (const [id, s] of sessions) {
      if (now - s.createdAt > SESSION_IDLE_MS) {
        void closeSession(id);
      }
    }
  }, 10 * 60 * 1000);
}
