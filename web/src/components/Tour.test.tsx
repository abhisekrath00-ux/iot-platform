// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import Tour from './Tour';

let root: Root; let box: HTMLDivElement; let nav: HTMLElement;
beforeEach(() => {
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  box = document.createElement('div'); document.body.append(box);
  nav = document.createElement('nav'); nav.innerHTML = `<a href="/">Fleet</a>`; document.body.append(nav);
  root = createRoot(box);
});
afterEach(async () => { await act(async () => root.unmount()); box.remove(); nav.remove(); vi.restoreAllMocks(); localStorage.clear(); });

function setViewport(width: number) {
  Object.defineProperty(window, 'innerWidth', { value: width, configurable: true });
}

test('tour card stays inside a narrow mobile viewport even when the anchor is far right', async () => {
  setViewport(390);
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ right: 900, top: 100, left: 800, width: 100, height: 20, bottom: 120, x: 800, y: 100, toJSON: () => ({}) } as DOMRect);
  await act(async () => root.render(<Tour onClose={() => {}} />));
  const card = document.querySelector('.tour-card') as HTMLElement;
  const left = parseInt(card.style.left, 10);
  // 390px viewport: the card (320px wide, 24px margin via CSS) must start at or left of 46px
  expect(left).toBeLessThanOrEqual(46);
  expect(left).toBeGreaterThanOrEqual(12);
});

test('tour card sits right of the sidebar anchor on a desktop viewport', async () => {
  setViewport(1400);
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ right: 200, top: 100, left: 100, width: 100, height: 20, bottom: 120, x: 100, y: 100, toJSON: () => ({}) } as DOMRect);
  await act(async () => root.render(<Tour onClose={() => {}} />));
  const card = document.querySelector('.tour-card') as HTMLElement;
  expect(card.style.left).toBe('218px');
});
