// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import AssistantMemory, { memoryValid } from './AssistantMemory';
import { api } from '../lib/api';
vi.mock('../lib/api', () => ({ api: vi.fn(), download: vi.fn().mockResolvedValue(undefined) }));
let root: Root; let box: HTMLDivElement;
beforeEach(() => { (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true; box = document.createElement('div'); document.body.append(box); root = createRoot(box); });
afterEach(async () => { await act(async () => root.unmount()); box.remove(); vi.clearAllMocks(); });
const button = (s: string) => Array.from(box.querySelectorAll('button')).find(b => b.textContent === s)!;
test('default off requires explicit provider/privacy acknowledgement; no save on load', async () => {
  vi.mocked(api).mockResolvedValue({ enabled: false, notes: [] });
  await act(async () => root.render(<AssistantMemory />));
  expect(box.textContent).toContain('hosted AI provider'); expect(button('Enable memory').disabled).toBe(true);
  expect(api).toHaveBeenCalledTimes(1);
  await act(async () => (box.querySelector('input[type=checkbox]') as HTMLInputElement).click());
  expect(button('Enable memory').disabled).toBe(false);
  await act(async () => button('Enable memory').click());
  expect(api).toHaveBeenCalledWith('/v1/assistant/memory/settings', { method: 'PUT', body: '{"enabled":true}' });
});
test('deleting requires separate review; cancel does not send', async () => {
  vi.mocked(api).mockResolvedValue({ enabled: true, notes: [{ id: 'note', title: 'Pump', content: 'Orchid', expires_at: '2026-11-01T00:00:00Z' }] });
  await act(async () => root.render(<AssistantMemory />));
  await act(async () => button('Delete...').click()); expect(button('Delete note')).toBeTruthy(); expect(api).toHaveBeenCalledTimes(1);
  await act(async () => button('Keep it').click()); expect(api).toHaveBeenCalledTimes(1);
  await act(async () => button('Delete...').click()); await act(async () => button('Delete note').click());
  expect(api).toHaveBeenCalledWith('/v1/assistant/memory/note', { method: 'DELETE' });
});
test('backend failure is visible, not silently treated as empty', async () => {
  vi.mocked(api).mockRejectedValue(new Error('503: memory unavailable'));
  await act(async () => root.render(<AssistantMemory />)); expect(box.querySelector('[role=alert]')?.textContent).toContain('memory unavailable'); expect(box.textContent).not.toContain('No notes saved');
});
test('byte bounds and retention match server', () => {
  expect(memoryValid('Pump', 'Orchid', 30)).toBe(true); expect(memoryValid('', 'x', 1)).toBe(false);
  expect(memoryValid('x', 'é'.repeat(1001), 1)).toBe(false); expect(memoryValid('x', 'x', 91)).toBe(false); expect(memoryValid('x', 'x', 1.5)).toBe(false);
});
test('disable uses explicit false and preserves export access', async () => {
  vi.mocked(api).mockResolvedValue({ enabled: true, notes: [] });
  await act(async () => root.render(<AssistantMemory />));
  await act(async () => button('Disable retrieval').click());
  expect(api).toHaveBeenCalledWith('/v1/assistant/memory/settings', { method: 'PUT', body: '{"enabled":false}' });
  expect(button('Export my notes')).toBeTruthy();
});
