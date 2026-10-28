// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import Reports from './Reports';
import { api } from '../lib/api';
vi.mock('../lib/api', () => ({ api: vi.fn(), download: vi.fn() }));
let box: HTMLDivElement, root: Root;
let pending: { resolve: (r: unknown) => void; reject: (e: Error) => void }[];
beforeEach(() => {
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  pending = [];
  vi.mocked(api).mockReset();
  vi.mocked(api).mockImplementation(async (path: string) => {
    if (path === '/v1/reports/preview') return new Promise((resolve, reject) => pending.push({ resolve, reject }));
    if (path === '/v1/me') return { role: 'admin' };
    if (path === '/v1/reports/options') return { sites: [], assets: [], devices: [] };
    return [];
  });
  box = document.createElement('div'); document.body.append(box); root = createRoot(box);
});
afterEach(async () => { await act(async () => root.unmount()); box.remove(); });
const button = (text: string) => [...box.querySelectorAll('button')].find(b => b.textContent === text)!;
const calls = () => vi.mocked(api).mock.calls.filter(c => c[0] === '/v1/reports/preview');
async function changeName(text: string) {
  await act(async () => {
    const input = box.querySelector('input[placeholder="Weekly energy summary"]')!;
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, text);
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
}
test('pending preview blocks duplicate requests and cancellation ignores a late success', async () => {
  await act(async () => root.render(<Reports />));
  await act(async () => { button('Preview').click(); button('Preview').click(); });
  expect(calls()).toHaveLength(1);
  expect(button('Building preview...').disabled).toBe(true);
  expect(box.textContent).toContain('No report is saved or sent');
  await act(async () => button('Cancel preview').click());
  await act(async () => pending[0].resolve({ html: '<h1>obsolete</h1>', rows: 2 }));
  expect(box.querySelector('iframe')).toBeNull();
  expect(button('Preview').disabled).toBe(false);
});
test('draft edits clear a preview and old results/errors cannot replace a newer snapshot', async () => {
  await act(async () => root.render(<Reports />));
  await act(async () => button('Preview').click());
  await changeName('New draft');
  await act(async () => button('Preview').click());
  expect(JSON.parse(calls()[1][1]!.body as string).name).toBe('New draft');
  await act(async () => pending[1].resolve({ html: '<h1>new</h1>', rows: 0 }));
  expect(box.querySelector('iframe')!.getAttribute('srcdoc')).toContain('new');
  expect(box.textContent).toContain('(0 rows)');
  expect(box.querySelector('iframe')!.getAttribute('sandbox')).toBe('');
  await act(async () => pending[0].reject(Error('obsolete failure')));
  expect(box.textContent).not.toContain('obsolete failure');
  expect(box.querySelector('iframe')).not.toBeNull();
  await changeName('Changed again');
  expect(box.querySelector('iframe')).toBeNull();
  expect(box.textContent).not.toContain('Snapshot of this draft');
});
test('failed or incomplete refresh clears old HTML and permits retry, with no save or delivery', async () => {
  await act(async () => root.render(<Reports />));
  await act(async () => button('Preview').click());
  await act(async () => pending[0].resolve({ html: '<h1>first</h1>', rows: 1 }));
  await act(async () => button('Preview').click());
  expect(box.querySelector('iframe')).toBeNull();
  await act(async () => pending[1].reject(Error('503: synthetic unavailable')));
  expect(box.querySelector('[role="alert"]')!.textContent).toContain('Preview failed');
  expect(box.querySelector('iframe')).toBeNull();
  await act(async () => button('Preview').click());
  await act(async () => pending[2].resolve({ html: '', rows: 0 }));
  expect(box.textContent).toContain('incomplete preview');
  expect(vi.mocked(api).mock.calls.filter(c => c[1]?.method)).toHaveLength(3);
  expect(calls()).toHaveLength(3);
});

test('edit binds version, blocks duplicate pending saves and disables draft changes', async () => {
  let finish!: (r: unknown) => void;
  const saved = { id: 'r1', name: 'Stored draft', version: 7, definition: { metrics: [], window_hours: 24, group_by: 'hour' } };
  const reads = vi.mocked(api).getMockImplementation()!;
  vi.mocked(api).mockImplementation(async (p: string, init?: RequestInit) => {
    if (p === '/v1/reports' && !init) return [saved];
    if (init?.method === 'PUT') return new Promise(resolve => { finish = resolve; });
    return reads(p, init);
  });
  window.scrollTo = vi.fn();
  await act(async () => root.render(<Reports />));
  await act(async () => button('Edit').click());
  expect(box.textContent).toContain('Edit report (version 7)');
  const form = box.querySelector('form')!;
  await act(async () => { form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); });
  const writes = vi.mocked(api).mock.calls.filter(c => c[1]?.method === 'PUT');
  expect(writes).toHaveLength(1);
  expect(writes[0][1]!.headers).toEqual({ 'If-Match': '7' });
  expect(box.querySelector('fieldset')!.disabled).toBe(true);
  expect(button('Saving...').disabled).toBe(true);
  await act(async () => finish({ id: 'r1', version: 8 }));
  expect(box.textContent).toContain('Report saved');
  expect(box.textContent).not.toContain('Edit report (version');
});

test('uncertain save preserves draft and blocks blind retry; missing edit version never writes', async () => {
  const saved = { id: 'r1', name: 'Stored draft', version: 3, definition: { metrics: [], window_hours: 24, group_by: 'hour' } };
  const reads = vi.mocked(api).getMockImplementation()!;
  let missing = false;
  vi.mocked(api).mockImplementation(async (p: string, init?: RequestInit) => {
    if (p === '/v1/reports' && !init) return [{ ...saved, version: missing ? undefined : 3 }];
    if (init?.method === 'PUT') throw Error('network unavailable');
    return reads(p, init);
  });
  window.scrollTo = vi.fn();
  await act(async () => root.render(<Reports />));
  await act(async () => button('Edit').click());
  await changeName('Keep my draft');
  await act(async () => box.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })));
  expect(box.textContent).toContain('outcome may be uncertain');
  expect((box.querySelector('input[placeholder="Weekly energy summary"]') as HTMLInputElement).value).toBe('Keep my draft');
  expect(button('Save changes (new version)').disabled).toBe(true);
  await act(async () => box.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })));
  expect(vi.mocked(api).mock.calls.filter(c => c[1]?.method === 'PUT')).toHaveLength(1);
  missing = true;
  await act(async () => button('Reload reports to check').click());
  await act(async () => button('Edit').click());
  expect(box.textContent).toContain('version unavailable');
  expect(button('Save changes (new version)').disabled).toBe(true);
  await act(async () => box.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })));
  expect(box.textContent).toContain('No version is available');
  expect(vi.mocked(api).mock.calls.filter(c => c[1]?.method === 'PUT')).toHaveLength(1);
});


test('cancel edit clears inherited delivery and report content before a new draft', async () => {
 const saved = { id: 'r1', name: 'Stored', version: 2, channel_id: 'c1', schedule_cron: '0 8 * * *', definition: { metrics: [{device_id:'d',point_id:'p'}], window_hours: 168, group_by: 'day' } };
 const reads = vi.mocked(api).getMockImplementation()!;
 vi.mocked(api).mockImplementation(async (p: string, i?: RequestInit) => p === '/v1/reports' && !i ? [saved] : p === '/v1/notifications/channels' ? [{id:'c1',type:'email',target:'synthetic@example.invalid'}] : reads(p,i));
 window.scrollTo=vi.fn();
 await act(async()=>root.render(<Reports/>));
 await act(async()=>button('Edit').click());
 await act(async()=>button('Cancel edit').click());
 await changeName('Fresh draft');
 await act(async()=>button('Preview').click());
 const draft=JSON.parse(calls()[0][1]!.body as string);
 expect(draft.definition.window_hours).toBe(24);expect(draft.definition.metrics).toEqual([]);
 const channel=[...box.querySelectorAll('select')].find(s=>[...s.options].some(o=>o.value==='c1'))!;
 expect(channel.value).toBe('');
 expect((box.querySelector('input[placeholder="custom: 0 6 * * 1"]') as HTMLInputElement).value).toBe('');
});
