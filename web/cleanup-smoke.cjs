const { chromium } = require('./node_modules/playwright-core');
const assert = require('node:assert/strict');

(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH || '/usr/bin/chromium', args: ['--no-sandbox', '--disable-dev-shm-usage'] });
  try {
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
    await Promise.all([
      page.waitForResponse(response => response.url().includes('/api/v1/storage/cleanup/preview') && response.status() === 200),
      row.getByRole('button', { name: 'Preview' }).click()
    ]);
    await page.getByText(/Preview: [1-9]\d* candidates, about/).waitFor();
    await Promise.all([
      page.waitForResponse(response => response.url().includes('/api/v1/storage/retention') && response.request().method() === 'PUT' && response.status() === 200),
      row.getByRole('button', { name: 'Save' }).click()
    ]);
    await Promise.all([
      page.waitForResponse(response => response.url().includes('/api/v1/storage/cleanup/preview') && response.status() === 200),
      row.getByRole('button', { name: 'Preview' }).click()
    ]);
    await page.getByText(/Preview: [1-9]\d* candidates, about/).waitFor();
    await page.evaluate(() => { window.confirm = () => true; });
    await Promise.all([
      page.waitForResponse(response => response.url().includes('/api/v1/storage/cleanup') && response.request().method() === 'POST' && response.status() === 200),
      row.getByRole('button', { name: 'Clean up' }).click()
    ]);
    const resultStatus = await page.evaluate(async id => (await fetch('/api/v1/jobs/' + id + '/result')).status, process.env.RETENTION_JOB_ID);
    assert.equal(resultStatus, 410, 'cleanup expires the result endpoint without deleting job history');
    await page.goto(process.env.SMOKE_BASE + '/jobs', { waitUntil: 'networkidle' });
    await page.getByLabel('Search jobs').fill(process.env.RETENTION_JOB_ID);
    await page.getByLabel('Filter jobs by status').selectOption('succeeded');
    await page.getByText('Result expired', { exact: true }).waitFor();
    await context.close();
    console.log('Browser verified retention preview, cleanup confirmation, expired result response, and retained job history.');
  } finally {
    await browser.close();
  }
})().catch(error => { console.error(error); process.exit(1); });
