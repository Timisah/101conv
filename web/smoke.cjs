const { chromium } = require('./node_modules/playwright-core');
const fs = require('node:fs/promises');
const assert = require('node:assert/strict');

(async () => {
  const browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH || '/usr/bin/chromium', args: ['--no-sandbox','--disable-dev-shm-usage'] });
  const errors = [];
  let activePage = null;
  const base = process.env.SMOKE_BASE;
  async function chooseAdvanced(page, profileID) { await page.locator('details.advanced-provider').first().locator('summary').click(); await page.getByLabel('Profile for this job').selectOption(profileID); }
  async function admin(path, method, body) { const response = await fetch(base+'/api/v1'+path,{method,headers:{Authorization:'Bearer '+process.env.ENGINE_ADMIN_TOKEN,'Content-Type':'application/json'},body:JSON.stringify(body)}); if(!response.ok)throw new Error('Fixture setup failed: '+response.status+' '+await response.text()); return response; }
  await admin('/engines/install','POST',{definition:{schemaVersion:1,id:'ppm-pgm',version:'1.0.0',name:'PPM to PGM test converter',platform:'linux-amd64',capabilities:['image.ppm.decode','image.pgm.encode'],executables:{'ppm-pgm':'ppm-pgm'}},sourceDirectory:'/var/lib/101conv/imports'});
  await admin('/profiles','POST',{definition:{schemaVersion:1,type:'conversion',id:'ppm-pgm-grayscale',version:'1.0.0',name:'PPM to grayscale PGM',description:'Convert ASCII PPM into portable grayscale images.',license:'MIT',engines:[{engineId:'ppm-pgm',versions:{minimum:'1.0.0',maximumExclusive:'2.0.0'},capabilities:['image.ppm.decode','image.pgm.encode']}],rules:[{engineId:'ppm-pgm',executable:'ppm-pgm',sources:['ppm'],targets:['pgm'],arguments:['--input','{source}','--output','{target}']}]}});
  await admin('/profiles','POST',{definition:{schemaVersion:1,type:'compression',id:'ppm-pgm-compress',version:'1.0.0',name:'PPM Image Compressor',description:'Compress a PPM image to grayscale PGM.',license:'MIT',engines:[{engineId:'ppm-pgm',versions:{minimum:'1.0.0',maximumExclusive:'2.0.0'},capabilities:['image.ppm.decode','image.pgm.encode']}],compression:{options:[{key:'quality',label:'Quality',type:'number',default:'85',min:1,max:100,step:1}]},rules:[{engineId:'ppm-pgm',executable:'ppm-pgm',sources:['ppm'],targets:['pgm'],arguments:['--input','{source}','{compression.args}','--output','{target}'],compressionArguments:['--quality','{option:quality}']}]}});
  await admin('/profiles','POST',{definition:{schemaVersion:1,type:'conversion',id:'pnm-pgm-alternate',version:'1.0.0',name:'PNM to grayscale PGM',description:'Convert portable anymap images to PGM.',license:'MIT',engines:[{engineId:'ppm-pgm',versions:{minimum:'1.0.0',maximumExclusive:'2.0.0'},capabilities:['image.ppm.decode','image.pgm.encode']}],rules:[{engineId:'ppm-pgm',executable:'ppm-pgm',sources:['pnm'],targets:['pgm'],arguments:['--input','{source}','--output','{target}']}]}});
  await admin('/profiles','POST',{definition:{schemaVersion:1,type:'conversion',id:'ppm-pgm-slow',version:'1.0.0',name:'PPM to PGM (slow test)',description:'A delayed test profile for cancellation checks.',license:'MIT',engines:[{engineId:'ppm-pgm',versions:{minimum:'1.0.0',maximumExclusive:'2.0.0'},capabilities:['image.ppm.decode','image.pgm.encode']}],rules:[{engineId:'ppm-pgm',executable:'ppm-pgm',sources:['ppm'],targets:['pgm'],arguments:['--sleep-ms','5000','--input','{source}','--output','{target}']}]}});
  await admin('/profiles','POST',{definition:{schemaVersion:1,type:'conversion',id:'ppm-pgm-cancel',version:'1.0.0',name:'PPM to PGM (cancellation test)',description:'A long-running fixture that keeps cancellation controls active.',license:'MIT',engines:[{engineId:'ppm-pgm',versions:{minimum:'1.0.0',maximumExclusive:'2.0.0'},capabilities:['image.ppm.decode','image.pgm.encode']}],rules:[{engineId:'ppm-pgm',executable:'ppm-pgm',sources:['ppm'],targets:['pgm'],arguments:['--sleep-ms','60000','--input','{source}','--output','{target}']}]}});
  const ppm = Buffer.from('P3\n2 1\n255\n255 0 0 0 255 0\n');
  const uploadForm = new FormData(); uploadForm.append('file', new Blob([ppm]), 'mode-check.ppm');
  const uploadResponse = await fetch(base+'/api/v1/uploads',{method:'POST',headers:{Authorization:'Bearer '+process.env.ENGINE_ADMIN_TOKEN},body:uploadForm});
  assert.equal(uploadResponse.status,201,'test upload is created: '+(uploadResponse.ok?'':await uploadResponse.text()));
  const uploadId=(await uploadResponse.json()).id;
  const compressionAsConversion=await fetch(base+'/api/v1/jobs',{method:'POST',headers:{Authorization:'Bearer '+process.env.ENGINE_ADMIN_TOKEN,'Content-Type':'application/json'},body:JSON.stringify({uploadId,profileId:'ppm-pgm-compress',targetFormat:'pgm',idempotencyKey:'wrong-mode-conversion-001'})});
  assert.equal(compressionAsConversion.status,422,'compression profile cannot be submitted as a conversion');
  const conversionAsCompression=await fetch(base+'/api/v1/compressions',{method:'POST',headers:{Authorization:'Bearer '+process.env.ENGINE_ADMIN_TOKEN,'Content-Type':'application/json'},body:JSON.stringify({uploadId,profileId:'ppm-pgm-grayscale',targetFormat:'pgm',idempotencyKey:'wrong-mode-compression-01',options:{}})});
  assert.equal(conversionAsCompression.status,422,'conversion profile cannot be submitted as a compression job');
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, acceptDownloads: true });
    const page = await context.newPage();
    activePage = page;
    page.on('pageerror', error => errors.push(error.message));
    const apiResponses=[];page.on('response',async response=>{if(/\/api\/v1\/(jobs|uploads|batches)/.test(response.url()))apiResponses.push({url:response.url(),status:response.status(),body:(await response.text().catch(()=>'' )).slice(0,400)});});
    await page.goto(base, { waitUntil: 'networkidle' });
    await page.getByRole('heading', { name: 'Create your administrator account' }).waitFor();
    await page.getByLabel('Bootstrap passphrase').fill(process.env.ENGINE_ADMIN_TOKEN);
    await page.getByLabel('Administrator username').fill('smokeadmin');
    await page.getByLabel('New password').fill('smoke-test-password-123');
    await page.getByRole('button', { name: 'Create administrator' }).click();
    await page.getByRole('heading', { name: 'Check storage and start converting' }).waitFor();
    await page.getByRole('button', { name: 'Finish setup and convert' }).click();
    await page.getByRole('heading', { name: 'What are we converting?' }).waitFor();
    assert.equal(await page.locator('body').innerText().then(t => t.includes(process.env.ENGINE_ADMIN_TOKEN)), false, 'admin token must not appear in UI');

    await page.goto(base + '/storage', { waitUntil: 'networkidle' });
    await page.getByRole('heading', { name: 'Storage', exact: true }).waitFor();
    await page.getByText('Original uploads', { exact: true }).first().waitFor();
    await page.getByRole('button',{name:'Start scan'}).click();
    await page.waitForFunction(async()=>{const response=await fetch('/api/v1/storage');if(!response.ok)return false;return (await response.json()).scan.status==='complete';},null,{timeout:60000});
    await page.getByText('Engine packages',{exact:true}).waitFor();
    await page.getByLabel('Unsubmitted uploads policy').selectOption('after_days');
    await page.getByLabel('Unsubmitted uploads days').fill('7');
    await page.locator('.retention-policy-row').first().getByRole('button', { name: 'Preview' }).click();
    await page.getByText(/Preview: .* candidates, about/).waitFor();
    await Promise.all([page.waitForResponse(r => r.url().includes('/api/v1/storage/retention') && r.request().method() === 'PUT' && r.status() === 200), page.locator('.retention-policy-row').first().getByRole('button', { name: 'Save' }).click()]);
    await page.goto(base + '/operations', { waitUntil: 'networkidle' });
    await page.getByRole('heading', { name: 'Operations', exact: true }).waitFor();
    await page.getByText(/usable profiles/).first().waitFor();

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

    // Exercise the real automation flow end to end. Core is the only process
    // with these test mounts; the isolated conversion Worker has no mount.
    await page.goto(base + '/drives', { waitUntil: 'networkidle' });
    await page.getByRole('heading', { name: 'Mounted drives', level: 1 }).waitFor();
    await page.getByLabel('Name', { exact: true }).fill('Smoke incoming');
    await page.getByLabel('Container path').fill('/mounts/incoming');
    await page.getByLabel('Access').selectOption('read');
    await page.getByRole('button', { name: 'Register drive' }).click();
    await page.getByText('Smoke incoming', { exact: true }).waitFor();
    await page.getByLabel('Name', { exact: true }).fill('Smoke converted');
    await page.getByLabel('Container path').fill('/mounts/converted');
    await page.getByLabel('Access').selectOption('read_write');
    await page.getByRole('button', { name: 'Register drive' }).click();
    await page.getByText('Smoke converted', { exact: true }).waitFor();

    await page.goto(base + '/automations', { waitUntil: 'networkidle' });
    await page.getByRole('heading', { name: 'Automations', level: 1 }).waitFor();
    await page.getByLabel('Blueprint').selectOption('automatic-folder-conversion');
    await page.getByLabel('Name', { exact: true }).fill('Smoke folder conversion');
    await page.getByLabel('Input folder').selectOption({ label: 'Smoke incoming · ' + '/mounts/incoming' });
    await page.getByLabel('Output folder').selectOption({ label: 'Smoke converted · ' + '/mounts/converted' });
    await page.getByLabel('Installed profile').selectOption('ppm-pgm-grayscale');
    await page.getByLabel('Output format').selectOption('pgm');
    await page.getByLabel('Extensions').fill('ppm');
    await page.getByLabel('Scan interval (seconds)').fill('30');
    await page.getByLabel('Stable for (seconds)').fill('5');
    await page.getByLabel('Concurrent files (1–8)').fill('1');
    // Exercise the API's omitted-version policy through the real blueprint UI.
    await page.route('**/api/v1/blueprints/automatic-folder-conversion/install', async route => {
      const body = route.request().postDataJSON();
      delete body.profileVersion;
      await route.continue({ postData: JSON.stringify(body) });
    });
    await page.getByRole('button', { name: 'Install blueprint' }).click();
    const automation = page.locator('.automation-card').filter({ hasText: 'Smoke folder conversion' });
    await automation.waitFor({ timeout: 15000 });
    async function scanAutomationViaUI() {
      const [response] = await Promise.all([
        page.waitForResponse(response => /\/api\/v1\/automations\/[^/]+\/run$/.test(response.url()) && response.request().method() === 'POST'),
        automation.getByRole('button', { name: 'Run now' }).click(),
      ]);
      assert.equal(response.status(), 202, 'automation scan accepted: ' + (response.ok() ? '' : await response.text()));
    }
    await automation.getByRole('button', { name: 'Enable automation' }).click();
    await automation.getByRole('button', { name: 'Preview' }).click();
    await page.getByRole('heading', { name: 'Current-file match preview' }).waitFor();
    await scanAutomationViaUI(); // establishes empty initial scan
    await fs.writeFile(process.env.SMOKE_MOUNT_ROOT + '/incoming/automation-input.ppm', ppm);
    await scanAutomationViaUI(); // first observation of the new file
    // The shell kills Core after the first persisted observation. Baseline
    // and observed-file state must survive; in-memory login sessions expire.
    await fs.writeFile('/smoke-output/automation-restart-ready', 'ready');
    let restarted = false;
    for (let i = 0; i < 120; i++) {
      try { await fs.access('/smoke-output/automation-restarted'); restarted = true; break; } catch {}
      await new Promise(resolve => setTimeout(resolve, 1000));
    }
    assert.ok(restarted, 'Core was restarted between automation observation and submission');
    const restoredLogin = await page.request.post(base + '/api/v1/auth/session', {
      headers: { Origin: base }, data: { username: 'smokeadmin', password: 'smoke-test-password-123' },
    });
    assert.equal(restoredLogin.status(), 201, 'same administrator can sign in after Core restart');
    await page.goto(base + '/automations', { waitUntil: 'networkidle' });
    await page.getByRole('heading', { name: 'Automations', level: 1 }).waitFor();
    await new Promise(resolve => setTimeout(resolve, 5500));
    await scanAutomationViaUI(); // stability window elapsed
    const savedAutomation = (await page.evaluate(() => fetch('/api/v1/automations').then(r => r.json()))).automations.find(a => a.name === 'Smoke folder conversion');
    assert.equal(savedAutomation?.definition.action.profileVersion, '1.0.0', 'omitted blueprint version is persisted exactly');
    const automationID = savedAutomation?.id;
    assert.ok(automationID, 'blueprint installed a persistent automation');
    let runs = [];
    for (let i = 0; i < 90; i++) {
      runs = await page.evaluate(async id => (await fetch('/api/v1/automations/' + id + '/history').then(r => r.json())).runs, automationID);
      if (runs.some(run => run.fileName === 'automation-input.ppm' && run.state === 'succeeded')) break;
      await new Promise(resolve => setTimeout(resolve, 1000));
    }
    const completedAutomationRun = runs.find(run => run.fileName === 'automation-input.ppm' && run.state === 'succeeded');
    assert.ok(completedAutomationRun, 'stable file was converted exactly once by the automation');
    assert.equal(runs.filter(run => run.fileName === 'automation-input.ppm').length, 1, 'unchanged input is not processed repeatedly');
    const automatedOutput = await fs.readFile(process.env.SMOKE_MOUNT_ROOT + '/converted/automation-input.pgm', 'utf8');
    assert.deepEqual(automatedOutput.trim().split(/\s+/), ['P2', '2', '1', '255', '76', '150'], 'automation output includes the exact expected grayscale pixels after Core recovery');
    await scanAutomationViaUI();
    await scanAutomationViaUI();
    const recoveredRuns = await page.evaluate(async id => (await fetch('/api/v1/automations/' + id + '/history').then(r => r.json())).runs, automationID);
    assert.equal(recoveredRuns.filter(run => run.jobId).length, 1, 'restart and repeated scans retain exactly one submitted job');
    assert.deepEqual((await fs.readdir(process.env.SMOKE_MOUNT_ROOT + '/converted')).filter(name => !name.startsWith('.101conv-')), ['automation-input.pgm'], 'restart produces exactly one published output');
    await automation.getByRole('button', { name: 'History' }).click();
    await page.getByText('automation-input.ppm', { exact: true }).waitFor();
    await page.setViewportSize({ width: 390, height: 844 });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1), true, 'automation editor fits mobile viewport');
    await page.setViewportSize({ width: 1440, height: 1000 });

    await page.goto(base + '/convert', { waitUntil: 'networkidle' });
    await page.getByRole('heading', { name: 'What are we converting?' }).waitFor();

    await page.locator('input[type=file]').setInputFiles(Array.from({length:20},(_,index)=>({ name: `sample-${String(index+1).padStart(2,'0')}.${index%2?'pnm':'ppm'}`, mimeType: index%2?'image/x-portable-anymap':'image/x-portable-pixmap', buffer: ppm })));
    await page.getByText('sample-20.pnm', { exact: true }).waitFor();
    await page.getByLabel('Convert to').selectOption('pgm');
    assert.match(await page.locator('#profile-select option:checked').textContent(), /resolve each format mapping/, 'mixed batch uses independent profile mappings');
    const convert = page.getByRole('button', { name: /Convert 20 files/ });
    await convert.click();
    await page.getByRole('status').filter({ hasText: 'Batch created: 20 item(s) submitted.' }).waitFor({ timeout: 10000 });
    if (!await page.evaluate(()=>localStorage.getItem('101conv-job-ids'))) console.error('submit diagnostic',JSON.stringify({body:(await page.locator('body').innerText()).slice(0,1000),apiResponses}));
    await page.getByRole('link', { name: 'Jobs & history' }).click();
    const conversionBatch=page.locator('.batch-history-card').filter({hasText:'Conversion batch'}).first();
    await conversionBatch.waitFor({timeout:15000});
    const conversionBatchID=(await conversionBatch.locator(':scope > summary').innerText()).match(/ID ([0-9a-f-]{36})/)?.[1];assert.ok(conversionBatchID,'20-file batch id is displayed');
    if(process.env.SMOKE_OUTPUT_DIR)await fs.writeFile(process.env.SMOKE_OUTPUT_DIR+'/batch-id',conversionBatchID);
    await page.waitForFunction(()=>[...document.querySelectorAll('.batch-history-card')].some(card=>card.innerText.includes('20 done')),null,{timeout:60000});
    await conversionBatch.locator(':scope > summary').click();
    const [batchDownload]=await Promise.all([page.waitForEvent('download'),conversionBatch.getByRole('button',{name:'Download successful files (.zip)'}).click()]);
    const zipPath=await batchDownload.path();assert.ok(zipPath,'successful batch archive downloaded');
    const zipBytes=await fs.readFile(zipPath);assert.equal(zipBytes.subarray(0,2).toString(),'PK','batch archive is a ZIP file');assert.ok(zipBytes.includes(Buffer.from('item-001-')),'archive contains a generated safe item filename');

    // A corrupt member fails independently. Retrying it must leave the good
    // member's original job untouched.
    await page.goto(base + '/convert', { waitUntil: 'networkidle' });
    await page.locator('input[type=file]').setInputFiles([
      {name:'partial-good.ppm',mimeType:'image/x-portable-pixmap',buffer:ppm},
      {name:'partial-corrupt.ppm',mimeType:'image/x-portable-pixmap',buffer:Buffer.from('not a PPM image')}
    ]);
    await page.getByLabel('Convert to').selectOption('pgm');
    await page.getByRole('button',{name:/Convert 2 files/}).click();
    await page.getByRole('link',{name:'Jobs & history'}).click();
    const partialBatch=page.locator('.batch-history-card').filter({hasText:'2 files'}).first();
    await partialBatch.waitFor({timeout:15000});
    await page.waitForFunction(()=>[...document.querySelectorAll('.batch-history-card')].some(card=>card.innerText.includes('1 done · 1 failed')),null,{timeout:60000});
    await partialBatch.locator(':scope > summary').click();
    await partialBatch.getByText('partial-corrupt.ppm',{exact:true}).waitFor();
    const partialID=(await partialBatch.locator(':scope > summary').innerText()).match(/ID ([0-9a-f-]{36})/)?.[1];assert.ok(partialID,'partial batch id is shown');
    const beforeRetry=await page.evaluate(async id=>fetch('/api/v1/batches/'+id).then(r=>r.json()),partialID);
    const successfulJobID=beforeRetry.items.find(item=>item.state==='succeeded')?.jobId;assert.ok(successfulJobID,'successful member has a job');
    assert.equal(beforeRetry.items.filter(item=>item.state==='failed').length,1,'invalid input is isolated to its own item');
    const failedJobID=beforeRetry.items.find(item=>item.state==='failed')?.jobId;assert.ok(failedJobID,'failed member has a job');
    await partialBatch.getByRole('button',{name:'Retry failed'}).click();
    await page.waitForFunction(async args=>{const b=await fetch('/api/v1/batches/'+args.id).then(r=>r.json());return b.failed===1&&b.succeeded===1&&b.items.some(item=>item.state==='failed'&&item.jobId!==args.failedID);},{id:partialID,failedID:failedJobID},{timeout:60000});
    const afterRetry=await page.evaluate(async id=>fetch('/api/v1/batches/'+id).then(r=>r.json()),partialID);
    assert.equal(afterRetry.items.find(item=>item.state==='succeeded').jobId,successfulJobID,'retry did not rerun successful work');

    // The cancellation-only fixture stays active while the browser renders
    // history, without changing the production worker or its isolation settings.
    await page.goto(base + '/convert', { waitUntil: 'networkidle' });
    await page.locator('input[type=file]').setInputFiles([
      {name:'cancel-one.ppm',mimeType:'image/x-portable-pixmap',buffer:ppm},
      {name:'cancel-two.ppm',mimeType:'image/x-portable-pixmap',buffer:ppm}
    ]);
    await page.getByLabel('Convert to').selectOption('pgm');
    await chooseAdvanced(page, 'ppm-pgm-cancel');
    await page.getByRole('button',{name:/Convert 2 files/}).click();
    await page.getByRole('status').filter({hasText:'Batch created: 2 item(s) submitted.'}).waitFor({timeout:15000});
    await page.getByRole('link',{name:'Jobs & history'}).click();
    const itemCancelID=await page.evaluate(()=>{
      const ids=JSON.parse(localStorage.getItem('101conv-batch-ids')||'[]');
      return ids.at(-1);
    });
    assert.match(itemCancelID,/^[0-9a-f-]{36}$/,'item-cancellation batch ID is persisted before cancellation');
    const itemCancelBatch=page.locator('.batch-history-card').filter({hasText:itemCancelID});
    await itemCancelBatch.waitFor({timeout:15000});
    await itemCancelBatch.locator(':scope > summary').click();
    await itemCancelBatch.getByText('cancel-one.ppm',{exact:true}).waitFor();
    await itemCancelBatch.getByRole('button',{name:'Cancel this file'}).first().click();
    await page.waitForFunction(async id=>{const b=await fetch('/api/v1/batches/'+id).then(r=>r.json());return b.cancelled===1&&b.succeeded===1;},itemCancelID,{timeout:60000});

    await page.goto(base + '/convert', { waitUntil: 'networkidle' });
    await page.locator('input[type=file]').setInputFiles([
      {name:'batch-cancel-one.ppm',mimeType:'image/x-portable-pixmap',buffer:ppm},
      {name:'batch-cancel-two.ppm',mimeType:'image/x-portable-pixmap',buffer:ppm}
    ]);
    await page.getByLabel('Convert to').selectOption('pgm');
    await chooseAdvanced(page, 'ppm-pgm-cancel');
    await page.getByRole('button',{name:/Convert 2 files/}).click();
    await page.getByRole('status').filter({hasText:'Batch created: 2 item(s) submitted.'}).waitFor({timeout:15000});
    await page.getByRole('link',{name:'Jobs & history'}).click();
    const wholeCancelID=await page.evaluate(()=>{
      const ids=JSON.parse(localStorage.getItem('101conv-batch-ids')||'[]');
      return ids.at(-1);
    });
    assert.match(wholeCancelID,/^[0-9a-f-]{36}$/,'new batch ID is persisted before cancellation');
    const wholeCancelBatch=page.locator('.batch-history-card').filter({hasText:wholeCancelID});
    await wholeCancelBatch.waitFor({timeout:15000});
    await wholeCancelBatch.getByRole('button',{name:'Cancel batch'}).click();
    await page.waitForFunction(async id=>{const b=await fetch('/api/v1/batches/'+id).then(r=>r.json());return b.queued===0&&b.running===0&&b.cancelled===2;},wholeCancelID,{timeout:60000});

    const hostileForm=new FormData();hostileForm.append('file',new Blob([ppm]),'../../escape.ppm');
    const hostileUploadResponse=await fetch(base+'/api/v1/uploads',{method:'POST',headers:{Authorization:'Bearer '+process.env.ENGINE_ADMIN_TOKEN},body:hostileForm});
    assert.equal(hostileUploadResponse.status,201,'hostile filename upload is accepted as a regular file');
    const hostileUpload=await hostileUploadResponse.json();
    const hostileBatchResponse=await admin('/batches','POST',{mode:'convert',idempotencyKey:'hostile-zip-test-001',items:[{uploadId:hostileUpload.id,targetFormat:'pgm'}]});
    const hostileBatch=(await hostileBatchResponse.json()).batch;
    await page.waitForFunction(async id=>{
      const b=await fetch('/api/v1/batches/'+id).then(r=>r.json());
      if(b.succeeded!==1||!b.items?.every(item=>item.job?.resultState==='available'))return false;
      const item=b.items[0];
      const result=await fetch('/api/v1/jobs/'+item.jobId+'/result');
      return result.ok;
    },hostileBatch.id,{timeout:60000});
    const adminHeaders={Authorization:'Bearer '+process.env.ENGINE_ADMIN_TOKEN};
    const adminBatchResponse=await fetch(base+'/api/v1/batches/'+hostileBatch.id,{headers:adminHeaders});
    const adminBatch=await adminBatchResponse.json();
    const adminItem=adminBatch.items?.[0];
    const adminResultResponse=adminItem?.jobId?await fetch(base+'/api/v1/jobs/'+adminItem.jobId+'/result',{headers:adminHeaders}):null;
    const safeZipResponse=await fetch(base+'/api/v1/batches/'+hostileBatch.id+'/results.zip',{headers:adminHeaders});
    const safeZipBody=Buffer.from(await safeZipResponse.arrayBuffer());
    assert.equal(safeZipResponse.status,200,'hostile-name batch ZIP is downloadable: '+JSON.stringify({batchStatus:adminBatchResponse.status,batchState:{state:adminBatch.state,succeeded:adminBatch.succeeded,items:adminBatch.items?.map(item=>({state:item.state,jobId:item.jobId,resultState:item.job?.resultState}))},resultStatus:adminResultResponse?.status,zipBody:safeZipBody.toString('utf8').slice(0,200)}));
    const safeZip=safeZipBody.toString('latin1');
    assert.match(safeZip,/item-001-[0-9a-f-]{36}\.pgm/,'ZIP uses a generated safe item name');
    assert.equal(safeZip.includes('../escape'),false,'ZIP does not contain a client-controlled path');

    // Exercise the actual worker pool, per-batch cap, and owner rotation using
    // real Core/Worker processes and the same converter used by the UI tests.
    const userCreate=await admin('/users','POST',{username:'smokeuser',password:'smoke-user-password-123',roleId:'converter',permissionOverrides:{},quotas:{maxFileBytes:{mode:'unlimited'},monthlyJobs:{mode:'unlimited'},monthlyBytes:{mode:'unlimited'}}});
    const smokeUser=(await userCreate.json()).user;assert.ok(smokeUser.id,'test account created through the admin API');
    async function uploadFor(token,name){const form=new FormData();form.append('file',new Blob([ppm]),name);const response=await fetch(base+'/api/v1/uploads',{method:'POST',headers:{Authorization:'Bearer '+token},body:form});assert.equal(response.status,201,'concurrency fixture upload is accepted');return (await response.json()).id;}
    const adminSlowUploads=await Promise.all(Array.from({length:4},(_,i)=>uploadFor(process.env.ENGINE_ADMIN_TOKEN,`admin-slow-${i+1}.ppm`)));
    const adminSlowResponse=await admin('/batches','POST',{mode:'convert',idempotencyKey:'admin-fairness-batch-0001',items:adminSlowUploads.map(uploadId=>({uploadId,profileId:'ppm-pgm-slow',targetFormat:'pgm'}))});
    assert.equal(adminSlowResponse.status,201,'admin submits a four-file slow batch');
    const adminSlowBatch=(await adminSlowResponse.json()).batch;
    const userContext=await browser.newContext({viewport:{width:1280,height:900}});const userPage=await userContext.newPage();
    userPage.on('pageerror',error=>errors.push(error.message));
    await userPage.goto(base,{waitUntil:'networkidle'});
    await userPage.getByRole('button', { name: 'Sign in', exact: true }).click();
    await userPage.getByRole('heading', { name: 'Sign in', exact: true }).waitFor();
    await userPage.getByLabel(/User name/).fill('smokeuser');
    await userPage.getByLabel('Password').fill('smoke-user-password-123');
    await userPage.getByRole('button',{name:/Sign in/}).click();
    await userPage.getByRole('heading',{name:'What are we converting?'}).waitFor();
    const userAccess=await userPage.evaluate(async id=>{
      const batch=await fetch('/api/v1/batches/'+id);const zip=await fetch('/api/v1/batches/'+id+'/results.zip');
      return {batchStatus:batch.status,zipStatus:zip.status};
    },adminSlowBatch.id);
    assert.ok(userAccess.batchStatus===403||userAccess.batchStatus===404,'another owner cannot view an admin batch');
    assert.ok(userAccess.zipStatus===403||userAccess.zipStatus===404,'another owner cannot download an admin ZIP');
    const userSlowBatch=await userPage.evaluate(async source=>{
      const uploads=[];
      for(let i=0;i<2;i++){const form=new FormData();form.append('file',new Blob([source]),`user-slow-${i+1}.ppm`);const up=await fetch('/api/v1/uploads',{method:'POST',body:form});if(up.status!==201)throw Error('user upload failed: '+up.status);uploads.push((await up.json()).id);}
      const response=await fetch('/api/v1/batches',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({mode:'convert',idempotencyKey:'user-fairness-batch-0001',items:uploads.map(uploadId=>({uploadId,profileId:'ppm-pgm-slow',targetFormat:'pgm'}))})});
      if(response.status!==201)throw Error('user batch submission failed: '+response.status+' '+await response.text());return (await response.json()).batch;
    },ppm.toString());
    let maxConcurrent=0,maxAdminBatchRunning=0,userServedBeforeAdminDone=false,adminState,userState;
    for(let i=0;i<100;i++){
      [adminState,userState]=await Promise.all([admin('/batches/'+adminSlowBatch.id,'GET'),admin('/batches/'+userSlowBatch.id,'GET')]);
      const a=(await adminState.json()).batch,u=(await userState.json()).batch;
      maxConcurrent=Math.max(maxConcurrent,a.running+u.running);
      maxAdminBatchRunning=Math.max(maxAdminBatchRunning,a.running);
      if(u.running>0&&a.succeeded<a.total)userServedBeforeAdminDone=true;
      if(a.succeeded+a.failed+a.cancelled===a.total&&u.succeeded+u.failed+u.cancelled===u.total)break;
      await new Promise(resolve=>setTimeout(resolve,200));
    }
    const finalAdmin=(await admin('/batches/'+adminSlowBatch.id,'GET')).batch;
    const finalUser=(await admin('/batches/'+userSlowBatch.id,'GET')).batch;
    assert.equal(maxConcurrent,3,'configured worker pool actually ran three conversions concurrently');
    assert.ok(maxAdminBatchRunning<=2,'a single batch never exceeded its configured two-slot limit');
    assert.ok(userServedBeforeAdminDone,'round-robin scheduling served another owner before the first owner batch completed');
    assert.equal(finalAdmin.succeeded,4,'admin concurrency fixture completed all conversions');
    assert.equal(finalUser.succeeded,2,'second owner concurrency fixture completed all conversions');
    const ownerJobIDs=new Set(finalAdmin.items.map(item=>item.jobId));
    const userJobs=await userPage.evaluate(async()=>fetch('/api/v1/jobs?limit=100').then(r=>r.json()));
    assert.equal(userJobs.jobs.some(job=>ownerJobIDs.has(job.id)),false,'second owner history omits another owner’s jobs');
    await userContext.close();
    await page.getByLabel('Search jobs').fill('ppm-pgm-grayscale');
    await page.getByLabel('Filter jobs by status').selectOption('succeeded');
    const row = page.locator('tr').filter({ hasText: 'ppm-pgm-grayscale' });
    try { await row.waitFor({ timeout: 15000 }); } catch (error) { console.error('job diagnostic',JSON.stringify({ids:await page.evaluate(()=>localStorage.getItem('101conv-job-ids')),body:(await page.locator('body').innerText()).slice(0,1200),apiResponses})); throw error; }
    await row.getByText('Succeeded', { exact: true }).waitFor({ timeout: 45000 });
    const retentionJobId = (await row.locator('.job-id').first().textContent()).match(/[0-9a-f]{8}-[0-9a-f-]{27,}/i)?.[0];
    assert.ok(retentionJobId, 'conversion job id is available for retention verification');
    if (process.env.SMOKE_OUTPUT_DIR) await fs.writeFile(process.env.SMOKE_OUTPUT_DIR + '/retention-job-id', retentionJobId);
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
    await chooseAdvanced(page, 'ppm-pgm-compress');
    await page.getByRole('button', { name: /Compress 1 file/ }).click();
    await page.getByText(/Compression batch created: 1 item\(s\) submitted/).waitFor({ timeout: 10000 });
    await page.getByRole('link', { name: 'Jobs & history' }).click();
    await page.getByLabel('Search jobs').fill('ppm-pgm-compress');
    await page.getByLabel('Filter jobs by status').selectOption('');
    const compressedRow = page.locator('tr').filter({ hasText: 'ppm-pgm-compress' });
    await compressedRow.waitFor({ timeout: 15000 });
    await compressedRow.getByText(/^Compression ·/).waitFor();
    try { await compressedRow.getByText('Succeeded', { exact: true }).waitFor({ timeout: 45000 }); } catch (error) { console.error('compression job diagnostic', (await compressedRow.innerText()).slice(0,1000)); throw error; }

    // Use a completely separate browser context to exercise the public guest
    // path after first-run setup. No admin bearer token or account cookie is
    // available to this context.
    const guestContext = await browser.newContext({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 1, isMobile: true, hasTouch: true, acceptDownloads: true });
    const guestPage = await guestContext.newPage();
    activePage = guestPage;
    guestPage.on('pageerror', error => errors.push(error.message));
    await guestPage.goto(base, { waitUntil: 'networkidle' });
    await guestPage.getByRole('heading', { name: 'What are we converting?' }).waitFor();
    await guestPage.getByRole('button', { name: 'Sign in' }).waitFor();
    assert.equal(await guestPage.locator('.login-card').count(), 0, 'the site root opens the conversion workspace instead of the sign-in page');
    assert.equal(await guestPage.getByRole('button', { name: 'Continue as guest' }).count(), 0, 'guest mode starts automatically without a confirmation step');
    assert.equal(await guestPage.locator('.guest-mode-indicator').count(), 1, 'anonymous visitor is labeled as guest');
    assert.equal(await guestPage.getByRole('link', { name: 'Users & access' }).count(), 0, 'guest navigation hides administration');
    assert.equal(await guestPage.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1), true, 'guest conversion view fits a phone');
    await guestPage.getByRole('button', { name: 'Sign in' }).click();
    await guestPage.getByRole('heading', { name: 'Sign in' }).waitFor();
    assert.equal(await guestPage.locator('.guest-continue').count(), 0, 'the sign-in page is reached only through the sign-in action');
    await guestPage.goto(base, { waitUntil: 'networkidle' });
    await guestPage.locator('.guest-mode-indicator').waitFor();
    await guestPage.locator('input[type=file]').setInputFiles([
      { name: 'guest-one.ppm', mimeType: 'image/x-portable-pixmap', buffer: ppm },
      { name: 'guest-two.pnm', mimeType: 'image/x-portable-anymap', buffer: ppm }
    ]);
    await guestPage.getByLabel('Convert to').selectOption('pgm');
    await guestPage.getByRole('button', { name: /Convert 2 files/ }).click();
    await guestPage.getByText(/Batch created: 2 item\(s\) submitted/).waitFor({ timeout: 10000 });
    await guestPage.getByRole('link', { name: 'Jobs' }).click();
    const guestBatch = guestPage.locator('.batch-history-card').filter({ hasText: '2 files' }).first();
    await guestBatch.waitFor({ timeout: 15000 });
    const guestBatchID = (await guestBatch.locator(':scope > summary').innerText()).match(/ID ([0-9a-f-]{36})/)?.[1];
    assert.ok(guestBatchID, 'guest batch is visible in this browser session');
    await guestPage.waitForFunction(async id => { const response = await fetch('/api/v1/batches/' + id); return response.ok && (await response.json()).batch.succeeded === 2; }, guestBatchID, { timeout: 60000 });
    await guestBatch.locator(':scope > summary').click();
    const guestBatchData = await guestPage.evaluate(async id => (await fetch('/api/v1/batches/' + id).then(r => r.json())).batch, guestBatchID);
    const guestJobID = guestBatchData.items.find(item => item.state === 'succeeded')?.jobId;
    assert.ok(guestJobID, 'guest batch retains individual job references');
    const guestResult = await guestPage.evaluate(async id => { const r = await fetch('/api/v1/jobs/' + id + '/result'); return { status: r.status, text: await r.text() }; }, guestJobID);
    assert.equal(guestResult.status, 200, 'guest can download its individual result');
    assert.match(guestResult.text, /^P2\n/);
    const [guestZip] = await Promise.all([guestPage.waitForEvent('download'), guestBatch.getByRole('button', { name: 'Download successful files (.zip)' }).click()]);
    assert.equal((await fs.readFile(await guestZip.path())).subarray(0, 2).toString(), 'PK', 'guest can download a ZIP of its own successful results');
    const foreignContext = await browser.newContext();
    const foreignPage = await foreignContext.newPage();
    await foreignPage.goto(base, { waitUntil: 'networkidle' });
    await foreignPage.getByRole('heading', { name: 'What are we converting?' }).waitFor();
    const foreignAccess = await foreignPage.evaluate(async id => {
      const batch = await fetch('/api/v1/batches/' + id);
      const zip = await fetch('/api/v1/batches/' + id + '/results.zip');
      const admin = await fetch('/api/v1/storage');
      return { batch: batch.status, zip: zip.status, admin: admin.status };
    }, guestBatchID);
    assert.equal(foreignAccess.batch, 404, 'another guest cannot enumerate or view the batch');
    assert.equal(foreignAccess.zip, 404, 'another guest cannot download a guest ZIP');
    assert.equal(foreignAccess.admin, 403, 'guest cannot read administrative storage');
    assert.equal(await foreignPage.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1), true, 'guest jobs layout has no mobile overflow');
    await foreignContext.close();
    await guestPage.getByRole('link', { name: 'Compress' }).click();
    await guestPage.getByRole('heading', { name: 'Make files smaller' }).waitFor();
    await guestPage.locator('input[type=file]').setInputFiles({ name: 'guest-compress.ppm', mimeType: 'image/x-portable-pixmap', buffer: ppm });
    await guestPage.getByLabel('Output format').selectOption('pgm');
    await chooseAdvanced(guestPage, 'ppm-pgm-compress');
    await guestPage.getByRole('button', { name: /Compress 1 file/ }).click();
    await guestPage.getByText(/Compression batch created: 1 item\(s\) submitted/).waitFor({ timeout: 10000 });
    await guestPage.getByRole('link', { name: 'Jobs' }).click();
    await guestPage.locator('.batch-history-card').filter({ hasText: 'Compression batch' }).first().waitFor({ timeout: 15000 });
    await guestContext.close();

    const mobile = await browser.newContext({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 1, isMobile: true, hasTouch: true });
    const mobilePage = await mobile.newPage();
    activePage = mobilePage;
    mobilePage.on('pageerror', error => errors.push(error.message));
    await mobilePage.goto(base + '/convert', { waitUntil: 'networkidle' });
    await mobilePage.getByRole('button', { name: 'Sign in', exact: true }).click();
    await mobilePage.getByRole('heading', { name: 'Sign in', exact: true }).waitFor();
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
    await mobilePage.goto(base + '/storage', { waitUntil: 'networkidle' });
    await mobilePage.getByRole('heading', { name: 'Storage', exact: true }).waitFor();
    assert.equal(await mobilePage.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1), true, 'mobile storage has no horizontal overflow');
    await mobilePage.goto(base + '/jobs', { waitUntil: 'networkidle' });
    await mobilePage.getByRole('heading',{name:'Jobs & history'}).waitFor();
    assert.equal(await mobilePage.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1), true, 'mobile batch history has no horizontal overflow');
    await mobilePage.getByRole('link', { name: 'Jobs' }).click();
    await mobilePage.getByRole('heading', { name: 'Jobs & history' }).waitFor();
    await mobile.close();
    await context.close();
    assert.deepEqual(errors, [], 'no uncaught browser errors');
    console.log('Browser verified setup, anonymous conversion/download/isolation on mobile, ownership, two-slot fair scheduling, mixed-format 20-file batches, retry/cancellation/ZIP, conversion/compression, retention, restart data, and mobile layouts.');
  } catch (error) {
    if (activePage && process.env.SMOKE_OUTPUT_DIR) {
      await fs.mkdir(process.env.SMOKE_OUTPUT_DIR, { recursive: true });
      await activePage.screenshot({ path: process.env.SMOKE_OUTPUT_DIR + '/failure.png', fullPage: true }).catch(() => {});
    }
    throw error;
  } finally {
    await browser.close();
  }
})().catch(error => { console.error(error); process.exit(1); });
