import puppeteer from 'puppeteer-core';
import { readFileSync } from 'fs';
const TOKEN = readFileSync('/tmp/lab-token', 'utf8').trim();
const browser = await puppeteer.launch({ executablePath: '/usr/bin/google-chrome', headless: 'new', args: ['--no-sandbox','--disable-dev-shm-usage'] });
const sleep = ms => new Promise(r => setTimeout(r, ms));
const setVal = (el, v) => {
  const proto = el.tagName === 'SELECT' ? HTMLSelectElement.prototype : HTMLInputElement.prototype;
  Object.getOwnPropertyDescriptor(proto, 'value').set.call(el, v);
  el.dispatchEvent(new Event(el.tagName === 'SELECT' ? 'change' : 'input', { bubbles: true }));
};

const page = await browser.newPage();
await page.setViewport({ width: 1440, height: 900 });
await page.evaluateOnNewDocument((tok) => { localStorage.setItem('iot.token', tok); localStorage.setItem('hexmon-tour-done','1'); }, TOKEN);
await page.goto('http://localhost:5173/map', { waitUntil: 'networkidle0', timeout: 45000 });
await sleep(1500);
await page.screenshot({ path: '/tmp/poly-baseline-desktop.png' });

// switch the zone form to polygon shape and start drawing
await page.evaluate((setValSrc) => {
  const setVal = eval('(' + setValSrc + ')');
  setVal(document.querySelector('#mz-shape'), 'polygon');
}, setVal.toString());
await sleep(300);
await page.evaluate(() => { [...document.querySelectorAll('button')].find(b => b.textContent.trim() === 'Draw on the map')?.click(); });
await sleep(300);

// click four corners around the Main Incomer dot (about +-30 viewBox units)
const clicks = await page.evaluate(() => {
  const svg = document.querySelector('svg[role="img"]');
  const dot = [...svg.querySelectorAll('circle')].find(c => c.querySelector('title')?.textContent.includes('Main Incomer'));
  const cx = parseFloat(dot.getAttribute('cx')), cy = parseFloat(dot.getAttribute('cy'));
  const r = svg.getBoundingClientRect();
  const kx = r.width / 760, ky = r.height / 460;
  const pts = [[-30, -28], [-30, 30], [34, 30], [34, -28]];
  return pts.map(([dx, dy]) => [r.left + (cx + dx) * kx, r.top + (cy + dy) * ky]);
});
for (const [x, y] of clicks) { await page.mouse.click(x, y); await sleep(250); }
await sleep(400);
await page.screenshot({ path: '/tmp/poly-draft-desktop.png' });
console.log('draft corners:', await page.evaluate(() => document.body.innerText.match(/\d+ corners placed\./)?.[0] || 'NONE'));

// name it and save
await page.evaluate((setValSrc) => {
  const setVal = eval('(' + setValSrc + ')');
  setVal(document.querySelector('#mz-name'), 'Yard');
}, setVal.toString());
await sleep(200);
await page.evaluate(() => { [...document.querySelectorAll('button')].find(b => b.textContent.trim() === 'Add zone')?.click(); });
await sleep(2000);
await page.screenshot({ path: '/tmp/poly-saved-desktop.png' });
console.log('zone list:', await page.evaluate(() => {
  const li = [...document.querySelectorAll('li')].map(l => l.innerText).find(t => t.includes('Yard'));
  return li ? li.split('\n').slice(0, 3).join(' | ') : 'NOT FOUND';
}));
console.log('incomer inside:', await page.evaluate(() => {
  const row = [...document.querySelectorAll('td')].find(td => td.textContent.includes('Main Incomer'))?.parentElement;
  return row ? row.innerText.replace(/\n/g, ' | ') : 'NO ROW';
}));
await page.close();

// mobile final view
const m = await browser.newPage();
await m.setViewport({ width: 390, height: 844, isMobile: true, hasTouch: true });
await m.evaluateOnNewDocument((tok) => { localStorage.setItem('iot.token', tok); localStorage.setItem('hexmon-tour-done','1'); }, TOKEN);
await m.goto('http://localhost:5173/map', { waitUntil: 'networkidle0', timeout: 45000 });
await sleep(1500);
await m.screenshot({ path: '/tmp/poly-mobile-top.png' });
await m.evaluate(() => { [...document.querySelectorAll('b')].find(b => b.textContent.trim() === 'Zones')?.scrollIntoView({ block: 'start' }); });
await sleep(600);
await m.screenshot({ path: '/tmp/poly-mobile-zones.png' });
await m.close();
await browser.close();
console.log('DONE');
