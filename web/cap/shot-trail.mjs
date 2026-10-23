import puppeteer from 'puppeteer-core';
import {readFileSync} from 'fs';
const browser=await puppeteer.launch({executablePath:'/usr/bin/google-chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
for(const mode of ['desktop','mobile']){
 const page=await browser.newPage();await page.setViewport(mode==='mobile'?{width:390,height:844,isMobile:true,hasTouch:true}:{width:1440,height:1000});
 await page.evaluateOnNewDocument(tok=>{localStorage.setItem('iot.token',tok);localStorage.setItem('hexmon-tour-done','1')},readFileSync('/tmp/lab-token','utf8').trim());
 await page.goto('http://127.0.0.1:5173/map',{waitUntil:'networkidle0'});
 await page.select('#mt-device','itest-trail-dev');
 await page.waitForFunction(()=>document.body.textContent.includes('recorded samples shown.'));
 await page.screenshot({path:`/tmp/trail-${mode}.png`});
 console.log(mode,await page.evaluate(()=>({overflow:document.documentElement.scrollWidth>innerWidth,status:document.querySelector('[role="status"]')?.textContent,select:document.querySelector('#mt-device').getBoundingClientRect().toJSON()})));
 if(mode==='mobile'){await page.evaluate(()=>document.querySelector('svg[aria-label="Site map"]').scrollIntoView({block:'center'}));await page.screenshot({path:'/tmp/trail-mobile-map.png'})}
 await page.close();
}await browser.close();
