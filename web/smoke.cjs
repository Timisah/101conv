const { chromium } = require('./node_modules/playwright-core');
const fs = require('node:fs/promises');
const assert = require('node:assert/strict');

(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH || '/usr/bin/chromium', args: ['--no-sandbox','--disable-dev-shm-usage'] });
  const errors = [];
  const base = process.env.SMOKE_BASE;
  async function admin(path, method, body) { const response = await fetch(base+'/api/v1'+path,{method,headers:{Authorization:'Bearer '+process.env.ENGINE_ADMIN_TOKEN,'Content-Type':'application/json'},body:JSON.stringify(body)}); if(!response.ok)throw new Error('Fixture setup failed: '+response.status+' '+await response.text()); return response; }
  await admin('/engines/install','POST',{definition:{schemaVersion:1,id:'ppm-pgm',version:'1.0.0',name:'PPM to PGM test converter',platform:'linux-amd64',capabilities:['image.ppm.decode','image.pgm.encode'],executables:{'ppm-pgm':'ppm-pgm'}},sourceDirectory:'/var/lib/101conv/imports'});
  await admin('/profiles','POST',{definition:{schemaVersion:1,id:'ppm-pgm-grayscale',version:'1.0.0',name:'PPM to grayscale PGM',description:'Convert ASCII PPM into portable grayscale images.',license:'MIT',engines:[{engineId:'ppm-pgm',versions:{minimum:'1.0.0',maximumExclusive:'2.0.0'},capabilities:['image.ppm.decode','image.pgm.encode']}],rules:[{engineId:'ppm-pgm',executable:'ppm-pgm',sources:['ppm'],targets:['pgm'],arguments:['--input','{source}','--output','{target}']}]}});
  await admin('/profiles','POST',{definition:{schemaVersion:1,id:'ppm-pgm-compress',version:'1.0.0',name:'PPM Image Compressor',description:'Compress a PPM image to grayscale PGM.',license:'MIT',engines:[{engineId:'ppm-pgm',versions:{minimum:'1.0.0',maximumExclusive:'2.0.0'},capabilities:['image.ppm.decode','image.pgm.encode']}],compression:{options:[{key:'quality',label:'Quality',type:'number',default:'85',min:1,max:100,step:1}]},rules:[{engineId:'ppm-pgm',executable:'ppm-pgm',sources:['ppm'],targets:['pgm'],arguments:['--input','{source}','{compression.args}','--output','{target}'],compressionArguments:['--quality','{option:quality}']}]}});
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, acceptDownloads: true });
    const page = await context.newPage();
    page.on('pageerror', error => errors.push(error.message));
    const apiResponses=[];page.on('response',async response=>{if(/\/api\/v1\/(jobs|uploads)/.test(response.url()))apiResponses.push({url:response.url(),status:response.status(),body:(await response.text().catch(()=>'' )).slice(0,400)});});
    await page.goto(base, { waitUntil: 'networkidle' });
    await page.getByLabel('Password').fill(process.env.ENGINE_ADMIN_TOKEN);
    await page.getByRole('button', { name: /Sign in securely/ }).click();
    await page.getByRole('heading', { name: 'What are we converting?' }).waitFor();
    assert.equal(await page.locator('body').innerText().then(t => t.includes(process.env.ENGINE_ADMIN_TOKEN)), false, 'admin token must not appear in UI');

    const converterNav = page.getByRole('navigation', { name: 'Converter navigation' });
    await converterNav.getByRole('link', { name: 'Profiles' }).click();
    await page.getByRole('heading', { name: 'Manage converters' }).waitFor();
    assert.equal(await page.locator('.tabbar a').nth(0).getAttribute('aria-current'), 'page');
    await converterNav.getByRole('link', { name: 'Engines' }).click();
    assert.match(page.url(), /tab=engines$/);
    assert.equal(await page.locator('.tabbar a').nth(1).getAttribute('aria-current'), 'page');
    await converterNav.getByRole('link', { name: 'Repository catalog' }).click();
    await page.getByRole('heading', { name: 'Browse available converters' }).waitFor();
    await page.goto(base + '/convert', { waitUntil: 'networkidle' });
    await page.getByRole('heading', { name: 'What are we converting?' }).waitFor();

    const ppm = Buffer.from('P3\n2 1\n255\n255 0 0 0 255 0\n');
    await page.locator('input[type=file]').setInputFiles({ name: 'sample.ppm', mimeType: 'image/x-portable-pixmap', buffer: ppm });
    await page.getByLabel('Convert to').selectOption('pgm');
    const convert = page.getByRole('button', { name: /Convert 1 file/ });
    await convert.click();
    await page.getByRole('status').filter({ hasText: 'Conversion queued' }).waitFor({ timeout: 10000 }).catch(() => {});
    if (!await page.evaluate(()=>localStorage.getItem('101conv-job-ids'))) console.error('submit diagnostic',JSON.stringify({body:(await page.locator('body').innerText()).slice(0,1000),apiResponses}));
    await page.getByRole('link', { name: 'Jobs & history' }).click();
    await page.getByLabel('Search jobs').fill('ppm-pgm-grayscale');
    await page.getByLabel('Filter jobs by status').selectOption('succeeded');
    const row = page.locator('tr').filter({ hasText: 'ppm-pgm-grayscale' });
    try { await row.waitFor({ timeout: 15000 }); } catch (error) { console.error('job diagnostic',JSON.stringify({ids:await page.evaluate(()=>localStorage.getItem('101conv-job-ids')),body:(await page.locator('body').innerText()).slice(0,1200),apiResponses})); throw error; }
    await row.getByText('Succeeded', { exact: true }).waitFor({ timeout: 45000 });
    const [download] = await Promise.all([
      page.waitForEvent('download', { timeout: 15000 }),
      row.getByRole('button', { name: 'Download result' }).click()
    ]);
    const path = await download.path();
    assert.ok(path, 'result file was downloaded');
    const result = await fs.readFile(path, 'utf8');
    assert.match(result, /^P2\n2 1\n255\n/);
    assert.match(result, /\b76\s+150\s/);

    await page.goto(base + '/compress', { waitUntil: 'networkidle' });
    await page.getByRole('heading', { name: 'Make files smaller' }).waitFor();
    await page.locator('input[type=file]').setInputFiles({ name: 'compress-me.ppm', mimeType: 'image/x-portable-pixmap', buffer: ppm });
    await page.getByLabel('Compression profile').selectOption('ppm-pgm-compress');
    await page.getByLabel('Output format').selectOption('pgm');
    await page.getByRole('button', { name: /Compress 1 file/ }).click();
    await page.getByText('Compression queued. Track it in Jobs & history.').waitFor({ timeout: 10000 });
    await page.getByRole('link', { name: 'Jobs & history' }).click();
    await page.getByLabel('Search jobs').fill('ppm-pgm-compress');
    await page.getByLabel('Filter jobs by status').selectOption('');
    const compressedRow = page.locator('tr').filter({ hasText: 'ppm-pgm-compress' });
    await compressedRow.waitFor({ timeout: 15000 });
    await compressedRow.getByText(/^Compression ·/).waitFor();
    try { await compressedRow.getByText('Succeeded', { exact: true }).waitFor({ timeout: 45000 }); } catch (error) { console.error('compression job diagnostic', (await compressedRow.innerText()).slice(0,1000)); throw error; }

    const mobile = await browser.newContext({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 1, isMobile: true, hasTouch: true });
    const mobilePage = await mobile.newPage();
    mobilePage.on('pageerror', error => errors.push(error.message));
    await mobilePage.goto(base + '/convert', { waitUntil: 'networkidle' });
    await mobilePage.getByLabel('Password').fill(process.env.ENGINE_ADMIN_TOKEN);
    await mobilePage.getByRole('button', { name: /Sign in securely/ }).click();
    await mobilePage.getByRole('heading', { name: 'What are we converting?' }).waitFor();
    assert.equal(await mobilePage.locator('.mobile-nav').isVisible(), true, 'mobile navigation is visible');
    assert.equal(await mobilePage.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1), true, 'mobile page has no horizontal overflow');
    await mobilePage.getByRole('link', { name: 'Jobs' }).click();
    await mobilePage.getByRole('heading', { name: 'Jobs & history' }).waitFor();
    await mobile.close();
    await context.close();
    assert.deepEqual(errors, [], 'no uncaught browser errors');
    console.log('Browser verified login, conversion and compression, job history, result download, and mobile navigation.');
  } finally {
    await browser.close();
  }
})().catch(error => { console.error(error); process.exit(1); });
