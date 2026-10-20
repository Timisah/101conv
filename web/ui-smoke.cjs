// Fixture-backed browser checks for layout and interactions. Real conversions
// remain covered by the separate Docker browser suite.
const {chromium}=require('playwright-core');
const assert=require('node:assert/strict');
const fs=require('node:fs/promises');
const base=process.env.UI_BASE||'http://127.0.0.1:5173';
const out=process.env.UI_ARTIFACT_DIR||'/tmp/101conv-ui';
const admin={authenticated:true,username:'Alex',role:'admin',canConvert:true,canCompress:true,canReadJobs:true};
const profiles=['conversion','compression'].map(type=>({definition:{id:'image-'+type,type,name:type==='conversion'?'Image converter':'Image compressor',version:'1.0.0',description:'Image processing with your installed engine.',engines:[],...(type==='compression'?{compression:{options:[{key:'quality',label:'Quality',type:'number',default:'85',min:1,max:100}]}}:{}),rules:[{sources:['png','jpg','webp','avif'],targets:['webp','png'],...(type==='compression'?{compressionArguments:['--quality']}: {})}]},state:{enabled:true}}));
const providers=profiles.map((profile,index)=>({id:profile.definition.id,profileId:profile.definition.id,version:'1.0.0',name:profile.definition.name,engineId:'fixture',enabled:true,installed:true,available:true,capabilities:[{capabilityId:(index?'compress':'convert')+':png:webp',options:profile.definition.compression?.options||[]}]}));
const capabilities=['convert','compress'].flatMap(operation=>['png','jpg','webp','avif'].flatMap(source=>['png','webp'].map(target=>({id:operation+':'+source+':'+target,operation,sourceFormat:source,targetFormat:target,providerIds:[providers[operation==='convert'?0:1].id]}))));
const presets=[{id:'preset-convert',name:'Quick WebP',description:'Fixture preset',mode:'convert',sourceFormat:'png',targetFormat:'webp',profileId:'image-conversion',profileVersion:'1.0.0',options:{},shared:false,default:true,compatible:true,createdAt:'2026-10-20T08:00:00Z',updatedAt:'2026-10-20T08:00:00Z'}];
const jobs=['succeeded','running','failed'].map((state,i)=>({id:'job-00000000-0000-0000-0000-00000000000'+i,profileId:'image-conversion',profileVersion:'1.0.0',sourceFormat:'png',targetFormat:'webp',state,progress:0,attempt:1,maxAttempts:3,outputSize:23456,originalState:'available',resultState:state==='succeeded'?'available':'not_ready',error:state==='failed'?'Invalid image file':undefined,createdAt:'2026-10-20T08:00:00Z',updatedAt:'2026-10-20T08:00:00Z'}));
async function fixture(page,session=admin){
 await page.route('**/readyz',r=>r.fulfill({json:{status:'ready',version:'0.1.0'}}));
 await page.route('**/api/v1/**',r=>{
  const url=new URL(r.request().url()),path=url.pathname.replace('/api/v1','');
  if(path==='/auth/session')return r.fulfill(r.request().method()==='POST'?{status:401,json:{error:{message:'Incorrect username or password.'}}}:{json:session});
  if(path==='/auth/guest-session')return r.fulfill({status:403,json:{error:{code:'guest_disabled',message:'Guest access disabled'}}});
  if(path==='/setup/status')return r.fulfill({json:{completed:true,administratorCreated:true}});
  if(path==='/system/update')return r.fulfill({json:{currentVersion:'0.1.0',latestVersion:'0.2.0',updateAvailable:true,status:'update_available',releaseName:'Release 0.2.0',releaseUrl:'https://github.com/Timisah/101conv/releases/tag/v0.2.0',releaseNotes:'Fixture release notes',checkedAt:'2026-10-20T08:00:00Z'}});
  if(path==='/profiles')return r.fulfill({json:{profiles}});
  if(path==='/capabilities')return r.fulfill({json:{schemaVersion:1,capabilities,providers}});
  if(path.startsWith('/presets'))return r.fulfill({json:{presets}});
  if(path.startsWith('/profile-preferences')){const mode=url.searchParams.get('mode')||'convert',profile=profiles[mode==='convert'?0:1];return r.fulfill({json:{preferences:['png','jpg','webp','avif'].flatMap(sourceFormat=>['png','webp'].map(targetFormat=>({mode,sourceFormat,targetFormat,effectiveProfileId:profile.definition.id,effectiveSource:'automatic',alternatives:[{id:profile.definition.id,name:profile.definition.name,version:'1.0.0'}]})))}});}
  if(path==='/jobs')return r.fulfill({json:{jobs:jobs.filter(j=>!url.searchParams.get('status')||j.state===url.searchParams.get('status')),total:3}});
  if(path.startsWith('/jobs/'))return r.fulfill({json:{job:jobs[0]}});
  if(path==='/engines')return r.fulfill({json:{engines:[]}});
  if(path==='/batches'&&r.request().method()==='POST')return r.fulfill({json:{batch:{id:'batch-1',total:1,succeeded:0,queued:1,running:0,failed:0,cancelled:0,items:[]}}});
  if(path==='/batches')return r.fulfill({json:{batches:[]}});
  if(path==='/uploads')return r.fulfill({json:{id:'upload-1'}});
  return r.fulfill({json:{}});
 });
}
async function noOverflow(page,label){const s=await page.evaluate(()=>({width:innerWidth,actual:document.documentElement.scrollWidth}));if(s.actual>s.width+1)console.log(await page.locator('body *').evaluateAll(es=>es.filter(e=>e.getBoundingClientRect().right>innerWidth+1&&getComputedStyle(e).position!=='fixed').slice(0,15).map(e=>({tag:e.tagName,cls:e.className,width:e.getBoundingClientRect().width,right:e.getBoundingClientRect().right}))));assert.ok(s.actual<=s.width+1,label+': horizontal overflow '+JSON.stringify(s));}
const file=name=>({name,mimeType:'image/png',buffer:Buffer.from('image')});
(async()=>{
 await fs.mkdir(out,{recursive:true});
 const browser=await chromium.launch({executablePath:process.env.CHROMIUM_PATH||'/usr/bin/chromium',args:['--no-sandbox','--disable-dev-shm-usage']});
 const errors=[];let activePage;
 try{
  const page=await browser.newPage({viewport:{width:1440,height:1000},reducedMotion:'reduce'});activePage=page;page.on('pageerror',e=>errors.push(e.message));await fixture(page);
  await page.goto(base+'/convert');await page.getByRole('heading',{name:'What are we converting?'}).waitFor();assert.equal(await page.getByText('v0.1.0',{exact:true}).count(),1,'current version is visible');await page.screenshot({path:out+'/desktop-convert.png',fullPage:true});
  await page.locator('input[type=file]').setInputFiles(file('sample.png'));await page.getByLabel('Convert to',{exact:true}).selectOption('webp');await page.getByLabel('Saved presets').selectOption('preset-convert');assert.equal(await page.getByLabel('Saved presets').inputValue(),'preset-convert');await page.locator('details.advanced-provider').first().locator('summary').click();assert.equal(await page.getByLabel('Profile for this job').inputValue(),'image-conversion');assert.equal(await page.getByRole('button',{name:'Convert 1 file',exact:true}).isEnabled(),true);await page.screenshot({path:out+'/desktop-selected.png',fullPage:true});await page.getByRole('button',{name:'Convert 1 file',exact:true}).click();await page.getByRole('link',{name:'View results',exact:true}).waitFor();
  for(const width of [1440,1024,820,768,760,390,320]){await page.setViewportSize({width,height:900});for(const route of ['convert','compress','jobs','converters']){await page.goto(base+'/'+route,{waitUntil:'domcontentloaded'});await page.locator('h1').waitFor({timeout:15000});await page.waitForTimeout(150);await noOverflow(page,width+' '+route);if(width===390||width===1440)await page.screenshot({path:`${out}/${width}-${route}.png`,fullPage:true});}}
  await page.setViewportSize({width:390,height:844});await page.goto(base+'/jobs');await page.getByRole('button',{name:'More navigation options'}).click();await page.getByRole('menuitem',{name:'Storage',exact:true}).waitFor();await noOverflow(page,'mobile menu');await page.keyboard.press('Escape');assert.equal(await page.getByRole('menuitem',{name:'Storage',exact:true}).count(),0);
  await page.goto(base+'/admin');await page.getByRole('heading',{name:'Application updates'}).waitFor();await page.getByText('Version 0.2.0 is available',{exact:true}).waitFor();await noOverflow(page,'mobile admin update panel');
  await page.goto(base+'/convert');await page.getByRole('combobox',{name:'Color theme'}).selectOption('dark');assert.equal(await page.locator('html').getAttribute('data-theme'),'dark');await page.screenshot({path:out+'/mobile-dark.png',fullPage:true});
  await page.locator('.dropzone').dispatchEvent('dragenter');await page.locator('.upload-icon').dispatchEvent('dragenter');await page.locator('.upload-icon').dispatchEvent('dragleave');assert.match(await page.locator('.dropzone').getAttribute('class'),/dragging/);await page.locator('.dropzone').dispatchEvent('dragleave');assert.doesNotMatch(await page.locator('.dropzone').getAttribute('class'),/dragging/);
  await page.locator('input[type=file]').setInputFiles(file('a-long-image-filename-for-layout-verification.png'));await page.getByLabel('Convert to',{exact:true}).selectOption('webp');await page.screenshot({path:out+'/mobile-selected.png',fullPage:true});await page.setViewportSize({width:320,height:844});await noOverflow(page,'small phone selection');
  const guest=await browser.newPage({viewport:{width:390,height:844}});guest.on('pageerror',e=>errors.push(e.message));await fixture(guest,{...admin,guest:true,role:'guest',guestMaxBatchFiles:2});await guest.goto(base+'/convert');await guest.locator('input[type=file]').setInputFiles(['a.png','b.png','c.png'].map(file));await guest.screenshot({path:out+'/guest-selected.png',fullPage:true});await guest.getByRole('alert').waitFor();assert.equal(await guest.locator('.file-row').count(),2);await noOverflow(guest,'guest selection');await guest.locator('input[type=file]').setInputFiles({...file('empty.png'),buffer:Buffer.alloc(0)});await guest.getByText('empty.png is empty.',{exact:false}).waitFor();
  const login=await browser.newPage({viewport:{width:390,height:844}});login.on('pageerror',e=>errors.push(e.message));await fixture(login,{authenticated:false});await login.goto(base+'/login');await login.getByLabel('Password',{exact:true}).fill('wrong-password');await login.getByRole('button',{name:'Sign in',exact:true}).click();await login.getByRole('alert').waitFor();await noOverflow(login,'login');
  assert.deepEqual(errors,[]);console.log('Passed: 28 responsive layouts, upload/submit, guest limits, mobile menu, theme, nested drag targets, and sign-in errors. Screenshots: '+out);
 }catch(error){if(activePage){console.error('Browser errors:',errors,'URL:',activePage.url(),'Content:',(await activePage.locator('body').innerText()).slice(0,1200));await activePage.screenshot({path:out+'/failure.png',fullPage:true}).catch(()=>{});}throw error;}finally{await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
