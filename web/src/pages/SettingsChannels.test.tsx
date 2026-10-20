// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import Settings from './Settings';
import { api } from '../lib/api';
vi.mock('../lib/api', () => ({ api: vi.fn(), download: vi.fn().mockResolvedValue(undefined) }));

let root: Root; let box: HTMLDivElement;
beforeEach(() => { (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true; box = document.createElement('div'); document.body.append(box); root = createRoot(box); });
afterEach(async () => { await act(async () => root.unmount()); box.remove(); vi.clearAllMocks(); });

const CH = { id: 'ch1', type: 'email', target: 'ops@example.com', enabled: false };

function mockApi(channels: any[] = [], testResult: any = { ok: true }) {
  vi.mocked(api).mockImplementation((url: string, opts?: any) => {
    if (url === '/v1/branding') return Promise.resolve({ product_name: 'HexThings', accent: '#000' }) as any;
    if (url === '/v1/notifications/channels' && !opts) return Promise.resolve(channels) as any;
    if (url === '/v1/notifications/channels' && opts?.method === 'POST') return Promise.resolve(CH) as any;
    if (url === '/v1/notifications/channels/ch1/test') return Promise.resolve(testResult) as any;
    return Promise.reject(new Error('not mocked: ' + url)) as any;
  });
}

async function renderPage() {
  await act(async () => root.render(<MemoryRouter><Settings /></MemoryRouter>));
  await act(async () => {});
}

test('no channel table until a channel exists', async () => {
  mockApi([]);
  await renderPage();
  const card = [...box.querySelectorAll('.card')].find(c => c.textContent!.includes('Notification channels'))!;
  expect(card.querySelector('table')).toBeNull();
});

test('adding a channel confirms it is off and names the next steps (Send test, then Enable)', async () => {
  mockApi([]);
  await renderPage();
  const input = box.querySelector('input[placeholder="ops@yourcompany.com"]') as HTMLInputElement;
  await act(async () => {
    Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!.call(input, 'ops@example.com');
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
  const form = box.querySelector('form')!;
  await act(async () => { form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })); });
  expect(vi.mocked(api)).toHaveBeenCalledWith('/v1/notifications/channels', expect.objectContaining({ method: 'POST' }));
  const msg = box.textContent!;
  expect(msg).toContain('Added email channel ops@example.com');
  expect(msg).toContain('It is off');
  expect(msg).toContain('Send test');
  expect(msg).toContain('Enable');
});

test('successful test shows the result inline on the channel row, not only in the shared line', async () => {
  mockApi([CH], { ok: true });
  await renderPage();
  const btn = [...box.querySelectorAll('button')].find(b => b.textContent === 'Send test')!;
  await act(async () => { btn.click(); });
  const row = box.querySelector('[data-testid="test-result-ch1"]')!;
  expect(row.textContent).toContain('Test sent');
  expect(row.textContent).toContain('Enable');
});

test('failed test shows the server error inline on the channel row', async () => {
  mockApi([CH], { ok: false, error: 'SMTP connection refused' });
  await renderPage();
  const btn = [...box.querySelectorAll('button')].find(b => b.textContent === 'Send test')!;
  await act(async () => { btn.click(); });
  const row = box.querySelector('[data-testid="test-result-ch1"]')!;
  expect(row.textContent).toContain('Test failed: SMTP connection refused');
});
