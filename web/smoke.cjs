const { chromium } = require('./node_modules/playwright-core');
const fs = require('node:fs/promises');
const assert = require('node:assert/strict');

(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH || '/usr/bin/chromium', args: ['--no-sandbox','--disable-dev-shm-usage'] });
  const errors = [];
  const base = process.env.SMOKE_BASE;
  async function admin(path, method, body) { const response = await fetch(base+'/api/v1'+path,{method,headers:{Authorization:'Bearer '+process.env.ENGINE_ADMIN_TOKEN,'Content-Type':'application/json'},body:JSON.stringify(body)}); if(!response.ok)throw new Error('Fixture setup failed: '+response.status+' '+await response.text()); return response; }
  await admin('/engines/install','POST',{definition:{schemaVersion:1,id:'ppm-pgm',version:'1.0.0',name:'PPM to PGM test converter',platform:'linux-amd64',capabilities:['image.ppm.decode','image.pgm.encode'],executables:{'ppm-pgm':'ppm-pgm'}},sourceDirectory:'/var/lib/101conv/imports'});
  await admin('/profiles','POST',{definition:{schemaVersion:1,type:'conversion',id:'ppm-pgm-grayscale',version:'1.0.0',name:'PPM to grayscale PGM',description:'Convert ASCII PPM into portable grayscale images.',license:'MIT',engines:[{engineId:'ppm-pgm',versions:{minimum:'1.0.0',maximumExclusive:'2.0.0'},capabilities:['image.ppm.decode','image.pgm.encode']}],rules:[{engineId:'ppm-pgm',executable:'ppm-pgm',sources:['ppm'],targets:['pgm'],arguments:['--input','{source}','--output','{target}']}]}});
  await admin('/profiles','POST',{definition:{schemaVersion:1,type:'compression',id:'ppm-pgm-compress',version:'1.0.0',name:'PPM Image Compressor',description:'Compress a PPM image to grayscale PGM.',license:'MIT',engines:[{engineId:'ppm-pgm',versions:{minimum:'1.0.0',maximumExclusive:'2.0.0'},capabilities:['image.ppm.decode','image.pgm.encode']}],compression:{options:[{key:'quality',label:'Quality',type:'number',default:'85',min:1,max:100,step:1}]},rules:[{engineId:'ppm-pgm',executable:'ppm-pgm',sources:['ppm'],targets:['pgm'],arguments:['--input','{source}','{compression.args}','--output','{target}'],compressionArguments:['--quality','{option:quality}']}]}});
  await admin('/profiles','POST',{definition:{schemaVersion:1,type:'conversion',id:'pnm-pgm-alternate',version:'1.0.0',name:'PNM to grayscale PGM',description:'Convert portable anymap images to PGM.',license:'MIT',engines:[{engineId:'ppm-pgm',versions:{minimum:'1.0.0',maximumExclusive:'2.0.0'},capabilities:['image.ppm.decode','image.pgm.encode']}],rules:[{engineId:'ppm-pgm',executable:'ppm-pgm',sources:['pnm'],targets:['pgm'],arguments:['--input','{source}','--output','{target}']}]}});
  const ppm = Buffer.from('P3\n2 1\n255\n255 0 0 0 255 0\n');
  const uploadForm = new FormData(); uploadForm.append('file', new Blob([ppm]), 'mode-check.ppm');
  const uploadResponse = await fetch(base+'/api/v1/uploads',{method:'POST',headers:{Authorization:'Bearer '+process.env.ENGINE_ADMIN_TOKEN},body:uploadForm});
  assert.equal(uploadResponse.status,201,'test upload is created');
  const uploadId=(await uploadResponse.json()).id;
  const compressionAsConversion=await fetch(base+'/api/v1/jobs',{method:'POST',headers:{Authorization:'Bearer '+process.env.ENGINE_ADMIN_TOKEN,'Content-Type':'application/json'},body:JSON.stringify({uploadId,profileId:'ppm-pgm-compress',targetFormat:'pgm',idempotencyKey:'wrong-mode-conversion-001'})});
  assert.equal(compressionAsConversion.status,422,'compression profile cannot be submitted as a conversion');
  const conversionAsCompression=await fetch(base+'/api/v1/compressions',{method:'POST',headers:{Authorization:'Bearer '+process.env.ENGINE_ADMIN_TOKEN,'Content-Type':'application/json'},body:JSON.stringify({uploadId,profileId:'ppm-pgm-grayscale',targetFormat:'pgm',idempotencyKey:'wrong-mode-compression-01',options:{}})});
  assert.equal(conversionAsCompression.status,422,'conversion profile cannot be submitted as a compression job');
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, acceptDownloads: true });
    const page = await context.newPage();
    page.on('pageerror', error => errors.push(error.message));
    const apiResponses=[];page.on('response',async response=>{if(/\/api\/v1\/(jobs|uploads)/.test(response.url()))apiResponses.push({url:response.url(),status:response.status(),body:(await response.text().catch(()=>'' )).slice(0,400)});});
    await page.goto(base, { waitUntil: 'networkidle' });
    await page.getByLabel('Password').fill(process.env.ENGINE_ADMIN_TOKEN);
    await page.getByRole('button', { name: /^Sign in$/ }).click();
    await page.getByRole('heading', { name: 'What are we converting?' }).waitFor();
    assert.equal(await page.locator('body').innerText().then(t => t.includes(process.env.ENGINE_ADMIN_TOKEN)), false, 'admin token must not appear in UI');

    await page.getByRole('link', { name: 'Profile defaults' }).click();
    await page.getByRole('heading', { name: 'Profile defaults' }).waitFor();
    await Promise.all([page.waitForResponse(r=>r.url().includes('/profile-preferences/global/convert/ppm/pgm')&&r.request().method()==='PUT'),page.getByLabel('Default convert profile for ppm to pgm').selectOption('ppm-pgm-grayscale')]);
    await page.getByRole('link', { name: 'Account & API keys' }).click();
    await page.getByRole('heading', { name: 'Account & API keys' }).waitFor();
    await Promise.all([page.waitForResponse(r=>r.url().includes('/profile-preferences/me/convert/ppm/pgm')&&r.request().method()==='PUT'),page.getByLabel('convert profile for ppm to pgm').selectOption('ppm-pgm-grayscale')]);
    let preference = await page.evaluate(async()=>fetch('/api/v1/profile-preferences?mode=convert').then(r=>r.json()));
    assert.equal(preference.preferences.find(x=>x.sourceFormat==='ppm'&&x.targetFormat==='pgm').effectiveSource,'personal preference');
    await Promise.all([page.waitForResponse(r=>r.url().includes('/profile-preferences/me/convert/ppm/pgm')&&r.request().method()==='DELETE'),page.getByLabel('convert profile for ppm to pgm').selectOption('')]);
    preference = await page.evaluate(async()=>fetch('/api/v1/profile-preferences?mode=convert').then(r=>r.json()));
    assert.equal(preference.preferences.find(x=>x.sourceFormat==='ppm'&&x.targetFormat==='pgm').effectiveSource,'administrator default');

    const converterNav = page.getByRole('navigation', { name: 'Converter navigation' });
    await converterNav.getByRole('link', { name: 'Profiles' }).click();
    await page.getByRole('heading', { name: 'Manage converters' }).waitFor();
    const managementNav=page.getByRole('navigation',{name:'Converter management'});
    assert.equal(await managementNav.getByRole('link',{name:/Conversion profiles/}).getAttribute('aria-current'),'page');
    await managementNav.getByRole('link',{name:/Compression profiles/}).click();
    await page.getByRole('heading',{name:'Installed compression profiles'}).waitFor();
    assert.deepEqual(await page.locator('.converter-card h3').allTextContents(),['PPM Image Compressor']);
    await converterNav.getByRole('link', { name: 'Engines' }).click();
    assert.match(page.url(), /tab=engines$/);
    assert.equal(await page.locator('.tabbar a').nth(2).getAttribute('aria-current'), 'page');
    await converterNav.getByRole('link', { name: 'Repository catalog' }).click();
    await page.getByRole('heading', { name: 'Browse the catalog' }).waitFor();
    const catalogNav=page.getByRole('navigation',{name:'Repository catalog categories'});
    await catalogNav.getByRole('link',{name:'Compression profiles'}).click();
    assert.match(page.url(), /marketplace\?tab=compression$/);
    await catalogNav.getByRole('link',{name:'Engines'}).click();
    assert.match(page.url(), /marketplace\?tab=engines$/);
    await page.goto(base + '/convert', { waitUntil: 'networkidle' });
    await page.getByRole('heading', { name: 'What are we converting?' }).waitFor();

    await page.locator('input[type=file]').setInputFiles([{ name: 'sample.ppm', mimeType: 'image/x-portable-pixmap', buffer: ppm }, { name: 'sample.pnm', mimeType: 'image/x-portable-anymap', buffer: ppm }]);
    await page.getByText('sample.pnm', { exact: true }).waitFor();
    await page.getByLabel('Convert to').selectOption('pgm');
    assert.match(await page.locator('#profile-select option:checked').textContent(), /resolve each format mapping/, 'mixed batch uses independent profile mappings');
    const convert = page.getByRole('button', { name: /Convert 2 files/ });
    await convert.click();
    await page.getByRole('status').filter({ hasText: '2 conversions queued' }).waitFor({ timeout: 10000 });
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
    await page.getByLabel('Search jobs').fill('pnm-pgm-alternate');
    const alternateRow = page.locator('tr').filter({ hasText: 'pnm-pgm-alternate' });
    await alternateRow.waitFor({ timeout: 15000 });
    await alternateRow.getByText('Succeeded', { exact: true }).waitFor({ timeout: 45000 });

    await page.goto(base + '/compress', { waitUntil: 'networkidle' });
    await page.getByRole('heading', { name: 'Make files smaller' }).waitFor();
    await page.locator('input[type=file]').setInputFiles({ name: 'compress-me.ppm', mimeType: 'image/x-portable-pixmap', buffer: ppm });
    try { await page.getByText('compress-me.ppm', { exact: true }).waitFor({ timeout: 5000 }); }
    catch (error) { console.error('compression file input diagnostic', JSON.stringify({body:(await page.locator('body').innerText()).slice(0,1800),inputs:await page.locator('input[type=file]').evaluateAll(nodes=>nodes.map(node=>({multiple:node.multiple,files:[...node.files].map(file=>file.name)}))),url:page.url()})); throw error; }
    await page.getByLabel('Output format').waitFor({ state: 'visible' });
    assert.equal(await page.getByLabel('Output format').isEnabled(), true, 'supported compression outputs appear after file selection');
    await page.getByLabel('Output format').selectOption('pgm');
    assert.match(await page.locator('#compress-profile option:checked').textContent(), /PPM Image Compressor \(automatic fallback\)/, 'automatic compression selection names its compatible fallback');
    await page.getByLabel('Profile for this job').selectOption('ppm-pgm-compress');
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
    await mobilePage.getByRole('button', { name: /^Sign in$/ }).click();
    await mobilePage.getByRole('heading', { name: 'What are we converting?' }).waitFor();
    assert.equal(await mobilePage.locator('.mobile-nav').isVisible(), true, 'mobile navigation is visible');
    assert.deepEqual((await mobilePage.locator('.mobile-nav > a').allTextContents()).map(text=>text.trim()),['Convert','Compress','Jobs'],'mobile bottom bar keeps frequent actions visible');
    assert.equal(await mobilePage.getByText('WORKSPACE / CONVERT').count(),0,'page eyebrow labels are omitted');
    assert.equal(await mobilePage.getByText('Files stay on your server').count(),0,'promotional privacy badge is omitted');
    assert.equal(await mobilePage.getByText('Conversion runs privately on your server.').count(),0,'workflow promo is omitted');
    assert.equal(await mobilePage.getByText('Your conversions keep running while you browse.').count(),0,'activity promo is omitted');
    await mobilePage.getByRole('button',{name:'More navigation options'}).click();
    await mobilePage.getByRole('menuitem',{name:'Catalog'}).waitFor();
    await mobilePage.getByRole('menuitem',{name:'Users & access'}).waitFor();
    await mobilePage.getByRole('menuitem',{name:'Catalog'}).click();
    await mobilePage.getByRole('heading',{name:'Browse the catalog'}).waitFor();
    assert.equal(await mobilePage.locator('.mobile-nav').getByRole('button',{name:'More navigation options'}).getAttribute('aria-expanded'),'false','more menu closes after navigation');
    assert.equal(await mobilePage.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1), true, 'mobile page has no horizontal overflow');
    await mobilePage.goto(base + '/profile-defaults', { waitUntil: 'networkidle' });
    await mobilePage.getByRole('heading', { name: 'Profile defaults' }).waitFor();
    assert.equal(await mobilePage.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1), true, 'mobile profile defaults have no horizontal overflow');
    await mobilePage.goto(base + '/account', { waitUntil: 'networkidle' });
    await mobilePage.getByRole('heading', { name: 'Preferences' }).waitFor();
    assert.equal(await mobilePage.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1), true, 'mobile account preferences have no horizontal overflow');
    await mobilePage.getByRole('link', { name: 'Jobs' }).click();
    await mobilePage.getByRole('heading', { name: 'Jobs & history' }).waitFor();
    await mobile.close();
    await context.close();
    assert.deepEqual(errors, [], 'no uncaught browser errors');
    console.log('Browser verified profile preferences, mixed-format conversion batches, compression, history, result download, and mobile layouts.');
  } finally {
    await browser.close();
  }
})().catch(error => { console.error(error); process.exit(1); });
