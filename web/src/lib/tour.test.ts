import { describe, it, expect } from 'vitest';
import { TOUR_STEPS, nextStep, prevStep, tourDone, markTourDone } from './tour';

describe('product tour', () => {
  it('steps advance and end', () => {
    expect(nextStep(0)).toBe(1);
    expect(nextStep(TOUR_STEPS.length - 1)).toBeNull();
    expect(prevStep(0)).toBe(0);
    expect(prevStep(3)).toBe(2);
  });
  it('every step targets a distinct route with copy', () => {
    const routes = new Set(TOUR_STEPS.map(s => s.to));
    expect(routes.size).toBe(TOUR_STEPS.length);
    for (const s of TOUR_STEPS) { expect(s.title).not.toBe(''); expect(s.body.length).toBeGreaterThan(20); }
  });
  it('remembers completion', () => {
    const m = new Map<string, string>();
    const store = { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v) };
    expect(tourDone(store)).toBe(false);
    markTourDone(store);
    expect(tourDone(store)).toBe(true);
  });
});
