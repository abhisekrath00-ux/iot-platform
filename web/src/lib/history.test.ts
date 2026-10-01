import { describe, expect, it } from 'vitest';
import { addNode, emptyGraph } from './graph';
import { COALESCE_MS, MAX_HISTORY, emptyHistory, record, redo, undo } from './history';

describe('history', () => {
  const g0 = emptyGraph();
  const g1 = addNode(g0, 'delay', 0, 0);
  const g2 = addNode(g1, 'debug', 0, 0);

  it('undoes and redoes separate edits', () => {
    let h = record(emptyHistory(), g0, 1000);
    h = record(h, g1, 1000 + COALESCE_MS + 1);
    const u = undo(h, g2)!;
    expect(u.g).toBe(g1);
    const u2 = undo(u.h, u.g)!;
    expect(u2.g).toBe(g0);
    expect(undo(u2.h, u2.g)).toBeNull();
    expect(redo(u2.h, u2.g)!.g).toBe(g1);
  });

  it('coalesces rapid edits into one step', () => {
    let h = record(emptyHistory(), g0, 1000);
    h = record(h, g1, 1100);
    expect(h.past).toHaveLength(1);
  });

  it('a new edit clears redo and history is bounded', () => {
    let h = record(emptyHistory(), g0, 1000);
    const u = undo(h, g1)!;
    expect(u.h.future).toHaveLength(1);
    expect(record(u.h, g0, 5000).future).toHaveLength(0);
    let big = emptyHistory();
    for (let i = 0; i < MAX_HISTORY + 20; i++) big = record(big, g0, i * 10000);
    expect(big.past).toHaveLength(MAX_HISTORY);
  });
});
