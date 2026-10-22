// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import DeviceGroups from './DeviceGroups';
import { api } from '../lib/api';
vi.mock('../lib/api', () => ({ api: vi.fn(), download: vi.fn() }));
let root: Root; let box: HTMLDivElement;
beforeEach(() => { (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true; box = document.createElement('div'); document.body.append(box); root = createRoot(box); });
afterEach(async () => { await act(async () => root.unmount()); box.remove(); vi.clearAllMocks(); });

const groups = [
  { id: 'root', name: 'Plant', description: '', devices: 3, parent_id: null },
  { id: 'line', name: 'Line 1', description: '', devices: 2, parent_id: 'root' },
];

test('chips show the nesting path and changing a parent calls the parent endpoint', async () => {
  vi.mocked(api).mockImplementation((url: string, init?: any) => {
    if (url === '/v1/groups' && !init) return Promise.resolve(groups) as any;
    if (url.endsWith('/parent')) return Promise.resolve({}) as any;
    return Promise.resolve([]) as any;
  });
  await act(async () => root.render(<MemoryRouter><DeviceGroups selected="" onSelect={() => {}} /></MemoryRouter>));
  expect(box.textContent).toContain('Plant > Line 1 (2)');
  // open the manage panel
  const manage = [...box.querySelectorAll('button')].find(b => b.textContent === 'Manage groups')!;
  await act(async () => manage.click());
  const sel = box.querySelector('select[aria-label="Parent of Line 1"]') as HTMLSelectElement;
  expect(sel).toBeTruthy();
  expect(sel.value).toBe('root');
  await act(async () => {
    const set = Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype, 'value')!.set!;
    set.call(sel, ''); sel.dispatchEvent(new Event('change', { bubbles: true }));
  });
  const call = vi.mocked(api).mock.calls.find(c => String(c[0]).endsWith('/parent'))!;
  expect(call[0]).toBe('/v1/groups/line/parent');
  expect(JSON.parse((call[1] as any).body).parent_id).toBe('');
});
