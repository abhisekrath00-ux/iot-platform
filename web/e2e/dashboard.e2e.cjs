// Browser e2e for dashboard drag-drop reorder and wall mode.
//
// NOT part of CI: it needs a running stack, a Chrome binary and puppeteer-core,
// none of which the CI job provides. Run it by hand against a dev stack:
//   npm i --no-save puppeteer-core
//   E2E_URL=http://localhost:5173 E2E_TOKEN=<admin jwt> E2E_BOARD='Widget demo' \
//   CHROME=/usr/bin/google-chrome node e2e/dashboard.e2e.cjs
// The board must have at least two widgets. The script never saves a layout.
const puppeteer = require('puppeteer-core');

const url = process.env.E2E_URL || 'http://localhost:5173';
const token = process.env.E2E_TOKEN;
const board = process.env.E2E_BOARD || 'Widget demo';
if (!token) { console.error('E2E_TOKEN required'); process.exit(2); }

const sleep = ms => new Promise(r => setTimeout(r, ms));
let failed = 0;
const check = (name, ok, detail = '') => { console.log(`${ok ? 'PASS' : 'FAIL'} ${name} ${ok ? '' : detail}`); if (!ok) failed++; };

(async () => {
  const b = await puppeteer.launch({ executablePath: process.env.CHROME || '/usr/bin/google-chrome', args: ['--no-sandbox'], headless: 'new' });
  const pg = await b.newPage();
  await pg.setViewport({ width: 1360, height: 860 });
  await pg.evaluateOnNewDocument(t => { localStorage.setItem('hexmon-tour-done', '1'); localStorage.setItem('iot.token', t); }, token);
  const click = async txt => { for (const x of await pg.$$('button')) { if ((await pg.evaluate(e => e.textContent, x)) === txt) { await x.click(); return true; } } return false; };
  const order = () => pg.$$eval('.board-item', els => els.map(e => e.innerText.split('\n')[0]));

  await pg.goto(`${url}/dashboards`, { waitUntil: 'networkidle0' });
  check('open board', await click(board));
  await sleep(1200);
  const before = await order();
  check('board has 2+ widgets', before.length >= 2, JSON.stringify(before));

  check('enter edit mode', await click('Edit'));
  await sleep(300);
  check('items draggable in edit mode', await pg.$$eval('.board-item', els => els.every(e => e.getAttribute('draggable') === 'true')));

  // Synthetic HTML5 drag events: drop the first widget onto the second.
  // React must re-render between events (drop reads state set by dragstart).
  const fire = (idx, type) => pg.evaluate((i, t) => {
    window.__dt = window.__dt || new DataTransfer();
    document.querySelectorAll('.board-item')[i].dispatchEvent(new DragEvent(t, { bubbles: true, cancelable: true, dataTransfer: window.__dt }));
  }, idx, type);
  await fire(0, 'dragstart'); await sleep(100);
  await fire(1, 'dragover'); await sleep(100);
  await fire(1, 'drop'); await sleep(100);
  await fire(0, 'dragend');
  await sleep(300);
  const after = await order();
  check('drag reorders widgets', after[0] === before[1] && after[1] === before[0], `${JSON.stringify(before)} -> ${JSON.stringify(after)}`);

  check('cancel edit', await click('Cancel'));
  await sleep(400);
  check('cancel restores order', JSON.stringify(await order()) === JSON.stringify(before));
  check('items not draggable after edit', await pg.$$eval('.board-item', els => els.every(e => e.getAttribute('draggable') !== 'true')));

  check('enter wall mode', await click('Wall mode'));
  await sleep(600);
  check('body has wall class', await pg.evaluate(() => document.body.classList.contains('wall')));
  check('navigation hidden in wall mode', await pg.evaluate(() => { const n = document.querySelector('nav'); return !n || getComputedStyle(n).display === 'none'; }));
  await pg.keyboard.press('Escape');
  await sleep(400);
  check('Escape exits wall mode', await pg.evaluate(() => !document.body.classList.contains('wall')));

  await b.close();
  console.log(failed ? `${failed} failed` : 'all passed');
  process.exit(failed ? 1 : 0);
})().catch(e => { console.error(e); process.exit(1); });
