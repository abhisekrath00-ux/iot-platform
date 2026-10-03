import { describe, expect, it } from 'vitest';
import { formatAttributes, parseAttributes } from './DeviceAttributes';

describe('attributes text', () => {
  it('parses name=value lines with numbers and booleans', () => {
    expect(parseAttributes('owner=plant team\nfloor=2\ncritical=true\n\nnote = a=b')).toEqual({ owner: 'plant team', floor: 2, critical: true, note: 'a=b' });
  });
  it('rejects a line without a name', () => {
    expect(() => parseAttributes('just text')).toThrow(/name=value/);
    expect(() => parseAttributes('=x')).toThrow();
  });
  it('round-trips', () => {
    const a = { owner: 'x', floor: 2 };
    expect(parseAttributes(formatAttributes(a))).toEqual(a);
  });
});
