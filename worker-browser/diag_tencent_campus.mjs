import { chromium } from 'playwright';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// 诊断：腾讯校招官网 join.qq.com 的岗位列表页结构。
// 已知：页面是 Vue SPA（data-v-* / #app），岗位列表由 XHR 异步拉取后渲染。
// 真实详情页 URL 形如：post_detail.html?postid=1282707398326592512
// 本脚本做两件事：
//   1) 监听网络响应，直接抓 Vue 拉列表的 API（里面就有 postid），这是最稳的；
//   2) 等列表渲染完，dump 右侧列表区真实 HTML，确认卡片 class 与跳转方式。
//
// 用法（先停掉 make bw，否则锁住 .auth/tencent）：
//   node worker-browser/diag_tencent_campus.mjs
//   TX_CAMPUS_URL='https://join.qq.com/post.html?query=p_1' node worker-browser/diag_tencent_campus.mjs
const BASE = process.env.TX_CAMPUS_URL || 'https://join.qq.com/post.html?query=p_1';
const AUTH_DIR = path.resolve(__dirname, '.auth', 'tencent');

let ctx;
try {
  ctx = await chromium.launchPersistentContext(AUTH_DIR, {
    headless: true,
    viewport: { width: 1440, height: 900 },
    locale: 'zh-CN',
    timezoneId: 'Asia/Shanghai',
  });
} catch (e) {
  console.error('启动浏览器失败（多半是 make bw 还在跑、锁住了 .auth/tencent）：', e.message);
  process.exit(1);
}
const page = ctx.pages()[0] ?? (await ctx.newPage());

// 1) 监听网络响应，抓列表 API
const apiHits = [];
page.on('response', async (resp) => {
  const u = resp.url();
  if (!/join\.qq\.com/i.test(u)) return;
  const ct = resp.headers()['content-type'] ?? '';
  if (!/json|javascript|text/i.test(ct)) return;
  // 只关心疑似「列表/招聘」接口
  if (!/post|recruit|job|campus|list|query|search/i.test(u)) return;
  try {
    const body = await resp.text();
    if (body.includes('postid') || body.includes('postId') || /post_detail/.test(body)) {
      apiHits.push({ url: u, len: body.length, snippet: body.slice(0, 800) });
    }
  } catch {
    /* ignore */
  }
});

await page.goto(BASE, { waitUntil: 'networkidle', timeout: 60000 }).catch(() => {});
// SPA 可能还会再发请求，多等一会儿
await page.waitForTimeout(6000);
for (let i = 0; i < 3; i++) {
  await page.mouse.wheel(0, 1200).catch(() => {});
  await page.waitForTimeout(800);
}

console.log('=== 标题 ===');
console.log(await page.title());

console.log('\n=== 网络层：含 postid / post_detail 的 API 响应 ===');
if (apiHits.length === 0) {
  console.log('(未捕获到含 postid 的接口)');
} else {
  for (const h of apiHits.slice(0, 5)) {
    console.log('URL:', h.url);
    console.log('长度:', h.len);
    console.log('片段:', h.snippet);
    console.log('----');
  }
}

console.log('\n=== 渲染层：页面里是否出现 postid / post_detail ===');
const domSig = await page.evaluate(() => {
  const html = document.body?.innerHTML ?? '';
  return {
    hasPostid: /postid/i.test(html),
    hasPostDetail: /post_detail/i.test(html),
    // 找含 postid 的最近容器
    sample: (html.match(/.{0,80}postid[=:]["']?\d{6,}.{0,40}/i) ?? [])[0] ?? '(无)',
  };
});
console.log(domSig);

console.log('\n=== 渲染层：疑似岗位卡片（含「工作地点/招聘范围」等信号词的容器 outerHTML 前 800 字）===');
const cards = await page.evaluate(() => {
  const sig = ['工作地点', '招聘范围', '岗位职责', '岗位类别', 'Base地', '工作城市', '招聘人数'];
  const hits = Array.from(document.querySelectorAll('*')).filter((el) =>
    sig.some((k) => (el.textContent ?? '').includes(k)),
  );
  const roots = new Set();
  for (const h of hits) {
    let node = h;
    for (let i = 0; i < 6 && node; i++) node = node.parentElement;
    if (node) roots.add(node);
  }
  return [...roots].slice(0, 3).map((n) => (n.outerHTML ?? '').slice(0, 800));
});
console.log(cards.length ? cards.join('\n----\n') : '(未找到候选卡片)');

console.log('\n=== 渲染层：含 post_detail 的 <a> href（去重）===');
const detailHrefs = await page.evaluate(() => {
  const set = new Set();
  for (const a of document.querySelectorAll('a[href]')) {
    const h = a.getAttribute('href') ?? '';
    if (/post_detail/i.test(h)) set.add(h.slice(0, 120));
  }
  return [...set].slice(0, 10);
});
console.log(detailHrefs.length ? detailHrefs.join('\n') : '(无 post_detail 链接)');

// 2) 验证详情页 JD 抓取：拿第一个 postId 打开详情页，等渲染后 dump 正文长度。
const firstPostId = await page.evaluate(() => {
  // 重新抓一次接口拿 postId（上面 reload 后监听器可能已失效，这里直接 fetch）。
  return null;
}).catch(() => null);

console.log('\n=== 详情页验证：post_detail.html?postid=<首个postId> ===');
// 从已捕获的 apiHits 里取第一个 postId。
const postIdMatch = (apiHits[0]?.snippet ?? '').match(/"postId":"(\d{6,})"/);
if (!postIdMatch) {
  console.log('(未从接口响应中解析到 postId，跳过详情页验证)');
} else {
  const postId = postIdMatch[1];
  const detailUrl = `https://join.qq.com/post_detail.html?postid=${postId}`;
  console.log('详情页 URL:', detailUrl);
  await page.goto(detailUrl, { waitUntil: 'domcontentloaded', timeout: 30000 }).catch(() => {});
  await page.waitForTimeout(2500);
  const detail = await page.evaluate(() => {
    const t = (document.body?.innerText ?? '').replace(/\s+/g, ' ').trim();
    return { len: t.length, head: t.slice(0, 200) };
  });
  console.log('正文长度:', detail.len);
  console.log('正文前 200 字:', detail.head || '(空)');
}

await ctx.close();
