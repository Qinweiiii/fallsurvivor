import { chromium } from 'playwright';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// 诊断：找到腾讯校招详情页拉取岗位详情的真实 API 路径。
// 已知列表接口是 /api/v1/position/searchPosition，但详情接口路径未知（/api/v1/jobDetails 是 404）。
// 本脚本导航到详情页 post_detail.html?postid=xxx，捕获所有 XHR/fetch 请求，
// 找到含 postId 或岗位 desc 的那个响应，打印其真实 URL。
//
// 用法（先停 make bw）：
//   node worker-browser/diag_tencent_detail_api.mjs
//   TX_POST_ID=1282707398326592512 node worker-browser/diag_tencent_detail_api.mjs

const POST_ID = process.env.TX_POST_ID || '1282707398326592512';
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
  console.error('启动浏览器失败（先停掉 make bw）：', e.message);
  process.exit(1);
}
const page = ctx.pages()[0] ?? (await ctx.newPage());

// 捕获所有网络请求（只要含 join.qq.com 的 api/v1 路径）。
const apiCalls = [];
page.on('response', async (resp) => {
  const u = resp.url();
  if (!/join\.qq\.com\/api\//i.test(u)) return;
  const ct = resp.headers()['content-type'] ?? '';
  if (!/json|text/i.test(ct)) return;
  try {
    const body = await resp.text();
    apiCalls.push({ url: u, status: resp.status(), len: body.length, body: body.slice(0, 500) });
  } catch {
    apiCalls.push({ url: u, status: resp.status(), len: 0, body: '(读取失败)' });
  }
});

const detailUrl = `https://join.qq.com/post_detail.html?postid=${POST_ID}`;
console.log('=== 导航到详情页 ===');
console.log(detailUrl);
await page.goto(detailUrl, { waitUntil: 'networkidle', timeout: 30000 }).catch(() => {});
await page.waitForTimeout(3000);

console.log(`\n=== 详情页所有 API 调用（共 ${apiCalls.length} 个）===`);
for (const c of apiCalls) {
  console.log(`\nURL: ${c.url}`);
  console.log(`Status: ${c.status}  长度: ${c.len}`);
  console.log(`片段: ${c.body.slice(0, 300)}`);
  console.log('---');
}

// 找含 postId 或 desc 的响应。
const detail = apiCalls.find((c) => c.body.includes('"desc"') || c.body.includes('"intentionBGDList"'));
if (detail) {
  console.log('\n★ 找到详情数据接口 ★');
  console.log('真实 URL:', detail.url);
  console.log('用这个路径替换 adapter 里的 /api/v1/jobDetails');
} else {
  console.log('\n未找到含 desc/intentionBGDList 的响应');
}

await ctx.close();
