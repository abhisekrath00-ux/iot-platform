import { describe, expect, it } from 'vitest';
import { addNode, connect, emptyGraph, problems, prunePorts, removeNode } from './graph';

describe('graph editor helpers', () => {
  it('adds nodes with unique ids and never a second trigger', () => {
    let g = emptyGraph();
    g = addNode(g, 'delay', 100, 100);
    g = addNode(g, 'delay', 200, 100);
    g = addNode(g, 'trigger', 0, 0);
    expect(g.nodes.map(n => n.id)).toEqual(['trigger', 'delay1', 'delay2']);
  });

  it('refuses loops, self edges, duplicate edges, inputs to the trigger and outputs from end nodes', () => {
    let g = addNode(addNode(addNode(emptyGraph(), 'delay', 0, 0), 'change', 0, 0), 'notify', 0, 0);
    const ok = (r: ReturnType<typeof connect>) => { expect(typeof r).not.toBe('string'); return r as any; };
    g = ok(connect(g, 'trigger', '0', 'delay1'));
    g = ok(connect(g, 'delay1', '0', 'change1'));
    expect(connect(g, 'change1', '0', 'delay1')).toBe('That would create a loop');
    expect(connect(g, 'delay1', '0', 'delay1')).toMatch(/itself/);
    expect(connect(g, 'trigger', '0', 'delay1')).toBe('Already connected');
    expect(connect(g, 'delay1', '0', 'trigger')).toMatch(/no input/);
    g = ok(connect(g, 'change1', '0', 'notify1'));
    expect(connect(g, 'notify1', '0', 'delay1')).toMatch(/end point/);
  });

  it('removes a node together with its edges, but never the trigger', () => {
    let g = addNode(emptyGraph(), 'delay', 0, 0);
    g = connect(g, 'trigger', '0', 'delay1') as any;
    expect(removeNode(g, 'trigger')).toBe(g);
    const r = removeNode(g, 'delay1');
    expect(r.edges).toHaveLength(0);
    expect(r.nodes).toHaveLength(1);
  });

  it('prunes edges from switch ports that no longer exist', () => {
    let g = addNode(emptyGraph(), 'switch', 0, 0);
    g = addNode(g, 'notify', 0, 0);
    g = connect(g, 'switch1', '1', 'notify1') as any;
    g.nodes = g.nodes.map(n => n.id === 'switch1' ? { ...n, rules: [n.rules![0]] } : n);
    expect(prunePorts(g).edges).toHaveLength(0);
  });

  it('reports what is missing before save', () => {
    let g = emptyGraph();
    expect(problems(g)).toContain('Set the device and point on the reading node');
    expect(problems(g)).toContain('Add at least one Notify node');
    g = addNode(g, 'notify', 0, 0);
    expect(problems(g).some(p => p.includes('needs a channel'))).toBe(true);
  });
});
