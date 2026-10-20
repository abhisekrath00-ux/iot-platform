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

function mockApi(channels: any[] = [], testResult: any = { ok: true }, emailCfg = true) {
  vi.mocked(api).mockImplementation((url: string, opts?: any) => {
    if (url === '/v1/branding') return Promise.resolve({ product_name: 'HexThings', accent: '#000' }) as any;
    if (url === '/v1/notifications/channels' && !opts) return Promise.resolve(channels) as any;
    if (url === '/v1/notifications/channels' && opts?.method === 'POST') return Promise.resolve(CH) as any;
    if (url === '/v1/notifications/channels/ch1/test') return Promise.resolve(testResult) as any;
    if (url === '/v1/notifications/email/status') return Promise.resolve({ configured: emailCfg }) as any;
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

test('email type warns inline when SMTP is not configured on the deployment', async () => {
  mockApi([], { ok: true }, false);
  await renderPage();
  const note = box.querySelector('[data-testid="email-smtp-off"]');
  expect(note).not.toBeNull();
  expect(note!.textContent).toContain('not configured on this deployment');
  expect(note!.textContent).toContain('SMTP_HOST');
  expect(note!.textContent).toContain('ALERT_FROM_EMAIL');
  expect(note!.textContent).toContain('still add the channel');
});

test('no SMTP warning when the deployment email is configured', async () => {
  mockApi([], { ok: true }, true);
  await renderPage();
  expect(box.querySelector('[data-testid="email-smtp-off"]')).toBeNull();
});

test('SMTP warning disappears when switching to a non-email type', async () => {
  mockApi([], { ok: true }, false);
  await renderPage();
  expect(box.querySelector('[data-testid="email-smtp-off"]')).not.toBeNull();
  const sel = [...box.querySelectorAll('select')].find(s => s.querySelector('option[value="slack"]'))! as HTMLSelectElement;
  await act(async () => {
    Object.getOwnPropertyDescriptor(window.HTMLSelectElement.prototype, 'value')!.set!.call(sel, 'slack');
    sel.dispatchEvent(new Event('change', { bubbles: true }));
  });
  expect(box.querySelector('[data-testid="email-smtp-off"]')).toBeNull();
});
