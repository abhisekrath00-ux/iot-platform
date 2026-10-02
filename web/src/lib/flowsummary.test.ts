import { describe, expect, it } from 'vitest';
import { flowSummary } from './flowsummary';

describe('flowSummary', () => {
  it('summarises a step-list flow', () => {
    expect(flowSummary({ trigger: { device_id: 'd', point_id: 'p', op: '>', value: 5 }, steps: [1, 2], latch: true })).toBe('d/p > 5 - 2 steps - latched');
  });
  it('does not crash on a graph flow with no trigger or steps', () => {
    expect(flowSummary({ graph: { nodes: [1, 2, 3] } })).toBe('graph, 3 nodes');
    expect(flowSummary({ trigger: { device_id: '', point_id: '', op: '', value: 0 }, graph: { nodes: [1] } })).toBe('graph, 1 nodes');
    expect(flowSummary({ trigger: null, steps: null, graph: {} })).toBe('graph flow');
    expect(flowSummary({})).toBe('');
    expect(flowSummary(null)).toBe('');
  });
});
