import { describe, it, expect } from 'vitest';
import { thresholdState, fraction, freshness, optNum, spanOf, moveItem } from './widgets';

describe('widget logic', () => {
  it('threshold state: crit beats warn, none when no value', () => {
    expect(thresholdState(10, 50, 80)).toBe('ok');
    expect(thresholdState(50, 50, 80)).toBe('warn');
    expect(thresholdState(90, 50, 80)).toBe('bad');
    expect(thresholdState(90, undefined, undefined)).toBe('ok');
    expect(thresholdState(NaN, 1, 2)).toBe('none');
  });
  it('fraction clamps and survives bad ranges', () => {
    expect(fraction(5, 0, 10)).toBe(0.5);
    expect(fraction(-5, 0, 10)).toBe(0);
    expect(fraction(50, 0, 10)).toBe(1);
    expect(fraction(5, 10, 10)).toBe(0);
  });
  it('freshness', () => {
    const now = Date.parse('2026-10-01T00:10:00Z');
    expect(freshness('2026-10-01T00:09:30Z', now)).toBe('online');
    expect(freshness('2026-10-01T00:00:00Z', now)).toBe('offline');
    expect(freshness(undefined, now)).toBe('offline');
    expect(freshness('nope', now)).toBe('offline');
  });
  it('optNum', () => {
    expect(optNum('')).toBeUndefined();
    expect(optNum('abc')).toBeUndefined();
    expect(optNum('0')).toBe(0);
    expect(optNum(' 12.5 ')).toBe(12.5);
  });
});

describe('layout logic', () => {
  it('spanOf uses defaults and clamps explicit spans', () => {
    expect(spanOf({ type: 'kpi' })).toBe(1);
    expect(spanOf({ type: 'timeseries' })).toBe(2);
    expect(spanOf({ type: 'kpi', span: 9 })).toBe(4);
    expect(spanOf({ type: 'bar', span: 0 })).toBe(1);
    expect(spanOf({ type: 'bar', span: NaN })).toBe(2);
  });
  it('moveItem reorders and ignores bad indexes', () => {
    expect(moveItem(['a', 'b', 'c', 'd'], 0, 2)).toEqual(['b', 'c', 'a', 'd']);
    expect(moveItem(['a', 'b', 'c', 'd'], 3, 0)).toEqual(['d', 'a', 'b', 'c']);
    expect(moveItem(['a', 'b'], 5, 0)).toEqual(['a', 'b']);
    const src = ['a', 'b'];
    expect(moveItem(src, 0, 1)).not.toBe(src);
    expect(src).toEqual(['a', 'b']);
  });
});
