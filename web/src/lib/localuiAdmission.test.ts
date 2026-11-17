// @vitest-environment jsdom
import { readFileSync } from 'node:fs';
import { afterEach, expect, it, vi } from 'vitest';
const html = readFileSync('../edge/internal/localui/index.html', 'utf8');
const script = html.match(/<script>([\s\S]*)<\/script>/)![1];
const initial = () => ({ as_of: '2026-11-17T15:55:00Z', collection: { state: 'open', active: false, pending: 2 }, session: { state: 'open', sending: false, queue: { retained: 1, capacity: 2, full: false }, last_send: null } });
async function flush() { for (let i = 0; i < 12; i++) await Promise.resolve(); }
function page(enabled = true, data: unknown = initial()) {
  document.documentElement.innerHTML = html;
  document.body.dataset.admission = enabled ? 'enabled' : 'disabled';
  const fetcher = vi.fn(async (url: string, _options?: any) => ({ ok: true, json: async () => url === '/api/admission-status' ? data : {} }));
  const intervals = vi.fn();
  const api = new Function('fetch', 'setInterval', 'setTimeout', 'clearTimeout', 'AbortController', script + ';return {admission}')(
    fetcher, intervals, setTimeout, clearTimeout, AbortController);
  return { api, fetcher, intervals };
}
afterEach(() => { vi.useRealTimers(); document.documentElement.innerHTML = ''; });
it('keeps the panel and staged fetch/cadence absent by default', async () => {
  const p = page(false); await flush();
  expect(document.getElementById('admission')!.hidden).toBe(true);
  expect(p.fetcher.mock.calls.some(c => c[0] === '/api/admission-status')).toBe(false);
  expect(p.intervals.mock.calls.some(c => c[1] === 5000)).toBe(false);
});
it('distinguishes volatile pending, saved rows and process-local history', async () => {
  page(); await flush(); const text = document.getElementById('admission')!.textContent!;
  expect(text).toContain('In-memory pending: 2'); expect(text).toContain('Saved rows: 1 / 2');
  expect(text).toContain('Do not start a new poll'); expect(text).toContain('No send result in this process');
  expect(text).toContain('Closing the process can lose them');
});
it.each(['uncertain', 'admission-confirmed', 'idle', 'refused'])('renders last-call %s without implying delivery', async outcome => {
  const d = initial() as any; d.collection.pending = 0; d.session.last_send = { outcome, batch: { Attempted: 1, Reconciled: outcome === 'admission-confirmed' ? 1 : 0 } };
  page(true, d); await flush(); const text = document.getElementById('admission-detail')!.textContent!;
  expect(text).toContain(outcome === 'uncertain' ? 'server may already have committed' : outcome === 'admission-confirmed' ? 'not effect delivery' : outcome === 'idle' ? 'No rows selected' : 'No automatic retry');
  expect(text).toContain('Last call only: attempted 1');
});
it('does not show stale queue after closing, and preserves pending warning', async () => {
  const d = initial() as any; d.session = { state: 'closed' }; d.collection.state = 'stopped'; page(true, d); await flush();
  expect(document.getElementById('admission-detail')!.textContent).toContain('Saved row count unavailable');
  expect(document.getElementById('admission-detail')!.textContent).not.toContain('Saved rows:');
  expect(document.getElementById('admission-state')!.textContent).toContain('no new source reads');
});
it('clears prior counts/as-of on fetch failure and malformed evidence', async () => {
  const p = page(); await flush();
  p.fetcher.mockResolvedValueOnce({ ok: false, json: async () => initial() }); p.api.admission(); await flush();
  expect(document.getElementById('admission-detail')!.innerHTML).toBe(''); expect(document.getElementById('admission-asof')!.textContent).toBe('');
  const invalid = initial(); invalid.collection.pending = -1;
  p.fetcher.mockResolvedValueOnce({ ok: true, json: async () => invalid }); p.api.admission(); await flush();
  expect(document.getElementById('admission-state')!.textContent).toContain('unavailable');
  expect(document.getElementById('admission-detail')!.innerHTML).toBe('');
});
it('bounds a hung status request and refuses overlapping requests', async () => {
  vi.useFakeTimers(); const p = page(); await flush();
  p.fetcher.mockImplementationOnce((_url: string, options?: any) => new Promise((_resolve, reject) => options.signal.addEventListener('abort', () => reject(Error('timeout')))) as any);
  const before = p.fetcher.mock.calls.length; p.api.admission(); p.api.admission();
  expect(p.fetcher.mock.calls.length).toBe(before + 1); await vi.advanceTimersByTimeAsync(3001); await flush();
  expect(document.getElementById('admission-state')!.textContent).toContain('unavailable');
  expect(document.getElementById('admission-detail')!.innerHTML).toBe('');
});
