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
