// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import Sessions from './Sessions';
import { api } from '../lib/api';
vi.mock('../lib/api', () => ({ api: vi.fn(), download: vi.fn().mockResolvedValue(undefined) }));
let root: Root; let box: HTMLDivElement;
beforeEach(() => { (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true; box = document.createElement('div'); document.body.append(box); root = createRoot(box); });
afterEach(async () => { await act(async () => root.unmount()); box.remove(); vi.clearAllMocks(); });
const rows = [
  { id: 'a', created_at: '2026-10-20T09:00:00Z', last_seen_at: '2026-10-20T11:00:00Z', ip: '10.0.0.1', user_agent: 'Firefox', current: true },
  { id: 'b', created_at: '2026-10-19T09:00:00Z', last_seen_at: '2026-10-20T08:00:00Z', ip: '10.0.0.2', user_agent: 'Chrome', current: false },
];
const mockApi = (me: object = {}) => vi.mocked(api).mockImplementation((url: string) => Promise.resolve(url === '/v1/me' ? me : rows) as any);
test('lists sessions, flags the current device, revokes another by id', async () => {
  mockApi();
  await act(async () => root.render(<Sessions />));
  expect(box.textContent).toContain('Your sessions');
  expect(box.textContent).toContain('Older sign-ins are not listed');
  expect(box.textContent).toContain('this device');
  const revoke = Array.from(box.querySelectorAll('button')).find(b => b.textContent === 'Revoke')!;
  await act(async () => revoke.click());
  expect(api).toHaveBeenCalledWith('/v1/me/sessions/b', { method: 'DELETE' });
});
test('customer-scoped users see the list read-only, not failing buttons', async () => {
  mockApi({ customer_id: 'cust-1' });
  await act(async () => root.render(<Sessions />));
  expect(box.textContent).toContain('Your sessions');
  expect(box.querySelector('button')).toBeNull();
  expect(box.textContent).toContain('not available to customer-scoped accounts');
});
test('empty or unavailable list hides the card (API keys, pre-tracking tokens)', async () => {
  vi.mocked(api).mockResolvedValue([]);
  await act(async () => root.render(<Sessions />));
  expect(box.textContent).not.toContain('Your sessions');
});
