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

import { chromium, type BrowserContext, type ElementHandle, type Page } from 'playwright';
import { mkdir, rm } from 'node:fs/promises';
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
  /**
   * 最近一次探索快照里的可交互元素句柄。
   *
   * 这是 browser-use selector_map 的轻量版：LLM 看到 ref，Worker 用 ref
   * 找回当时快照中的真实元素，避免重新按文案扫描页面而点错同名控件。
   */
  exploreRefs?: Map<string, ElementHandle<HTMLElement>>;
}

/** 刷新探索元素句柄，并释放上一轮快照的句柄避免泄漏。 */
export async function setExploreRefs(
  session: Session,
  refs: Map<string, ElementHandle<HTMLElement>>,
): Promise<void> {
  const previous = session.exploreRefs;
  session.exploreRefs = refs;
  if (!previous) return;
  await Promise.all(
    [...previous.values()].map(async (handle) => {
      try {
        await handle.dispose();
      } catch {
        // 元素可能已经随页面导航失效，忽略即可。
      }
    }),
  );
}

/** 读取最近一次快照中的元素句柄。 */
export function getExploreRef(session: Session, ref: string): ElementHandle<HTMLElement> | undefined {
  return session.exploreRefs?.get(ref);
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
  // 不用 as const：Playwright 的 args 参数要求可变数组，
  // 只读元组会导致类型不兼容。
  const launchOptions = {
    headless: false,
    viewport: { width: 1440, height: 900 },
    locale: 'zh-CN',
    timezoneId: 'Asia/Shanghai',
    acceptDownloads: false,
    args: ['--start-maximized'],
  };

  let context;
  try {
    context = await chromium.launchPersistentContext(userDataDir, launchOptions);
  } catch (err) {
    // 启动失败最常见的原因是 user-data-dir 仍被上一次的残留进程独占
    // （报 "Target page, context or browser has been closed"）。
    //
    // 这种失败是**永久性**的：只要残留进程不退出，该站点后续每次都失败。
    // 因此这里主动清掉该 profile 的锁文件再重试一次，
    // 而不是把问题抛给调用方——否则表现就是「某个站点莫名再也打不开」。
    const msg = err instanceof Error ? err.message : String(err);
    await releaseProfileLocks(userDataDir);
    try {
      context = await chromium.launchPersistentContext(userDataDir, launchOptions);
    } catch {
      throw new Error(
        `浏览器启动失败（已尝试清理 profile 锁后重试）: ${msg}。` +
          `若持续失败，请检查是否有残留的 Chromium 进程占用 ${userDataDir}`,
      );
    }
  }

  const page = context.pages()[0] ?? (await context.newPage());
  page.setDefaultTimeout(30_000);

  // 某些风控站点会在首跳期间中断原导航，再脚本重定向到登录/验证页。
  // 这时 Playwright 可能抛出 ERR_ABORTED；若页面实际已离开 about:blank，
  // 应把它作为可继续的会话交给上层登录态判定，而不是留下孤立 Chromium。
  try {
    await page.goto(url.toString(), { waitUntil: 'domcontentloaded', timeout: 60_000 });
  } catch (err) {
    if (page.url() === 'about:blank') {
      await context.close().catch(() => undefined);
      throw err;
    }
  }

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

/**
 * 清理 Chromium profile 的独占锁文件。
 *
 * Chromium 用 SingletonLock / SingletonCookie / SingletonSocket 三个文件
 * 标记「该 user-data-dir 正被占用」。进程被强制中断时这些文件会残留，
 * 导致后续启动一直失败。
 *
 * 只删这三个已知的锁文件，**不触碰目录内其他任何内容**——
 * 登录态（Cookies / Local Storage）都在同一目录下，误删会导致用户
 * 需要重新登录每个站点。
 */
async function releaseProfileLocks(userDataDir: string): Promise<void> {
  const lockNames = ['SingletonLock', 'SingletonCookie', 'SingletonSocket'];
  await Promise.all(
    lockNames.map(async (name) => {
      try {
        await rm(path.join(userDataDir, name), { force: true });
      } catch {
        // 不存在或无权限时忽略：这只是尽力而为的清理。
      }
    }),
  );
}

/**
 * 关闭会话。
 *
 * 必须同时关掉 context 与其底层 browser：
 *   launchPersistentContext 启动的 Chromium，在只调用 context.close()
 *   时进程有时不会退出（尤其上一次是被强制中断的会话）。残留进程会一直
 *   持有 .auth/<siteKey> 这个 user-data-dir 的独占锁，导致该站点后续
 *   再也打不开会话，报 "Target page, context or browser has been closed"。
 *
 *   实测这会累积到 8 个以上残留进程，且表现为「某个站点突然永久失败」，
 *   很难联想到是上一次会话没清干净——所以这里要显式收尾，
 *   而不是依赖调用方手动清理。
 */
export async function closeSession(taskId: string): Promise<void> {
  const s = sessions.get(taskId);
  if (!s) return;
  sessions.delete(taskId);

  // 先取底层 browser 引用：context 关闭后就取不到了。
  const browser = s.context.browser();

  if (s.exploreRefs) {
    await Promise.all(
      [...s.exploreRefs.values()].map(async (handle) => {
        try {
          await handle.dispose();
        } catch {
          // 页面关闭时句柄可能已失效。
        }
      }),
    );
    s.exploreRefs = undefined;
  }

  try {
    await s.context.close();
  } catch {
    // 用户可能已手动关闭浏览器，忽略。
  }

  // 再确保浏览器进程本身退出，释放 user-data-dir 锁。
  if (browser) {
    try {
      await browser.close();
    } catch {
      // 已退出则忽略。
    }
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
