import assert from 'node:assert/strict';
import test from 'node:test';
import { chromium } from 'playwright';
import { performNavAction } from './nav_act.js';
import type { Session } from './browser_manager.js';

test('search uses the supplied snapshot ref instead of scanning another input', async (t) => {
  const browser = await chromium.launch({ headless: true });
  t.after(async () => browser.close());

  const page = await browser.newPage();
  await page.setContent(`
    <input id="wrong" placeholder="搜索站内内容">
    <input id="job-search" placeholder="搜索职位或关键词">
    <script>
      document.querySelector('#job-search').addEventListener('keydown', (event) => {
        if (event.key === 'Enter') document.body.dataset.submitted = event.target.value;
      });
    </script>
  `);
  const handle = await page.locator('#job-search').elementHandle();
  assert.ok(handle, 'job search input should exist');
  const session = {
    page,
    exploreRefs: new Map([['el:2', handle]]),
  } as unknown as Session;

  const result = await performNavAction(session, { type: 'search', ref: 'el:2', keyword: 'ai' });

  assert.equal(result.ok, true);
  assert.equal(await page.locator('#job-search').inputValue(), 'ai');
  assert.equal(await page.locator('#wrong').inputValue(), '');
  assert.equal(await page.locator('body').getAttribute('data-submitted'), 'ai');
});

test('search rejects an expired snapshot ref', async (t) => {
  const browser = await chromium.launch({ headless: true });
  t.after(async () => browser.close());
  const page = await browser.newPage();
  const session = { page, exploreRefs: new Map() } as unknown as Session;

  const result = await performNavAction(session, { type: 'search', ref: 'el:404', keyword: 'ai' });

  assert.equal(result.ok, false);
  assert.match(result.message, /不存在或已过期/);
});

test('click_ref allows navigation entries whose text contains 投递', async (t) => {
  const browser = await chromium.launch({ headless: true });
  t.after(async () => browser.close());

  const page = await browser.newPage();
  await page.setContent(`
    <a id="jobs-entry" href="#jobs">岗位投递</a>
  `);
  const handle = await page.locator('#jobs-entry').elementHandle();
  assert.ok(handle, 'jobs entry should exist');
  const session = {
    page,
    exploreRefs: new Map([['el:3', handle]]),
  } as unknown as Session;

  const result = await performNavAction(session, { type: 'click_ref', ref: 'el:3' });

  assert.equal(result.ok, true);
  assert.equal(new URL(page.url()).hash, '#jobs');
});
