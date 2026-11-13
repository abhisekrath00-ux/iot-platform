// Opt-in child of TestRealBuiltEdgePackagesDownload. Real production build and
// handler; loopback proxy + synthetic shell identity, not owner login/install.
const http=require('node:http'),https=require('node:https'),fs=require('node:fs/promises'),path=require('node:path'),crypto=require('node:crypto');
const {chromium}=require('playwright');
(async()=>{
 const api=new URL(process.env.HEX_PACKAGE_TEST_API);if(api.protocol!=='https:'||api.hostname!=='127.0.0.1')throw Error('loopback fixture required');
 const root=path.resolve(__dirname,'../web/dist');const source=process.env.HEX_PACKAGE_SOURCE;const catalog=JSON.parse(await fs.readFile(path.join(source,'manifest.json')));const selected=catalog.packages.find(p=>p.id==='windows-amd64-exe');
 let corrupt=false,downloads=0,browser;
 const srv=http.createServer(async(req,res)=>{try{
  const pathname=new URL(req.url,'http://localhost').pathname;
  if(pathname.startsWith('/v1/edge-packages')){
   // The ephemeral fixture owns this TLS certificate, not an external host.
   const upstream=https.request(new URL(req.url,api),{rejectUnauthorized:false,headers:{Authorization:req.headers.authorization||''}},r=>{const chunks=[];r.on('data',b=>chunks.push(b));r.on('end',()=>{let b=Buffer.concat(chunks);if(corrupt&&pathname.endsWith('/windows-amd64-exe')&&r.statusCode===200){b=Buffer.from(b);b[Math.floor(b.length/2)]^=1;}res.writeHead(r.statusCode,r.headers);res.end(b);});});upstream.on('error',e=>{res.writeHead(502);res.end('fixture upstream failed')});upstream.end();return;
  }
  if(pathname.startsWith('/v1/')){let body=[];if(pathname==='/v1/me')body={id:'synthetic-package-viewer',role:'admin',tenant_id:'synthetic-package-tenant'};if(pathname==='/v1/branding')body={product_name:'HexThings',accent:'#0ea5e9'};res.writeHead(200,{'content-type':'application/json'});res.end(JSON.stringify(body));return;}
  let file=path.join(root,pathname);if(!file.startsWith(root+path.sep))file=path.join(root,'index.html');try{if(!(await fs.stat(file)).isFile())throw Error()}catch{file=path.join(root,'index.html')}
  const type=file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':file.endsWith('.svg')?'image/svg+xml':file.endsWith('.png')?'image/png':'text/html';res.writeHead(200,{'content-type':type});res.end(await fs.readFile(file));
 }catch(e){res.writeHead(500);res.end('fixture request failed')}});
 await new Promise(r=>srv.listen(0,'127.0.0.1',r));
 try{
  browser=await chromium.launch({executablePath:process.env.CHROME_EXECUTABLE||'/usr/bin/google-chrome',headless:true,args:['--no-sandbox']});
  const page=await browser.newPage({viewport:{width:1400,height:1000},acceptDownloads:true});
  await page.addInitScript(token=>{localStorage.setItem('iot.token',token);localStorage.setItem('hexmon-tour-done','1')},process.env.HEX_PACKAGE_TEST_TOKEN);
  await page.route('**/*',route=>{const u=new URL(route.request().url());return ['127.0.0.1','localhost'].includes(u.hostname)||u.protocol==='blob:'?route.continue():route.abort()});
  page.on('download',()=>downloads++);
  await page.goto(`http://127.0.0.1:${srv.address().port}/onboarding`);
  const card=page.locator('section[aria-labelledby="edge-downloads-title"]');await card.getByRole('button',{name:'Download Windows installer',exact:true}).waitFor();
  await card.scrollIntoViewIfNeeded();await card.screenshot({path:'/downloads/hexthings-real-package-desktop.png'});
  const pending=page.waitForEvent('download');await card.getByRole('button',{name:'Download Windows installer',exact:true}).click();const download=await pending;
  if(download.suggestedFilename()!==selected.filename)throw Error('wrong saved filename');const file=await download.path();const saved=await fs.readFile(file);if(saved.length!==selected.size||crypto.createHash('sha256').update(saved).digest('hex')!==selected.sha256)throw Error('saved bytes mismatch');await download.delete();
  // Change one response byte after the production handler verified it. A real
  // Chrome WebCrypto check, not mocked hashing, must prevent a second save.
  corrupt=true;await page.setViewportSize({width:390,height:844});await card.getByRole('button',{name:'Download Windows installer',exact:true}).click();await card.getByRole('alert').filter({hasText:'SHA256 changed. Nothing saved.'}).waitFor();
  if(downloads!==1)throw Error('corrupted bytes saved');if(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth))throw Error('mobile overflow');
  await card.getByRole('button',{name:'Download Windows installer',exact:true}).evaluate(el=>el.scrollIntoView({block:'center'}));await page.screenshot({path:'/downloads/hexthings-real-package-mobile-action.png'});await card.getByRole('alert').scrollIntoViewIfNeeded();await page.screenshot({path:'/downloads/hexthings-real-package-mobile-refusal.png'});
  console.log(JSON.stringify({result:'PASS',version:catalog.version,actualSavedBytes:selected.size,sha256:selected.sha256,successfulDownloads:downloads,tamperedSecondSave:false,desktop:'/downloads/hexthings-real-package-desktop.png',mobileAction:'/downloads/hexthings-real-package-mobile-action.png',mobile:'/downloads/hexthings-real-package-mobile-refusal.png',scope:'built dashboard + actual loopback TLS/JWT package handler; synthetic shell identity; no native install or equipment'}));
 }finally{if(browser)await browser.close();await new Promise(r=>srv.close(r));}
})().catch(e=>{console.error(e.message);process.exitCode=1});
