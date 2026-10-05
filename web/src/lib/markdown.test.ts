import { describe, expect, it } from 'vitest';
import { parseBlocks } from './markdown';

describe('parseBlocks', () => {
  it('reads a table, a list and a code block', () => {
    const b = parseBlocks('Devices:\n\n| name | state |\n|---|---|\n| pump | up |\n\n- one\n- two\n\n```\nx = 1\n```');
    expect(b.map(x => x.t)).toEqual(['p', 'table', 'ul', 'code']);
    const t = b[1] as { head: string[]; rows: string[][] };
    expect(t.head).toEqual(['name', 'state']);
    expect(t.rows).toEqual([['pump', 'up']]);
  });
  it('keeps markup as plain text, never as elements', () => {
    const b = parseBlocks('<img src=x onerror=alert(1)> **bold**');
    expect(b).toHaveLength(1);
    expect((b[0] as { text: string }).text).toContain('<img');
  });
  it('does not loop on an unterminated code fence', () => {
    expect(parseBlocks('```\nabc')).toEqual([{ t: 'code', text: 'abc' }]);
  });
});
