// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import Ask, { fmtTime, fmtValue } from './Ask';
import { api } from '../lib/api';
vi.mock('../lib/api', () => ({ api: vi.fn() }));

let root: Root; let box: HTMLDivElement;
beforeEach(() => { (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true; box = document.createElement('div'); document.body.append(box); root = createRoot(box); });
afterEach(async () => { await act(async () => root.unmount()); box.remove(); vi.clearAllMocks(); });

test('offset-bearing observed_at renders a real date, never Invalid Date', () => {
  expect(fmtTime('2026-10-21T00:22:30.048941+05:30')).not.toContain('Invalid');
  expect(fmtTime('2026-10-21T00:22:30Z')).not.toContain('Invalid');
  expect(fmtTime('2026-10-21T00:22:30')).not.toContain('Invalid'); // bare UTC legacy shape
  expect(fmtTime('2026-10-21T00:22:30.048941+05:30')).toBe(new Date('2026-10-20T18:52:30Z').toLocaleString());
});

test('long floats are rounded for display', () => {
  expect(fmtValue(8.563330593401318)).toBe(8.56);
  expect(fmtValue(50)).toBe(50);
});

test('a value answer renders name, rounded value, unit and parsed time', async () => {
  vi.mocked(api).mockResolvedValue({ kind: 'value', interpreted_as: 'the latest reading of point p_total on the device named demo-meter1', device: { name: 'Demo Meter 1' }, value: 8.563330593401318, unit: 'kW', observed_at: '2026-10-21T00:22:30.048941+05:30' } as any);
  await act(async () => root.render(<Ask />));
  const input = box.querySelector('input[aria-label="Ask a question"]') as HTMLInputElement;
  await act(async () => {
    Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!.call(input, 'latest p_total of demo-meter1');
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
  await act(async () => (box.querySelector('button') as HTMLButtonElement).click());
  const status = box.querySelector('[role=status]')!;
  expect(status.textContent).toContain('Demo Meter 1');
  expect(status.textContent).toContain('8.56 kW');
  expect(status.textContent).not.toContain('Invalid Date');
});
