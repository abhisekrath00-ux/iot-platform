import { describe, expect, it } from 'vitest';
import { groupPath, sortGroupsByTree } from './groups';

const tree = [
  { id: 'cell', name: 'Cell A', parent_id: 'line' },
  { id: 'root', name: 'Plant', parent_id: null },
  { id: 'line', name: 'Line 1', parent_id: 'root' },
];

describe('groupPath', () => {
  it('renders the full nesting path', () => {
    expect(groupPath(tree[0], tree)).toBe('Plant > Line 1 > Cell A');
  });
  it('renders a top-level group as just its name', () => {
    expect(groupPath(tree[1], tree)).toBe('Plant');
  });
  it('stops at a missing parent instead of hanging', () => {
    expect(groupPath({ id: 'x', name: 'X', parent_id: 'gone' }, tree)).toBe('X');
  });
  it('stops at a loop instead of hanging', () => {
    const loop = [
      { id: 'a', name: 'A', parent_id: 'b' },
      { id: 'b', name: 'B', parent_id: 'a' },
    ];
    expect(groupPath(loop[0], loop)).toBe('B > A');
  });
});

describe('sortGroupsByTree', () => {
  it('orders parents before children, alphabetical within a level', () => {
    expect(sortGroupsByTree(tree).map(g => g.id)).toEqual(['root', 'line', 'cell']);
  });
  it('keeps orphans whose parent is not in the list', () => {
    const withOrphan = [...tree, { id: 'o', name: 'Orphan', parent_id: 'gone' }];
    expect(sortGroupsByTree(withOrphan).map(g => g.id)).toContain('o');
  });
});
