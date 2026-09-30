import { describe, expect, it } from 'vitest';
import { formatValue } from './format';

describe('formatValue', () => {
  it('rounds long floats', () => expect(formatValue(230.82948066346708)).toBe('230.8'));
  it('groups thousands', () => expect(formatValue(1234567.891)).toBe('1,234,567.9'));
  it('keeps small precision', () => expect(formatValue(0.012345)).toBe('0.0123'));
  it('trims trailing zeros', () => { expect(formatValue(2.5)).toBe('2.5'); expect(formatValue(3)).toBe('3'); });
  it('handles zero and non-finite', () => { expect(formatValue(0)).toBe('0'); expect(formatValue(NaN)).toBe('-'); });
});
