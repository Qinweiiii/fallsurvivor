/**
 * 站点适配器。
 *
 * 不同招聘网站的登录判定与表单容器差异较大，
 * 因此按站点隔离，避免写出一个巨大的万能脚本。
 */

import type { Page, Response } from 'playwright';
import type { JobCard } from '../types.js';

/** 站点适配器接口。 */
export interface SiteAdapter {
  /** 站点标识。 */
  key: string;
  /** 站点名称。 */
  name: string;
  /** 判断当前页面是否已处于登录态。 */
  isLoggedIn(page: Page): Promise<boolean>;
  /** 进入申请表单页（若需要额外点击「立即申请」等入口）。 */
  prepareForm?(page: Page): Promise<void>;
  /**
   * 从当前列表页抽取结构化岗位卡片（半固定脚本使用）。
   *
   * keyword 为可选搜索关键词；非空时在站点搜索框输入并触发过滤，
   * 再抽取过滤后的结果。腾讯校招的列表由 Vue 异步接口 searchPosition 提供，
   * 关键词需写入该接口的查询参数，因此必须经由页面搜索框提交，而非拼 URL。
   */
  extractJobs?(page: Page, keyword?: string): Promise<JobCard[]>;
}

/**
 * 点击「进入申请表单」的入口按钮。
 *
 * 这是整个 Worker 中唯一执行点击的地方，因此做了严格约束：
 *
 *  1. 只接受调用方给出的固定入口文案，不接受任何外部传入的选择器；
 *  2. 点击前校验按钮文案必须与入口文案精确一致（去空白后），
 *     避免误点到「确认提交」「提交申请」这类最终提交按钮；
 *  3. 若页面上已经存在表单输入框，说明已在表单页，直接跳过点击。
 *
 * 最终提交按钮在任何情况下都不会被本函数点击。
 */
async function clickFormEntry(page: Page, entryTexts: string[]): Promise<void> {
  // 已经在表单页则无需再点。
  const hasForm = await page
    .evaluate(() => document.querySelectorAll('input, textarea, select').length > 3)
    .catch(() => false);
  if (hasForm) return;

  for (const text of entryTexts) {
    // 二次确认：入口文案本身不得命中提交类关键词。
    if (looksLikeFinalSubmit(text)) continue;

    const candidates = page.locator(`button, a[role="button"], a`);
    const count = Math.min(await candidates.count(), 200);

    for (let i = 0; i < count; i++) {
      const el = candidates.nth(i);
      const raw = (await el.innerText().catch(() => '')) || '';
      const label = raw.replace(/\s+/g, '');
      // 必须与入口文案完全一致，禁止包含匹配。
      if (label !== text) continue;

      await el.click({ timeout: 8000 }).catch(() => undefined);
      await page.waitForLoadState('domcontentloaded', { timeout: 15_000 }).catch(() => undefined);
      return;
    }
  }
}

/**
 * 判断文案是否属于最终提交类。
 *
 * 注意这里与「进入表单」的入口文案刻意区分：
 * 「立即申请」是入口，「提交申请」「确认提交」是终点。
 */
function looksLikeFinalSubmit(text: string): boolean {
  const t = text.replace(/\s+/g, '');
  const finalKeywords = ['提交', '投递', '确认', 'submit', 'confirm'];
  return finalKeywords.some((k) => t.toLowerCase().includes(k));
}

/** 通用登录判定：查找常见的「登录 / 注册」入口。 */
async function genericIsLoggedIn(page: Page): Promise<boolean> {
  return page.evaluate(() => {
    const text = document.body?.innerText ?? '';
    // 出现明显的登录入口，说明尚未登录。
    const loginSignals = ['登录', '登入', 'Sign in', 'Log in', '立即登录'];
    const hasLoginEntry = loginSignals.some((s) => text.includes(s));

    // 出现明显的已登录标志。
    const loggedSignals = ['退出登录', '我的简历', '个人中心', '退出', 'Sign out', 'Log out'];
    const hasLoggedMark = loggedSignals.some((s) => text.includes(s));

    if (hasLoggedMark) return true;
    // 存在密码输入框时几乎可以确定处于登录页。
    if (document.querySelector('input[type="password"]')) return false;
    return !hasLoginEntry;
  });
}

/** 通用适配器。 */
const genericAdapter: SiteAdapter = {
  key: 'generic',
  name: '通用站点',
  isLoggedIn: genericIsLoggedIn,
};

/** BOSS 直聘适配器。 */
const bossAdapter: SiteAdapter = {
  key: 'boss',
  name: 'BOSS直聘',
  async isLoggedIn(page: Page): Promise<boolean> {
    return page.evaluate(() => {
      const text = document.body?.innerText ?? '';
      if (text.includes('我的简历') || text.includes('在线简历')) return true;
      if (document.querySelector('.btn-login, .login-btn, [class*="login"]')) return false;
      return !text.includes('登录');
    });
  },
};

/**
 * 腾讯招聘适配器。
 *
 * 重要：腾讯招聘的岗位列表与详情页是公开的，浏览岗位**不需要登录**
 *（登录只在投递简历时才必要）。因此这里的判定不是「是否已登录」，
 * 而是「页面当前是否可继续浏览/抓取」——只要页面渲染出了实质内容、
 * 且没有被跳转到账号登录页，就允许爬虫继续。
 *
 * 早期版本直接以「个人中心 / 我的投递 / 退出」作为登录标志，
 * 导致未登录访问首页时（这三个词都不存在）被误判为 needs_login，
 * 爬虫在完全可公开浏览的页面上无谓中断。
 */
const tencentAdapter: SiteAdapter = {
  key: 'tencent',
  name: '腾讯招聘',
  async isLoggedIn(page: Page): Promise<boolean> {
    return page.evaluate(() => {
      const text = (document.body?.innerText ?? '').trim();

      // 1. 已登录的明确信号。
      if (text.includes('个人中心') || text.includes('我的投递') || text.includes('退出登录')) {
        return true;
      }

      // 2. 被跳转到账号登录页 / 登录墙，才真正需要用户先登录。
      const href = location.href.toLowerCase();
      if (/accounts\.tencent\.com|passport\.tencent\.com|login/i.test(href)) {
        return false;
      }
      // 页面几乎空白且提示登录，视为登录墙。
      if (text.length < 200 && (text.includes('登录') || text.includes('sign in'))) {
        return false;
      }

      // 3. 其余情况：只要渲染出了实质内容，就认为可继续浏览抓取。
      return text.length > 200;
    });
  },
  async prepareForm(page: Page): Promise<void> {
    await clickFormEntry(page, ['立即申请', '申请职位']);
  },
  /**
   * 腾讯校招（join.qq.com）列表页抽卡。
   *
   * 关键事实（网络层抓包确认）：列表数据由 Vue 通过接口
   *   GET https://join.qq.com/api/v1/position/searchPosition?timestamp=...
   * 异步拉取，响应形如：
   *   { "status":0, "data":{ "positionList":[
   *       { "positionTitle":"AI全栈工程师",
   *         "workCities":"深圳总部 北京 上海 ...",
   *         "bgs":"CDG CSIG IEG ...",
   *         "postId":"1282707398326592512", ... }, ... ] } }
   *
   * 卡片 DOM 本身【不】写入 postId（仅 onclick 跳转详情页），因此 DOM 抓取必然为 0。
   * 正确做法：直接捕获该接口响应解析 positionList，详情页 URL 拼为
   *   https://join.qq.com/post_detail.html?postid=<postId>
   *
   * keyword 可直接拼在列表页 URL 参数中（实测确认：
   *   post.html?query=p_1&keyword=agent → 搜索框自动填词、接口返回过滤结果），
   * 无需在搜索框里交互填词。
   */
  async extractJobs(page: Page, keyword?: string): Promise<JobCard[]> {
    // 构造带 keyword 的列表页 URL，直接触发过滤后的 searchPosition 接口。
    const kw = (keyword ?? '').trim();
    const current = page.url();
    let listUrl: string;
    if (/join\.qq\.com\/post\.html/i.test(current)) {
      // 在已有 URL 上追加/替换 keyword 参数。
      const u = new URL(current);
      if (kw) {
        u.searchParams.set('keyword', kw);
      } else {
        u.searchParams.delete('keyword');
      }
      listUrl = u.toString();
    } else {
      listUrl = kw
        ? `https://join.qq.com/post.html?query=p_1&keyword=${encodeURIComponent(kw)}`
        : 'https://join.qq.com/post.html?query=p_1';
    }

    // 监听「窗口内所有」searchPosition 响应并收集，再导航到列表页。
    // 关键竞态：SPA 加载时会先发一次【无 keyword】的 searchPosition（返回全量），
    // 读完 URL 上的 keyword 参数后才发第二次【过滤后】的请求。
    // 因此必须收集窗口内全部响应、等页面稳定后再取【最后一条】（即 keyword 过滤后的结果），
    // 而不是 waitForResponse 抓到的第一条（未过滤）。
    const collected: Response[] = [];
    const onResp = (r: Response) => {
      if (/join\.qq\.com\/api\/v1\/position\/searchPosition/i.test(r.url())) collected.push(r);
    };
    page.on('response', onResp);

    // 确保停在列表页（触发一次 searchPosition），随后等待 SPA 读取 keyword 参数并重发过滤请求。
    await page.goto(listUrl, { waitUntil: 'domcontentloaded', timeout: 30000 }).catch(() => undefined);
    // 先等到至少一次响应，确认接口已触发。
    await page.waitForResponse(
      (r) => /join\.qq\.com\/api\/v1\/position\/searchPosition/i.test(r.url()),
      { timeout: 15000 },
    ).catch(() => undefined);
    // 再等几秒，让 SPA 完成「全量 → 按 keyword 过滤」的二次请求。
    await page.waitForTimeout(4000);
    page.off('response', onResp);

    const resp = collected.length ? collected[collected.length - 1] : null;
    if (!resp) return [];
    const json = (await resp.json().catch(() => null)) as
      | { data?: { positionList?: Array<Record<string, unknown>> } }
      | null;
    const list = json?.data?.positionList ?? [];
    if (!Array.isArray(list) || list.length === 0) return [];

    // 对每条卡片，在页面上下文内调 jobDetails 接口获取完整信息：
    //   - desc / request（JD 正文，比 DOM innerText 干净）
    //   - intentionBGDList（结构化的事业群+部门，可组装成 "腾讯金融科技 - CDG"）
    // 接口路径：/api/v1/jobDetails?postId=<id>&timestamp=<ts>
    //
    // 关键：一个 postId 通常对应多个部门（如 AI全栈工程师 横跨 6 个事业群 34 个部门），
    // 按部门展开为多条独立 JobCard，每条带 department = "部门名 - 事业群"，
    // 落地后一个部门一行，不再把全部部门堆在单元格里。
    const cards: JobCard[] = [];
    for (const p of list) {
      const postId = String(p.postId ?? '').trim();
      const title = String(p.positionTitle ?? '').trim();
      if (!postId || !title) continue;

      // 在页面内同源 fetch jobDetails（携带浏览器 cookie/session）。
      // 真实接口路径（抓包确认）：/api/v1/jobDetails/getJobDetailsByPostId?timestamp=xxx&postId=xxx
      // 注意：不是 /api/v1/jobDetails（那个是 404）。
      const detail = await page
        .evaluate(
          async (pid) => {
            try {
              const r = await fetch(
                `/api/v1/jobDetails/getJobDetailsByPostId?timestamp=${Date.now()}&postId=${encodeURIComponent(pid)}`,
              );
              if (!r.ok) {
                return { __err: `jobDetails HTTP ${r.status}` };
              }
              const json = (await r.json().catch(() => null)) as Record<string, unknown> | null;
              return json ?? { __err: 'jobDetails empty body' };
            } catch (e) {
              return { __err: `jobDetails exception: ${(e as Error).message}` };
            }
          },
          postId,
        )
        .catch((e) => ({ __err: `jobDetails evaluate failed: ${(e as Error).message}` }));

      if (detail && (detail as Record<string, unknown>).__err) {
        // eslint-disable-next-line no-console
        console.warn(`[tencent] jobDetails 失败 (postId=${postId}):`, (detail as { __err: string }).__err);
      }

      const d = (detail as Record<string, unknown> | null)?.data as
        | Record<string, unknown>
        | undefined;

      // 扁平化「部门(事业群) | 部门(事业群) | ...」为单个字符串。
            // 一个 postId 通常对应多个部门（如 AI全栈工程师 横跨 6 个事业群 34 个部门），
            // 这里全部拼到同一个字符串里入库；前端表格在显示时按 | 换行。
            // 注意：用 `(BG)` 而非 `-BG`，避免与字段自身的「-」分隔符冲突；
            // 用 `|` 作为多条之间的分隔，前端按它换行。
            const deptBgPieces: string[] = [];
            if (Array.isArray(d?.intentionBGDList)) {
              for (const bg of d.intentionBGDList as Array<Record<string, unknown>>) {
                const showTitle = String(bg.showTitle ?? '').trim();
                const depts = bg.departmentList as Array<Record<string, unknown>> | undefined;
                if (Array.isArray(depts)) {
                  for (const dept of depts) {
                    const name = String(dept.name ?? '').trim();
                    if (name && showTitle) deptBgPieces.push(`${name}(${showTitle})`);
                  }
                }
              }
            }
            const departmentStr = deptBgPieces.join(' | ');

            // JD 文本（来自 jobDetails.desc / request，结构化且比 DOM 干净）。
            const desc = d?.desc ? String(d.desc).replace(/\s+/g, ' ').trim() : '';
            const req = d?.request ? String(d.request).replace(/\s+/g, ' ').trim() : '';
            const workCityList = d?.workCityList
              ? (d.workCityList as unknown[]).map((x) => String(x).trim()).filter(Boolean).join(', ')
            : String(p.workCities ?? '').trim();
            const projectName = String(p.projectName ?? '').trim();
            const recruitLabel = String(p.recruitLabelName ?? '').trim();

            // 部门列表的 JSON 表达（裁掉 comment 等长字段，避免 prompt 过长），
            // 仅作为上下文给 LLM 参考；它不应当被模型塞进 department/business。
            const intentionBGDBrief = Array.isArray(d?.intentionBGDList)
              ? (d.intentionBGDList as Array<Record<string, unknown>>).map((bg) => ({
                  showTitle: bg.showTitle,
                  showTxt: bg.showTxt,
                  departments: Array.isArray(bg.departmentList)
                    ? (bg.departmentList as Array<Record<string, unknown>>).map((dept) => ({
                        name: dept.name,
                        workCityList: dept.workCityList,
                      }))
                    : [],
                }))
              : [];

            const detailUrl = `https://join.qq.com/post_detail.html?postid=${encodeURIComponent(postId)}`;

            // 一个 postId 一条 JobCard（不再按部门裂开）。
            const sections: string[] = [
              `职位: ${title}`,
              `工作城市: ${workCityList}`,
              `届别: ${[projectName, recruitLabel].filter(Boolean).join(' / ')}`,
            ];
            if (intentionBGDBrief.length) {
              sections.push(`部门列表(JSON，供上下文参考，请勿从中挑一个填入 department/business): ${JSON.stringify(intentionBGDBrief)}`);
            }
            if (desc) sections.push(`岗位描述:\n${desc}`);
            if (req) sections.push(`岗位要求:\n${req}`);

            const card: JobCard = {
              url: detailUrl,
              title: title.slice(0, 200),
              raw_text: sections.join('\n'),
            };
            // department 字段：可能为空（jobDetails 失败兜底），前端按 | 换行。
            if (departmentStr) card.department = departmentStr;
            cards.push(card);
            if (cards.length >= 80) break;
          }
          return cards;
        },
      };

/**
 * 字节跳动招聘适配器。
 *
 * 与腾讯同理：岗位列表页是公开的，浏览/抓取岗位**不需要登录**
 * （登录只在投递时才必要）。因此 isLoggedIn 的语义是「页面是否可继续抓取」，
 * 而非「是否已登录」——只要没被跳转到账号登录页且渲染出了实质内容即可。
 *
 * 早期版本要求「我的投递/个人中心/退出登录」才放行，导致公开列表页被误判
 * 为 needs_login 而无谓中断，这里修正为与 tencentAdapter 一致的登录墙判定。
 */
const bytedanceAdapter: SiteAdapter = {
  key: 'bytedance',
  name: '字节跳动招聘',
  async isLoggedIn(page: Page): Promise<boolean> {
    return page.evaluate(() => {
      const text = (document.body?.innerText ?? '').trim();

      // 已登录的明确信号。
      if (text.includes('个人中心') || text.includes('我的投递') || text.includes('退出登录')) {
        return true;
      }

      // 被跳转到账号登录页 / 登录墙，才真正需要用户先登录。
      const href = location.href.toLowerCase();
      if (/login|passport|sso|account\.bytedance|signin/i.test(href)) {
        return false;
      }
      // 页面几乎空白且提示登录，视为登录墙。
      if (text.length < 200 && (text.includes('登录') || text.includes('sign in'))) {
        return false;
      }

      // 其余情况：只要渲染出了实质内容，就认为可继续浏览抓取。
      return text.length > 200;
    });
  },
  /**
   * 从列表页抽取结构化岗位卡片。
   *
   * 仅读取 DOM 中真实存在的岗位详情链接（href 命中字节官方详情页模式），
   * 不执行任何下发选择器。详情页 URL 形如：
   *   - 国内社招：https://jobs.bytedance.com/experienced/position/<id>
   *   - 国内校招：https://jobs.bytedance.com/campus/position/<id>
   *   - 国际站：https://job.bytedance.com/en/job/detail/<id>
   *
   * 抽取前会自动勾选「2027届校园招聘」筛选条件（若尚未勾选），
   * 确保抓到的是目标届别的岗位而非全部混合结果。
   */
  async extractJobs(page: Page): Promise<JobCard[]> {
    // 自动勾选「2027届校园招聘」筛选条件。
    // 字节校招的筛选是 Ant Design 树形组件：每个树节点用自定义 .atsx-tree-checkbox
    // （非原生 input，无 checked 属性，靠 className 含 'checked' 表示选中），
    // 点击该节点内的 .atsx-tree-checkbox 即切换选中。这是纯筛选操作，不涉及提交/投递。
    await page.evaluate(() => {
      // 叶子 treeitem = 不再包含子 treeitem 的 li[role=treeitem]。
      const leaves = Array.from(document.querySelectorAll('li[role="treeitem"]')).filter(
        (li) => !li.querySelector('li[role="treeitem"]'),
      );
      const target = leaves.find((li) => {
        const t = (li.textContent ?? '').replace(/\s+/g, ' ').trim();
        // 命中「2027届校园招聘」，但排除同级的兄弟项（前沿技术 / Seed / 实习）。
        return (
          t.includes('2027届校园招聘') &&
          !t.includes('前沿技术') &&
          !t.includes('Seed') &&
          !t.includes('实习')
        );
      });
      const cb = target?.querySelector('.atsx-tree-checkbox') as HTMLElement | null;
      if (cb && !cb.className.toString().includes('checked')) {
        cb.click();
      }
    }).catch(() => {});
    // 等待列表因筛选条件变更而刷新（字节校招列表是前端过滤，通常 <500ms）。
    await page.waitForTimeout(900).catch(() => {});

    const cards = await page.evaluate(() => {
      const out: Array<{ url: string; title: string; raw_text: string }> = [];
      const seen = new Set<string>();
      const anchors = Array.from(document.querySelectorAll('a[href]')) as HTMLAnchorElement[];
      const detailRe = /jobs?\.bytedance\.com\/(experienced\/position|campus\/position|en\/job\/detail)\/[^/?#]+/i;

      for (const a of anchors) {
        const href = a.href;
        if (!detailRe.test(href)) continue;
        if (seen.has(href)) continue;
        seen.add(href);

        const title = (a.innerText ?? '').replace(/\s+/g, ' ').trim().split('\n')[0];
        if (!title) continue;

        // 向上找容器，携带卡片可见文本作为地点 / 部门线索（详情抓取失败时回退用）。
        let node: HTMLElement | null = a;
        for (let i = 0; i < 4 && node; i++) node = node.parentElement;
        const rawText = (node?.innerText ?? a.innerText ?? '').replace(/\s+/g, ' ').trim();

        out.push({ url: href, title: title.slice(0, 200), raw_text: rawText });
        if (out.length >= 60) break;
      }
      return out;
    });

    return cards;
  },
};

const adapters = new Map<string, SiteAdapter>([
  [genericAdapter.key, genericAdapter],
  [bossAdapter.key, bossAdapter],
  [tencentAdapter.key, tencentAdapter],
  [bytedanceAdapter.key, bytedanceAdapter],
]);

/** 按站点标识获取适配器，未知站点回退到通用适配器。 */
export function getAdapter(siteKey: string): SiteAdapter {
  return adapters.get(siteKey) ?? genericAdapter;
}
