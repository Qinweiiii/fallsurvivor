/**
 * 站点适配器。
 *
 * 设计约束：除 BOSS 直聘（列表页会先展示空路由再重定向到登录页，结构特殊）外，
 * 所有站点统一走通用适配器。通用适配器只按「页面是否渲染出实质内容、是否落在登录墙」
 * 判定能否继续抓取，不按站点写任何特化逻辑。新增站点只需要在后端配 Recipe，
 * 不需要在这里加 adapter。
 */

import type { Page } from 'playwright';
import type { JobCard } from '../types.js';

/** 站点适配器接口。 */
export interface SiteAdapter {
  /** 站点标识。 */
  key: string;
  /** 站点名称。 */
  name: string;
  /** 判断当前页面是否可继续访问；公开页面不要求已登录。 */
  isLoggedIn(page: Page): Promise<boolean>;
  /** 进入申请表单页（若需要额外点击「立即申请」等入口）。 */
  prepareForm?(page: Page): Promise<void>;
  /**
   * 从当前列表页抽取结构化岗位卡片（半固定脚本使用）。
   *
   * keyword 为可选搜索关键词；非空时在站点搜索框输入并触发过滤，
   * 再抽取过滤后的结果。通用站点若未实现，则由上层按 browser_plan 重放处理。
   */
  extractJobs?(page: Page, keyword?: string): Promise<JobCard[]>;
}

/**
 * 通用访问判定。
 *
 * 招聘站点的导航栏通常会一直展示「登录」入口，不能据此推断页面需要登录。
 * 只有跳转到认证路由、展示可见密码框，或页面明确要求登录且没有实质内容时，
 * 才阻断后续抓取。
 */
async function genericIsLoggedIn(page: Page): Promise<boolean> {
  return page.evaluate(() => {
    const text = (document.body?.innerText ?? '').trim();
    const href = location.href.toLowerCase();
    if (/(?:^|[/?#])(login|signin|sign-in|passport|auth)(?:[/?#]|$)/.test(href)) {
      return false;
    }

    const hasVisiblePasswordInput = Array.from(document.querySelectorAll<HTMLInputElement>('input[type="password"]'))
      .some((input) => {
        const style = getComputedStyle(input);
        return style.display !== 'none' && style.visibility !== 'hidden' && input.getClientRects().length > 0;
      });
    if (hasVisiblePasswordInput) return false;

    const loginWallSignals = ['请先登录', '登录后查看', '登录后继续', '登录后可查看', '请登录后继续'];
    const hasLoginWall = loginWallSignals.some((signal) => text.includes(signal));
    if (hasLoginWall && text.length < 300) return false;

    // 公开职位列表、详情和首页都有实质正文时可继续探索；导航栏中的登录按钮不影响判断。
    return text.length > 200;
  });
}

/** 通用适配器。 */
const genericAdapter: SiteAdapter = {
  key: 'generic',
  name: '通用站点',
  isLoggedIn: genericIsLoggedIn,
};

/**
 * BOSS 直聘适配器。
 *
 * BOSS 的列表页会先短暂展示空的列表路由，再由站点脚本重定向至登录页，
 * 因此单独处理：只有岗位列表已实际渲染，才把无显式登录控件视为可访问。
 */
const bossAdapter: SiteAdapter = {
  key: 'boss',
  name: 'BOSS直聘',
  async isLoggedIn(page: Page): Promise<boolean> {
    return page.evaluate(() => {
      const text = (document.body?.innerText ?? '').trim();
      const href = location.href.toLowerCase();
      if (/\/web\/user\/|passport|login/.test(href)) return false;
      if (text.includes('我的简历') || text.includes('在线简历')) return true;
      if (document.querySelector('input[type="password"], .btn-login, .login-btn, [class*="login"]')) return false;

      // BOSS 会先短暂展示空的列表路由，再由站点脚本重定向至登录页。
      // 只有岗位列表已实际渲染，才把无显式登录控件视为可访问。
      const hasJobList = Boolean(
        document.querySelector('[class*="job-card"], [class*="job-list"], a[href*="job_detail"]'),
      );
      return text.length > 300 && hasJobList;
    });
  },
};

const adapters = new Map<string, SiteAdapter>([
  [genericAdapter.key, genericAdapter],
  [bossAdapter.key, bossAdapter],
]);

/** 按站点标识获取适配器，未知站点回退到通用适配器。 */
export function getAdapter(siteKey: string): SiteAdapter {
  return adapters.get(siteKey) ?? genericAdapter;
}
