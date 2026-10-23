import puppeteer from 'puppeteer-core';import {readFileSync} from 'fs';
const b=await puppeteer.launch({executablePath:'/usr/bin/google-chrome',headless:true,args:['--no-sandbox','--disable-dev-shm-usage']});
for(const mode of ['desktop','mobile']){
const p=await b.newPage();await p.setViewport(mode==='desktop'?{width:1440,height:1100}:{width:390,height:844,isMobile:true,hasTouch:true});
await p.evaluateOnNewDocument(tok=>{localStorage.setItem('iot.token',tok);localStorage.setItem('hexmon-tour-done','1')},readFileSync('/tmp/lab-token','utf8').trim());
await p.goto('http://127.0.0.1:5173/dashboards',{waitUntil:'networkidle0'});await p.evaluate(()=>[...document.querySelectorAll('button')].find(x=>x.textContent.trim()==='Plant aggregate trends')?.click());
await p.waitForFunction(()=>document.body.textContent.includes('of 2 current member devices'));await new Promise(r=>setTimeout(r,5000));
await p.screenshot({path:`/tmp/group-series-${mode}.png`});
if(mode==='mobile'){await p.evaluate(()=>[...document.querySelectorAll('.card')].find(x=>x.textContent.includes('hourly device means'))?.scrollIntoView({block:'start'}));await p.screenshot({path:'/tmp/group-series-mobile-chart.png'})}
await p.evaluate(()=>[...document.querySelectorAll('button')].find(x=>x.textContent.trim()==='Edit')?.click());
await p.waitForSelector('select[aria-label="Widget source"]');
await p.evaluate(()=>{const sels=[...document.querySelectorAll('select')];const type=sels.find(s=>[...s.options].some(o=>o.value==='timeseries'));type.value='timeseries';type.dispatchEvent(new Event('change',{bubbles:true}))});
await p.select('select[aria-label="Widget source"]','group');
await p.evaluate(()=>document.querySelector('select[aria-label="Widget source"]').scrollIntoView({block:'center'}));await p.screenshot({path:`/tmp/group-series-form-${mode}.png`});console.log(mode,await p.evaluate(()=>document.documentElement.scrollWidth>innerWidth));await p.close();}await b.close();
