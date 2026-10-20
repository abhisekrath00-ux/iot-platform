// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import EdgeSetup from './EdgeSetup';
import { api } from '../lib/api';
vi.mock('../lib/api', () => ({ api: vi.fn(), download: vi.fn().mockResolvedValue(undefined) }));

let root: Root; let box: HTMLDivElement;
beforeEach(() => {
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  box = document.createElement('div'); document.body.append(box); root = createRoot(box);
  vi.mocked(api).mockImplementation((url: string) => {
    if (url === '/v1/sites') return Promise.resolve([{ id: 'site-1', name: 'Main site' }]) as any;
    if (url.startsWith('/v1/system/endpoints')) return Promise.resolve({ resolved: [], warnings: [] }) as any;
    return Promise.reject(new Error('not mocked: ' + url)) as any;
  });
});
afterEach(async () => { await act(async () => root.unmount()); box.remove(); vi.clearAllMocks(); });

const selects = () => [...box.querySelectorAll('select')] as HTMLSelectElement[];
const byOption = (text: string) => selects().find((s) => [...s.options].some((o) => o.textContent?.includes(text)))!;
const setSel = async (s: HTMLSelectElement, v: string) => { await act(async () => { s.value = v; s.dispatchEvent(new Event('change', { bubbles: true })); }); };

test('picking a template presets its native connection; manual override survives until the next template change', async () => {
  await act(async () => root.render(<MemoryRouter><EdgeSetup /></MemoryRouter>));
  await act(async () => {});
  const tplSel = byOption('Siemens SENTRON PAC2200');
  const connSel = selects().find((s) => [...s.options].some((o) => o.value === 'tcp'))!;
  expect(connSel.value).toBe('rtu'); // page default
  await setSel(tplSel, 'siemens-pac2200');
  expect(connSel.value).toBe('tcp'); // TCP-native template presets the connection
  await setSel(connSel, 'rtu'); // installer override (optional RS485 interface) is respected
  expect(connSel.value).toBe('rtu');
  await setSel(tplSel, 'schneider-iem3000');
  expect(connSel.value).toBe('rtu'); // RTU-only template presets back
  await setSel(tplSel, 'carlo-gavazzi-em24-e1');
  expect(connSel.value).toBe('tcp'); // Ethernet model presets tcp again
});
