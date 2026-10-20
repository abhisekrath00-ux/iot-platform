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

function mockApi(chats: { id: string; type: string; title: string }[]) {
  vi.mocked(api).mockImplementation((url: string) => {
    if (url === '/v1/notifications/telegram/chats') return Promise.resolve({ chats, pairing: false }) as any;
    if (url === '/v1/notifications/telegram/pair-code') return Promise.resolve({ code: 'ABCD23', expires_in_minutes: 10 }) as any;
    if (url === '/v1/branding') return Promise.resolve({ product_name: 'HexThings', accent: '#000' }) as any;
    if (url === '/v1/notifications/channels') return Promise.resolve([]) as any;
    return Promise.reject(new Error('not mocked')) as any; // child cards render their empty state
  });
}

async function pickTelegram() {
  const sel = box.querySelector('select')!;
  await act(async () => {
    sel.value = 'telegram';
    sel.dispatchEvent(new Event('change', { bubbles: true }));
  });
}

test('telegram setup warns that a paired group is an audience decision, before any pairing', async () => {
  mockApi([]);
  await act(async () => root.render(<MemoryRouter><Settings /></MemoryRouter>));
  await act(async () => {});
  await pickTelegram();
  expect(box.textContent).toContain('audience decision');
  expect(box.textContent).toContain('anyone who joins later');
  expect(box.textContent).toContain('cannot');
  expect(box.textContent).toContain('per chat, not per person');
});

test('find-chats review flags a group row before the admin presses Use', async () => {
  mockApi([{ id: '-1001234567890', type: 'supergroup', title: 'Plant Ops' }, { id: '123456789', type: 'private', title: 'Rahul' }]);
  await act(async () => root.render(<MemoryRouter><Settings /></MemoryRouter>));
  await act(async () => {});
  await pickTelegram();
  const findBtn = [...box.querySelectorAll('button')].find(b => b.textContent === 'Find chats')!;
  await act(async () => { findBtn.click(); });
  expect(box.textContent).toContain('Plant Ops');
  expect(box.textContent).toContain('All current and future members will see this site');
  // the private chat row carries no such note
  const rows = [...box.querySelectorAll('tr')];
  const priv = rows.find(r => r.textContent?.includes('Rahul'));
  expect(priv?.textContent).not.toContain('future members');
});
