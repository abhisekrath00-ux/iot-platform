// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import ChatPanel from './ChatPanel';
vi.mock('../lib/api', () => ({ api: vi.fn(() => Promise.resolve({})), download: vi.fn() }));
let root: Root; let box: HTMLDivElement;
beforeEach(() => { (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true; box = document.createElement('div'); document.body.append(box); root = createRoot(box); });
afterEach(async () => { await act(async () => root.unmount()); box.remove(); vi.clearAllMocks(); });

function scrollTo(y: number) {
  Object.defineProperty(window, 'scrollY', { value: y, configurable: true });
  window.dispatchEvent(new Event('scroll'));
}

test('the floating launcher ducks away on downward scroll and returns on upward scroll', async () => {
  await act(async () => root.render(<MemoryRouter><ChatPanel /></MemoryRouter>));
  const fab = box.querySelector('.chat-fab')!;
  expect(fab.className).toBe('chat-fab');
  // scrolling down: it hides so it cannot cover a control being reached for
  await act(async () => scrollTo(300));
  expect(fab.className).toContain('fab-away');
  // further down: still hidden
  await act(async () => scrollTo(600));
  expect(fab.className).toContain('fab-away');
  // scrolling back up: it returns
  await act(async () => scrollTo(250));
  expect(fab.className).toBe('chat-fab');
  // near the top it is always shown
  await act(async () => { scrollTo(80); });
  await act(async () => scrollTo(10));
  expect(fab.className).toBe('chat-fab');
});
