import { describe, expect, it } from 'vitest';
import { parseSSE } from './sse';

describe('parseSSE', () => {
  it('splits events and keeps a partial one for the next chunk', () => {
    const a = parseSSE('event: delta\ndata: "Hel"\n\nevent: delta\ndata: "lo"\n\nevent: fin');
    expect(a.events).toEqual([{ event: 'delta', data: '"Hel"' }, { event: 'delta', data: '"lo"' }]);
    const b = parseSSE(a.rest + 'al\ndata: {"a":1}\n\n');
    expect(b.events).toEqual([{ event: 'final', data: '{"a":1}' }]);
  });
  it('handles CRLF', () => {
    expect(parseSSE('event: x\r\ndata: 1\r\n\r\n').events).toHaveLength(1);
  });
});
