// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import OnCall from './OnCall';
import { api } from '../lib/api';
vi.mock('../lib/api', () => ({ api: vi.fn(), download: vi.fn().mockResolvedValue(undefined) }));
let root: Root; let box: HTMLDivElement;
beforeEach(() => { (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true; box = document.createElement('div'); document.body.append(box); root = createRoot(box); });
afterEach(async () => { await act(async () => root.unmount()); box.remove(); vi.clearAllMocks(); });

const channels = [{ id: 'ch-ops', type: 'email', target: 'ops@hex.example', enabled: true }, { id: 'ch-noc', type: 'email', target: 'noc@hex.example', enabled: true }];
function mockWith(scheds: any[]) {
  vi.mocked(api).mockImplementation((url: string, init?: any) => {
    if (url === '/v1/oncall' && !init) return Promise.resolve(scheds) as any;
    if (url === '/v1/notifications/channels') return Promise.resolve(channels) as any;
    if (url.endsWith('/calendar') && init?.method === 'PUT') return Promise.resolve({}) as any;
    return Promise.reject(new Error('not mocked: ' + url)) as any;
  });
}
const base = { id: 'sc-1', name: 'rot', anchor: '2026-10-20T00:00:00Z', shift_hours: 24, channel_ids: ['ch-ops', 'ch-noc'], on_call_channel: 'ch-ops', shift_ends: '2026-10-22T00:00:00Z' };

test('linked schedule with a current event shows the override and source event', async () => {
  mockWith([{ ...base, google_calendar_id: 'cal-1', cal_channel_id: 'ch-noc', cal_until: '2026-10-21T20:00:00Z', cal_summary: 'Night shift: noc@hex.example', on_call_channel: 'ch-noc' }]);
  await act(async () => root.render(<OnCall />));
  const t = box.querySelector('[data-testid="oncall-cal-sc-1"]')!.textContent!;
  expect(t).toContain('cal-1');
  expect(t).toContain('Night shift: noc@hex.example');
  expect(t).toContain('email: noc@hex.example');
  expect(t).toContain('Unlink calendar');
});

test('linked schedule with a sync error shows it as an alert', async () => {
  mockWith([{ ...base, google_calendar_id: 'cal-1', cal_sync_error: 'current event "X" names no channel target in this workspace - the static rotation is in effect' }]);
  await act(async () => root.render(<OnCall />));
  const el = box.querySelector('[data-testid="oncall-cal-sc-1"]')!;
  expect(el.querySelector('[role="alert"]')!.textContent).toContain('names no channel target');
});

test('linked schedule with no current event says the rotation is in effect', async () => {
  mockWith([{ ...base, google_calendar_id: 'cal-1' }]);
  await act(async () => root.render(<OnCall />));
  expect(box.querySelector('[data-testid="oncall-cal-sc-1"]')!.textContent).toContain('rotation above is in effect');
});

test('unlinked schedule offers the link control; Link sends the calendar id', async () => {
  mockWith([{ ...base }]);
  await act(async () => root.render(<OnCall />));
  const input = box.querySelector('input[aria-label="Calendar id for rot"]') as HTMLInputElement;
  expect(input).toBeTruthy();
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!;
    setter.call(input, 'cal-9');
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
  const btn = [...box.querySelectorAll('button')].find(b => b.textContent === 'Link calendar')!;
  await act(async () => btn.click());
  const call = vi.mocked(api).mock.calls.find(c => String(c[0]).endsWith('/calendar'))!;
  expect(call[0]).toBe('/v1/oncall/sc-1/calendar');
  expect(JSON.parse((call[1] as any).body).calendar_id).toBe('cal-9');
});

test('Unlink sends an empty calendar id', async () => {
  mockWith([{ ...base, google_calendar_id: 'cal-1' }]);
  await act(async () => root.render(<OnCall />));
  const btn = [...box.querySelectorAll('button')].find(b => b.textContent === 'Unlink calendar')!;
  await act(async () => btn.click());
  const call = vi.mocked(api).mock.calls.find(c => String(c[0]).endsWith('/calendar'))!;
  expect(JSON.parse((call[1] as any).body).calendar_id).toBe('');
});
