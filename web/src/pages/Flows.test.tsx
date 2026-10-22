// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import Flows from './Flows';
import { api } from '../lib/api';
vi.mock('../lib/api', () => ({ api: vi.fn(), download: vi.fn().mockResolvedValue(undefined) }));
let root: Root; let box: HTMLDivElement;
beforeEach(() => { (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true; box = document.createElement('div'); document.body.append(box); root = createRoot(box); });
afterEach(async () => { await act(async () => root.unmount()); box.remove(); vi.clearAllMocks(); });

const groups = [{ id: 'g-comp', name: 'Compressors' }, { id: 'g-hvac', name: 'HVAC' }];
function mockWith(rules: any[]) {
  vi.mocked(api).mockImplementation((url: string, init?: any) => {
    if (url.startsWith('/v1/rules') && !init) return Promise.resolve(rules) as any;
    if (url === '/v1/rules' && init?.method === 'POST') return Promise.resolve({ id: 'r-new' }) as any;
    if (url === '/v1/flows') return Promise.resolve([]) as any;
    if (url === '/v1/notifications/channels') return Promise.resolve([]) as any;
    if (url === '/v1/assets') return Promise.resolve([]) as any;
    if (url === '/v1/groups') return Promise.resolve(groups) as any;
    return Promise.resolve([]) as any;
  });
}
const render = async () => act(async () => root.render(<MemoryRouter><Flows /></MemoryRouter>));

test('group picker lists groups and the create body carries group_id', async () => {
  mockWith([]);
  await render();
  const sel = box.querySelector('select[aria-label="Rule group"]') as HTMLSelectElement;
  expect(sel).toBeTruthy();
  expect([...sel.options].map(o => o.textContent)).toEqual(['No group scope', 'Compressors', 'HVAC']);
  await act(async () => {
    const set = Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype, 'value')!.set!;
    set.call(sel, 'g-comp'); sel.dispatchEvent(new Event('change', { bubbles: true }));
  });
  const nameI = box.querySelector('input[placeholder="High current on main meter"]') as HTMLInputElement;
  const pointI = box.querySelector('input[placeholder="current"]') as HTMLInputElement;
  const thrI = box.querySelector('input[placeholder="100"]') as HTMLInputElement;
  await act(async () => {
    const set = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!;
    for (const [el, v] of [[nameI, 'r1'], [pointI, 'temp'], [thrI, '50']] as const) {
      set.call(el, v); el.dispatchEvent(new Event('input', { bubbles: true }));
    }
  });
  const form = box.querySelector('form')!;
  await act(async () => { form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); });
  const call = vi.mocked(api).mock.calls.find(c => c[0] === '/v1/rules' && (c[1] as any)?.method === 'POST')!;
  expect(call).toBeTruthy();
  expect(JSON.parse((call[1] as any).body).definition.group_id).toBe('g-comp');
});

test('entering a single device disables the group picker with an explanation', async () => {
  mockWith([]);
  await render();
  const devI = box.querySelector('input[placeholder="meter-1"]') as HTMLInputElement;
  await act(async () => {
    const set = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!;
    set.call(devI, 'meter-1'); devI.dispatchEvent(new Event('input', { bubbles: true }));
  });
  const sel = box.querySelector('select[aria-label="Rule group"]') as HTMLSelectElement;
  expect(sel.disabled).toBe(true);
  expect(box.textContent).toContain('A group scope cannot be combined with a single device or an asset scope.');
});

test('a group-scoped rule row names the group', async () => {
  mockWith([{ id: 'r1', name: 'Compressor watch', enabled: true, version: 1, definition: { point_id: 'temp', op: '>', threshold: 50, severity: 'warning', group_id: 'g-comp' } }]);
  await render();
  expect(box.textContent).toContain('temp > 50 on group Compressors');
});

test('a rule with an unrecognized definition never renders undefined', async () => {
  mockWith([
    { id: 'r-old', name: 'Legacy rule', enabled: true, version: 1, definition: { point: 'power', value: 20 } },
    { id: 'r2', name: 'Plain threshold', enabled: true, version: 1, definition: { point_id: 'power', op: '>', threshold: 20, severity: 'warning', device_id: 'meter-1' } },
  ]);
  await render();
  expect(box.textContent).toContain('Unrecognized rule definition');
  expect(box.textContent).toContain('power > 20 on meter-1');
  expect(box.textContent).not.toContain('undefined');
});
