import { chromium } from 'playwright';

// 诊断：腾讯招聘首页在未登录状态下到底长什么样、是否需要登录、岗位链接结构。
const ctx = await chromium.launchPersistentContext('/tmp/tx-diag-auth', {
  headless: true,
  viewport: { width: 1440, height: 900 },
  locale: 'zh-CN',
});
const page = ctx.pages()[0] ?? (await ctx.newPage());

await page.goto('https://careers.tencent.com/', { waitUntil: 'domcontentloaded', timeout: 60000 });
// SPA 需要时间渲染。
await page.waitForTimeout(4000);

const title = await page.title();
const text = await page.evaluate(() => document.body?.innerText ?? '');

console.log('=== 标题 ===');
console.log(title);
console.log('\n=== 正文长度 ===', text.length);
console.log('=== 正文前 1200 字 ===');
console.log(text.slice(0, 1200));

console.log('\n=== 当前 adapter 的登录判定依据 ===');
console.log('含「个人中心」:', text.includes('个人中心'));
console.log('含「我的投递」:', text.includes('我的投递'));
console.log('含「退出」  :', text.includes('退出'));

console.log('\n=== 是否出现登录墙信号 ===');
for (const kw of ['登录', '注册', 'Sign in', '验证码', '请先登录']) {
  console.log(`  含「${kw}」:`, text.includes(kw));
}

console.log('\n=== 页面上所有 a 标签 href（前 30 条，过滤 javascript:）===');
const links = await page.evaluate(() =>
  Array.from(document.querySelectorAll('a'))
    .map((a) => a.href)
    .filter((h) => h && !h.startsWith('javascript:'))
    .slice(0, 30),
);
console.log(links.length ? links.join('\n') : '(无有效链接)');

console.log('\n=== 含 position/job 关键词的链接 ===');
const jobLinks = await page.evaluate(() =>
  Array.from(document.querySelectorAll('a'))
    .map((a) => a.href)
    .filter((h) => /position|job|detail/i.test(h))
    .slice(0, 20),
);
console.log(jobLinks.length ? jobLinks.join('\n') : '(无岗位链接)');

await ctx.close();
