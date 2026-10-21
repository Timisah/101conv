const { chromium } = require('./node_modules/playwright-core');
const assert = require('node:assert/strict');

(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH || '/usr/bin/chromium', args: ['--no-sandbox', '--disable-dev-shm-usage'] });
  try {
    async function expectResponse(request, action, label) {
      const response = await Promise.all([
        page.waitForResponse(candidate => candidate.url().includes(request.url) && candidate.request().method() === request.method),
        action(),
      ]).then(([candidate]) => candidate);
      const body = await response.text().catch(() => '');
      assert.equal(response.status(), request.status, `${label}: ${body}`);
      return body;
    }
    const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
    const page = await context.newPage();
    page.on('pageerror', error => { throw error; });
    // Setup enables guest access. Authenticate this protected-page fixture
    // explicitly instead of assuming a fresh browser opens a login form.
    const login = await context.request.post(process.env.SMOKE_BASE + '/api/v1/auth/session', {
      headers: { Origin: process.env.SMOKE_BASE }, data: { password: process.env.ENGINE_ADMIN_TOKEN },
    });
    assert.equal(login.status(), 201, 'cleanup fixture administrator signs in');
    await page.goto(process.env.SMOKE_BASE + '/storage', { waitUntil: 'networkidle' });
    await page.getByRole('heading', { name: 'Storage', exact: true }).waitFor();
    const row = page.locator('.retention-policy-row').filter({ hasText: 'Successful results' });
    await page.getByLabel('Successful results policy').selectOption('after_days');
    await page.getByLabel('Successful results days').fill('1');
    await expectResponse({url: '/api/v1/storage/cleanup/preview', method: 'GET', status: 200}, () => row.getByRole('button', { name: 'Preview' }).click(), 'initial cleanup preview');
    await page.getByText(/Preview: [1-9]\d* candidates, about/).waitFor();
    await expectResponse({url: '/api/v1/storage/retention', method: 'PUT', status: 200}, () => row.getByRole('button', { name: 'Save' }).click(), 'save retention policy');
    await expectResponse({url: '/api/v1/storage/cleanup/preview', method: 'GET', status: 200}, () => row.getByRole('button', { name: 'Preview' }).click(), 'post-save cleanup preview');
    await page.getByText(/Preview: [1-9]\d* candidates, about/).waitFor();
    await page.evaluate(() => { window.confirm = () => true; });
    await expectResponse({url: '/api/v1/storage/cleanup', method: 'POST', status: 200}, () => row.getByRole('button', { name: 'Clean up' }).click(), 'apply cleanup');
    const resultStatus = await page.evaluate(async id => (await fetch('/api/v1/jobs/' + id + '/result')).status, process.env.RETENTION_JOB_ID);
    assert.equal(resultStatus, 410, 'cleanup expires the result endpoint without deleting job history');
    await page.goto(process.env.SMOKE_BASE + '/jobs', { waitUntil: 'networkidle' });
    const retentionSearch = page.waitForResponse(r => r.url().includes('/api/v1/history') && r.url().includes('q=' + process.env.RETENTION_JOB_ID), { timeout: 15000 });
    await page.getByLabel('Search jobs').fill(process.env.RETENTION_JOB_ID);
    await retentionSearch;
    await page.getByLabel('Filter jobs by status').selectOption('succeeded');
    const retainedBatch = page.locator('.batch-history-card').first();
    await retainedBatch.waitFor({ timeout: 15000 });
    await retainedBatch.locator(':scope > summary').click();
    await retainedBatch.getByText('Result expired', { exact: true }).waitFor();
    await context.close();
    console.log('Browser verified retention preview, cleanup confirmation, expired result response, and retained job history.');
  } finally {
    await browser.close();
  }
})().catch(error => { console.error(error); process.exit(1); });
