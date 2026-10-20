// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import Settings from './Settings';
import { targetHint } from '../components/ChannelGuide';
import { api } from '../lib/api';
vi.mock('../lib/api', () => ({ api: vi.fn(), download: vi.fn().mockResolvedValue(undefined) }));

let root: Root; let box: HTMLDivElement;
beforeEach(() => { (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true; box = document.createElement('div'); document.body.append(box); root = createRoot(box); });
afterEach(async () => { await act(async () => root.unmount()); box.remove(); vi.clearAllMocks(); });

async function render(type: string) {
  vi.mocked(api).mockImplementation((url: string) => {
    if (url === '/v1/branding') return Promise.resolve({ product_name: 'HexThings', accent: '#000' }) as any;
    if (url === '/v1/notifications/channels') return Promise.resolve([]) as any;
    return Promise.reject(new Error('not mocked')) as any;
  });
  await act(async () => root.render(<MemoryRouter><Settings /></MemoryRouter>));
  await act(async () => {});
  const sel = box.querySelector('select')!;
  await act(async () => { sel.value = type; sel.dispatchEvent(new Event('change', { bubbles: true })); });
}

test('slack guide walks through app creation, operator-held token, invite and channel ID', async () => {
  await render('slack');
  const g = box.querySelector('[data-testid="guide-slack"]')!;
  expect(g.textContent).toContain('api.slack.com/apps');
  expect(g.textContent).toContain('chat:write');
  expect(g.textContent).toContain('SLACK_BOT_TOKEN');
  expect(g.textContent).toContain('Never paste the token into this page');
  expect(g.textContent).toContain('/invite');
  expect(g.textContent).toContain('Send test');
});

test('email guide names the operator env vars and one-address-per-channel', async () => {
  await render('email');
  const g = box.querySelector('[data-testid="guide-email"]')!;
  expect(g.textContent).toContain('SMTP_HOST');
  expect(g.textContent).toContain('ALERT_FROM_EMAIL');
  expect(g.textContent).toContain('again for each address');
});

test('teams and webhook guides require https webhook URLs', async () => {
  await render('teams');
  expect(box.querySelector('[data-testid="guide-teams"]')!.textContent).toContain('Incoming Webhook');
  await render('webhook');
  expect(box.querySelector('[data-testid="guide-webhook"]')!.textContent).toContain('HTTPS');
});

test('telegram keeps its own pairing flow (no generic guide)', async () => {
  await render('telegram');
  expect(box.querySelector('[data-testid="guide-telegram"]')).toBeNull();
  expect(box.textContent).toContain('Get pairing code');
});

test('target hints warn but never block', () => {
  expect(targetHint('slack', 'my-channel')).toContain('C, G or D');
  expect(targetHint('slack', 'C0123456789')).toBe('');
  expect(targetHint('email', 'ops@')).toContain('email address');
  expect(targetHint('email', 'ops@acme.com')).toBe('');
  expect(targetHint('teams', 'http://x')).toContain('https://');
  expect(targetHint('telegram', 'chat-with-me')).toContain('numeric');
  expect(targetHint('telegram', '-1001234567890')).toBe('');
  expect(targetHint('sms', '0491511')).toContain('+');
  expect(targetHint('sms', '+4915112345678')).toBe('');
  expect(targetHint('slack', '')).toBe(''); // empty stays silent
});
