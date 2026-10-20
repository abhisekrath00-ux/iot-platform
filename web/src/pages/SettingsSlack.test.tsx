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

const CHS = [
  { id: 'C0123456789', name: 'site-alerts', is_member: true, is_private: false },
  { id: 'G0456789ABC', name: 'ops-private', is_member: false, is_private: true },
];

function mockApi(result: any = { channels: CHS }) {
  vi.mocked(api).mockImplementation((url: string) => {
    if (url === '/v1/notifications/slack/channels') {
      return (result instanceof Error ? Promise.reject(result) : Promise.resolve(result)) as any;
    }
    if (url === '/v1/branding') return Promise.resolve({ product_name: 'HexThings', accent: '#000' }) as any;
    if (url === '/v1/notifications/channels') return Promise.resolve([]) as any;
    return Promise.reject(new Error('not mocked')) as any; // child cards render their empty state
  });
}

async function pickSlack() {
  const sel = box.querySelector('select')!;
  await act(async () => {
    sel.value = 'slack';
    sel.dispatchEvent(new Event('change', { bubbles: true }));
  });
}

async function pressFind() {
  const findBtn = [...box.querySelectorAll('button')].find(b => b.textContent === 'Find channels')!;
  await act(async () => { findBtn.click(); });
}

test('slack setup offers a Find channels picker for the channel ID', async () => {
  mockApi();
  await act(async () => root.render(<MemoryRouter><Settings /></MemoryRouter>));
  await act(async () => {});
  await pickSlack();
  expect([...box.querySelectorAll('button')].some(b => b.textContent === 'Find channels')).toBe(true);
  await pressFind();
  const cards = [...box.querySelectorAll('[data-testid=sl-channel-card]')];
  expect(cards.length).toBe(2);
  expect(box.textContent).toContain('#site-alerts');
  expect(box.textContent).toContain('id C0123456789');
  expect(box.textContent).toContain('private channel');
});

test('a channel the bot has not joined is flagged before the admin presses Use', async () => {
  mockApi();
  await act(async () => root.render(<MemoryRouter><Settings /></MemoryRouter>));
  await act(async () => {});
  await pickSlack();
  await pressFind();
  const cards = [...box.querySelectorAll('[data-testid=sl-channel-card]')];
  const nonMember = cards.find(d => d.textContent?.includes('ops-private'))!;
  expect(nonMember.textContent).toContain('not a member');
  expect(nonMember.textContent).toContain('/invite @YourBotName');
  const member = cards.find(d => d.textContent?.includes('site-alerts'))!;
  expect(member.textContent).not.toContain('not a member');
});

test('pressing Use this channel fills the target id and closes the review', async () => {
  mockApi();
  await act(async () => root.render(<MemoryRouter><Settings /></MemoryRouter>));
  await act(async () => {});
  await pickSlack();
  await pressFind();
  const cards = [...box.querySelectorAll('[data-testid=sl-channel-card]')];
  const card = cards.find(d => d.textContent?.includes('site-alerts'))!;
  const useBtn = [...card.querySelectorAll('button')].find(b => b.textContent === 'Use this channel')!;
  await act(async () => { useBtn.click(); });
  const tgt = [...box.querySelectorAll('input')].find(i => i.placeholder.includes('C0123456789'))!;
  expect(tgt.value).toBe('C0123456789');
  expect(box.querySelector('[data-testid=sl-channel-review]')).toBeNull();
});

test('an unconfigured deployment gets an operator-facing message, not a crash', async () => {
  mockApi(new Error('409: Slack is not configured on this deployment (the operator sets SLACK_BOT_TOKEN)'));
  await act(async () => root.render(<MemoryRouter><Settings /></MemoryRouter>));
  await act(async () => {});
  await pickSlack();
  await pressFind();
  expect(box.textContent).toContain('Slack is not configured on this deployment yet');
  expect(box.textContent).toContain('SLACK_BOT_TOKEN');
  expect(box.querySelector('[data-testid=sl-channel-review]')).toBeNull();
});

test('a Slack API failure (ok:false) surfaces its error text, not a crash', async () => {
  mockApi({ channels: [], error: 'slack: invalid_auth' });
  await act(async () => root.render(<MemoryRouter><Settings /></MemoryRouter>));
  await act(async () => {});
  await pickSlack();
  await pressFind();
  expect(box.textContent).toContain('slack: invalid_auth');
});

test('a truncated large-workspace listing says so instead of pretending to be complete', async () => {
  mockApi({ channels: CHS, truncated: true });
  await act(async () => root.render(<MemoryRouter><Settings /></MemoryRouter>));
  await act(async () => {});
  await pickSlack();
  await pressFind();
  expect(box.textContent).toContain('first 2000 channels');
  expect(box.textContent).toContain('not listed');
});

test('an empty workspace view tells the admin to invite the bot', async () => {
  mockApi({ channels: [] });
  await act(async () => root.render(<MemoryRouter><Settings /></MemoryRouter>));
  await act(async () => {});
  await pickSlack();
  await pressFind();
  expect(box.textContent).toContain('cannot see any channels yet');
  expect(box.textContent).toContain('/invite @YourBotName');
});
