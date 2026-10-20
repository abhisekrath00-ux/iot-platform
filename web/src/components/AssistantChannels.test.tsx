// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import AssistantChannels from './AssistantChannels';
import { api } from '../lib/api';
vi.mock('../lib/api', () => ({ api: vi.fn(), download: vi.fn().mockResolvedValue(undefined) }));
let root: Root; let box: HTMLDivElement;
beforeEach(() => { (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true; box = document.createElement('div'); document.body.append(box); root = createRoot(box); });
afterEach(async () => { await act(async () => root.unmount()); box.remove(); vi.clearAllMocks(); });

function mockAdmin() {
  vi.mocked(api).mockImplementation((url: string) => {
    if (url === '/v1/assistant/channel-settings') return Promise.resolve({ slack_enabled: false, email_enabled: false, has_slack_secret: false, has_email_secret: false, secrets_available: true, slack_url: '/v1/assistant/inbound/slack/t1', email_url: '/v1/assistant/inbound/email/t1' }) as any;
    if (url === '/v1/assistant/links') return Promise.resolve([]) as any;
    return Promise.reject(new Error('not mocked')) as any;
  });
}

test('admin sees guided Slack setup steps with the live inbound URL', async () => {
  mockAdmin();
  await act(async () => root.render(<AssistantChannels />));
  const t = box.textContent!;
  expect(t).toContain('Slack setup steps');
  expect(t).toContain('/v1/assistant/inbound/slack/t1');
  expect(t).toContain('Signing Secret');
  expect(t).not.toContain('about 10 minutes');
  expect(t).not.toContain('docs/assistant-slack-email-setup.md');
  expect(t).toContain('message.im');
  expect(t).toContain('SLACK_BOT_TOKEN');
  expect(t).toContain('Copy member ID');
  expect(t).toContain('Only direct-message events are accepted');
});

test('admin sees guided email setup steps with the contract and the live inbound URL', async () => {
  mockAdmin();
  await act(async () => root.render(<AssistantChannels />));
  const t = box.textContent!;
  expect(t).toContain('Email setup steps');
  expect(t).toContain('/v1/assistant/inbound/email/t1');
  expect(t).toContain('HMAC-SHA256');
  expect(t).toContain('dkim and spf pass');
  expect(t).toContain('gateway itself is not part of the install');
});

test('non-admin sees no setup steps or settings', async () => {
  vi.mocked(api).mockImplementation((url: string) => {
    if (url === '/v1/assistant/channel-settings') return Promise.reject(new Error('forbidden')) as any;
    if (url === '/v1/assistant/links') return Promise.resolve([]) as any;
    return Promise.reject(new Error('not mocked')) as any;
  });
  await act(async () => root.render(<AssistantChannels />));
  expect(box.textContent).not.toContain('Slack setup steps');
  expect(box.textContent).not.toContain('Save channels');
});

test('admin can generate a signed self-test command from the stored secret', async () => {
  vi.mocked(api).mockImplementation((url: string) => {
    if (url === '/v1/assistant/channel-settings') return Promise.resolve({ slack_enabled: true, email_enabled: true, has_slack_secret: true, has_email_secret: true, secrets_available: true, slack_url: '/v1/assistant/inbound/slack/t1', email_url: '/v1/assistant/inbound/email/t1' }) as any;
    if (url === '/v1/assistant/links') return Promise.resolve([]) as any;
    if (url === '/v1/assistant/selftest/email') return Promise.resolve({ kind: 'email', curl_valid: 'curl -i -X POST ... -H "X-Signature: ab12"', curl_tampered: 'curl -i -X POST ... tampered', expect_ok: 'HTTP 200 expected', expect_tampered: 'HTTP 401 expected', expires: 'valid 5 minutes', note: 'Self-test only.' }) as any;
    return Promise.reject(new Error('not mocked')) as any;
  });
  await act(async () => root.render(<AssistantChannels />));
  const btn = Array.from(box.querySelectorAll('button')).find(b => b.textContent === 'Check email signing')!;
  expect(btn).toBeTruthy();
  await act(async () => btn.click());
  expect(box.textContent).toContain('HTTP 200 expected');
  expect(box.querySelector('pre[aria-label="Valid signed self-test command"]')!.textContent).toContain('X-Signature');
  expect(box.querySelector('pre[aria-label="Tampered self-test command"]')!.textContent).toContain('tampered');
});
