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

test('factory serial hint shows vendor-stated settings, never silently replaces edited values, applies only on click', async () => {
  await act(async () => root.render(<MemoryRouter><EdgeSetup /></MemoryRouter>));
  await act(async () => {});
  const tplSel = byOption('IME Conto D4-Pd');
  await setSel(tplSel, 'ime-conto-d4');
  // form keeps the generic/user values - no silent replacement
  const baudSel = selects().find((s) => [...s.options].some((o) => o.textContent === '19200'))!;
  expect(baudSel.value).toBe('9600');
  const paritySel = selects().find((s) => [...s.options].some((o) => o.textContent === 'even'))!;
  expect(paritySel.value).toBe('none');
  // hint names the factory settings and offers an explicit apply
  const hint = [...box.querySelectorAll('p')].find((p) => p.textContent?.includes('factory serial settings'))!;
  expect(hint.textContent).toContain('19200 baud');
  expect(hint.textContent).toContain('even parity');
  expect(hint.textContent).toContain('unit ID 5');
  const btn = [...hint.querySelectorAll('button')].find((b) => b.textContent === 'Apply factory settings')!;
  await act(async () => { btn.dispatchEvent(new MouseEvent('click', { bubbles: true })); });
  expect(baudSel.value).toBe('19200');
  expect(paritySel.value).toBe('even');
  const addrInput = [...box.querySelectorAll('input[type=number]')][0] as HTMLInputElement;
  expect(addrInput.value).toBe('5');
  // after applying, the hint says the form matches instead of offering the button
  const hint2 = [...box.querySelectorAll('p')].find((p) => p.textContent?.includes('factory serial settings'))!;
  expect(hint2.textContent).toContain('matches');
  expect([...hint2.querySelectorAll('button')].length).toBe(0);
  // an installer edit after applying is preserved - no snap-back
  await setSel(baudSel, '9600');
  expect(baudSel.value).toBe('9600');
  const hint3 = [...box.querySelectorAll('p')].find((p) => p.textContent?.includes('factory serial settings'))!;
  expect([...hint3.querySelectorAll('button')].length).toBe(1); // apply offered again, but never forced
  expect(baudSel.value).toBe('9600');
  // templates without a serial block show no hint
  await setSel(tplSel, 'schneider-pm8000');
  expect([...box.querySelectorAll('p')].some((p) => p.textContent?.includes('factory serial settings'))).toBe(false);
});
