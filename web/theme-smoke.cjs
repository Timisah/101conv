const { chromium } = require('playwright-core');
const assert = require('node:assert/strict');
const base = process.env.UI_BASE || 'http://127.0.0.1:4173';
const key = '101conv-theme-preference';

async function expectTheme(page, expected) {
  await page.waitForFunction(theme => document.documentElement.dataset.theme === theme, expected);
  const actual = await page.evaluate(() => ({
    theme: document.documentElement.dataset.theme,
    scheme: getComputedStyle(document.documentElement).colorScheme,
    color: document.querySelector('meta[name="theme-color"]').content,
    apple: document.querySelector('meta[name="apple-mobile-web-app-status-bar-style"]').content,
  }));
  assert.deepEqual(actual, {theme: expected, scheme: expected, color: expected === 'dark' ? '#192620' : '#ffffff', apple: expected === 'dark' ? 'black' : 'default'});
}
async function fixtures(page) {
  await page.route('**/readyz', r => r.fulfill({json:{status:'ready'}}));
  await page.route('**/api/v1/**', r => {
    const path = new URL(r.request().url()).pathname.replace('/api/v1','');
    const data = path === '/auth/session' ? {authenticated:true,role:'admin'}
      : path === '/setup/status' ? {completed:true,administratorCreated:true}
      : path === '/me' ? {user:{permissions:{}}}
      : path === '/me/api-keys' ? {keys:[]}
      : path === '/profiles' ? {profiles:[]}
      : {preferences:[]};
    return r.fulfill({json:data});
  });
}
(async () => {
  const browser = await chromium.launch({executablePath:process.env.CHROMIUM_PATH || '/usr/bin/chromium',args:['--no-sandbox','--disable-dev-shm-usage']});
  try {
    // Verify the head script even when React cannot load.
    for (const colorScheme of ['light','dark']) {
      const context = await browser.newContext({colorScheme});
      const page = await context.newPage();
      await page.route('**/assets/*.js', r => r.abort());
      await page.addInitScript(() => localStorage.setItem('101conv-theme','light'));
      await page.goto(base);
      await expectTheme(page,colorScheme);
      assert.equal(await page.locator('#root').innerHTML(),'');
      assert.equal(await page.evaluate(() => window.conv101Theme.getPreference()),'system');
      await context.close();
    }
    const context = await browser.newContext({colorScheme:'dark',viewport:{width:390,height:844}});
    const page = await context.newPage();
    const errors=[];page.on('pageerror',e=>errors.push(e.message));
    await fixtures(page);
    await page.goto(base+'/account');
    const settings = page.getByRole('region',{name:'Appearance'});
    const choice = settings.getByRole('combobox',{name:'Color theme'});
    await choice.waitFor();
    assert.equal(await choice.inputValue(),'system');
    await expectTheme(page,'dark');
    await page.emulateMedia({colorScheme:'light'});await expectTheme(page,'light');
    await choice.selectOption('dark');await expectTheme(page,'dark');
    await page.emulateMedia({colorScheme:'light'});await expectTheme(page,'dark');
    await page.reload();await expectTheme(page,'dark');
    assert.equal(await choice.inputValue(),'dark');
    await choice.selectOption('light');await page.emulateMedia({colorScheme:'dark'});await expectTheme(page,'light');
    await page.reload();await expectTheme(page,'light');
    await choice.selectOption('system');await expectTheme(page,'dark');
    await page.emulateMedia({colorScheme:'light'});await expectTheme(page,'light');
    const other = await context.newPage();await fixtures(other);await other.goto(base+'/account');
    await other.locator('#appearance').getByRole('combobox',{name:'Color theme'}).selectOption('dark');
    await expectTheme(page,'dark');assert.equal(await choice.inputValue(),'dark');
    await other.evaluate(k=>localStorage.setItem(k,'invalid'),key);await expectTheme(page,'light');
    assert.equal(await choice.inputValue(),'system');
    assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
    assert.deepEqual(errors,[]);
    await context.close();
    // Storage access must not prevent startup or changing the theme in this tab.
    const blocked = await browser.newPage({colorScheme:'dark'});
    await blocked.addInitScript(() => Object.defineProperty(window,'localStorage',{get(){throw new Error('Storage disabled');}}));
    await fixtures(blocked);await blocked.goto(base+'/account');await expectTheme(blocked,'dark');
    await blocked.locator('#appearance').getByRole('combobox',{name:'Color theme'}).selectOption('light');await expectTheme(blocked,'light');
    console.log('Passed: pre-React system theme, legacy migration, live OS changes, explicit preferences, reload persistence, cross-tab sync, mobile account layout, browser chrome metadata, and blocked storage.');
  } finally { await browser.close(); }
})().catch(error=>{console.error(error);process.exitCode=1;});
