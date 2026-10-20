import { describe, it, expect } from 'vitest';
import { thresholdState, fraction, freshness, optNum, spanOf, moveItem, summarize, indicatorOn, filterWidgetsByAsset, createSequencer, Widget } from './widgets';

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

describe('widget library additions', () => {
  it('summarize', () => {
    expect(summarize([])).toBeNull();
    expect(summarize([NaN])).toBeNull();
    expect(summarize([2, 4, 9, 5])).toEqual({ last: 5, min: 2, max: 9, avg: 5, delta: 3, n: 4 });
  });
  it('indicatorOn', () => {
    expect(indicatorOn(1)).toBe(true);
    expect(indicatorOn(0)).toBe(false);
    expect(indicatorOn(NaN)).toBe(false);
    expect(indicatorOn(49, 50)).toBe(false);
    expect(indicatorOn(50, 50)).toBe(true);
  });
  it('table is wide by default', () => {
    expect(spanOf({ type: 'table' })).toBe(2);
    expect(spanOf({ type: 'stat' })).toBe(1);
  });
});

describe('filterWidgetsByAsset', () => {
  const w = (id: string, device_id: string): Widget => ({ id, type: 'kpi', title: id, device_id });
  const widgets = [w('a', 'dev-1'), w('b', 'dev-2'), w('note', ''), w('alarms', '')];
  it('passes everything through with no filter', () => {
    expect(filterWidgetsByAsset(widgets, null)).toHaveLength(4);
  });
  it('keeps only devices under the asset, plus unbound widgets', () => {
    const vis = filterWidgetsByAsset(widgets, new Set(['dev-2']));
    expect(vis.map(x => x.id)).toEqual(['b', 'note', 'alarms']);
  });
  it('an empty subtree leaves only unbound widgets', () => {
    expect(filterWidgetsByAsset(widgets, new Set()).map(x => x.id)).toEqual(['note', 'alarms']);
  });
});

describe('createSequencer', () => {
  it('only the latest begin stays current', () => {
    const seq = createSequencer();
    const a = seq.begin();
    const b = seq.begin();
    // the slow earlier response resolves after the newer one was requested
    expect(seq.isCurrent(a)).toBe(false);
    expect(seq.isCurrent(b)).toBe(true);
  });
  it('a single request stays current', () => {
    const seq = createSequencer();
    expect(seq.isCurrent(seq.begin())).toBe(true);
  });
  it('out-of-order completion across three requests keeps only the last', () => {
    const seq = createSequencer();
    const t1 = seq.begin(), t2 = seq.begin(), t3 = seq.begin();
    expect([seq.isCurrent(t1), seq.isCurrent(t2), seq.isCurrent(t3)]).toEqual([false, false, true]);
  });
});
