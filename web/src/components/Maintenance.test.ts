import { describe, it, expect } from 'vitest';
import { windowHours } from './Maintenance';

describe('windowHours', () => {
  it('accepts 1 to 168', () => { expect(windowHours('4')).toBe(4); expect(windowHours('168')).toBe(168); });
  it('refuses the rest', () => { for (const v of ['0', '169', '', 'x', 'NaN', '-3']) expect(windowHours(v)).toBeNull(); });
});
