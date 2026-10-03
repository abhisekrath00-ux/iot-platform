// Pure helpers for the flow graph editor (no React), so they can be unit tested.
// The server is the authority on validity; these keep the editor from building
// graphs it already knows will be refused.

export type NodeType = 'trigger' | 'switch' | 'change' | 'condition' | 'delay' | 'debug' | 'notify' | 'function' | 'template' | 'range' | 'rate_limit' | 'inject' | 'http' | 'control';

export interface GNode {
  id: string; type: NodeType; name?: string; x: number; y: number;
  device_id?: string; point_id?: string; op?: string; value?: number;
  property?: string; rules?: { op: string; value?: string | number }[]; mode?: string;
  changes?: { action: string; property: string; value?: string | number; to?: string }[];
  seconds?: number; channel_id?: string; message?: string; code?: string;
  template?: string; target?: string; in_min?: number; in_max?: number; out_min?: number; out_max?: number; clamp?: boolean;
  method?: string; url?: string; body?: string; extract?: string; target_id?: string; use_value?: boolean;
}
export interface GEdge { from: string; port: string; to: string; }
export interface Graph { nodes: GNode[]; edges: GEdge[]; }

export const NODE_W = 150;
export const NODE_H = 52;

export const LABELS: Record<NodeType, string> = {
  trigger: 'Reading', switch: 'Switch', change: 'Change', condition: 'Condition',
  delay: 'Delay', debug: 'Debug', notify: 'Notify', function: 'Function', template: 'Template', range: 'Range', rate_limit: 'Rate limit', inject: 'Timer', http: 'HTTP request', control: 'Control request'
};

export function portCount(n: GNode): number {
  if (n.type === 'notify' || n.type === 'debug') return 0;
  if (n.type === 'switch') return Math.max(1, n.rules?.length ?? 1);
  if (n.type === 'http' || n.type === 'control') return 2; // 1 = success / request raised, 2 = failure / refused
  return 1;
}

export function emptyGraph(): Graph {
  return { nodes: [{ id: 'trigger', type: 'trigger', device_id: '', point_id: '', op: '>', value: 0, x: 40, y: 80 }], edges: [] };
}

export function nextId(g: Graph, type: NodeType): string {
  let i = 1;
  while (g.nodes.some(n => n.id === `${type}${i}`)) i++;
  return `${type}${i}`;
}

export function defaults(type: NodeType): Partial<GNode> {
  switch (type) {
    case 'switch': return { property: 'value', mode: 'first', rules: [{ op: '>', value: 0 }, { op: 'else' }] };
    case 'change': return { changes: [{ action: 'set', property: 'vars.level', value: 'high' }] };
    case 'condition': return { op: '<', value: 0 };
    case 'delay': return { seconds: 30 };
    case 'notify': return { channel_id: '', message: 'value {value}' };
    case 'debug': return { message: '{value}' };
    case 'template': return { template: 'value {value}', target: 'text' };
    case 'inject': return { seconds: 3600, value: 0, device_id: '', point_id: '' };
    case 'rate_limit': return { seconds: 60 };
    case 'range': return { in_min: 0, in_max: 100, out_min: 0, out_max: 1, clamp: true };
    case 'function': return { code: 'return msg;' };
    case 'control': return { target_id: '', value: 1, use_value: false };
    case 'http': return { method: 'GET', url: 'http://', body: '', target: 'result', extract: '' };
    default: return {};
  }
}

export const isStart = (n: GNode) => n.type === 'trigger' || n.type === 'inject';

/** Swap the start node between a reading trigger and a timer, keeping its connections. */
export function setStart(g: Graph, kind: 'trigger' | 'inject'): Graph {
  const cur = g.nodes.find(isStart);
  if (!cur || cur.type === kind) return g;
  const base = { id: cur.id, x: cur.x, y: cur.y, name: cur.name };
  const next: GNode = kind === 'inject'
    ? { ...base, type: 'inject', ...defaults('inject') }
    : { ...base, type: 'trigger', device_id: '', point_id: '', op: '>', value: 0 };
  return { ...g, nodes: g.nodes.map(n => (n === cur ? next : n)) };
}

export function addNode(g: Graph, type: NodeType, x: number, y: number): Graph {
  if (type === 'trigger' || type === 'inject') return g; // exactly one start node, already present (use setStart)
  const id = nextId(g, type);
  return { ...g, nodes: [...g.nodes, { id, type, x, y, ...defaults(type) } as GNode] };
}

export function removeNode(g: Graph, id: string): Graph {
  const victim = g.nodes.find(n => n.id === id);
  if (victim && isStart(victim)) return g;
  return { nodes: g.nodes.filter(n => n.id !== id), edges: g.edges.filter(e => e.from !== id && e.to !== id) };
}

export function reaches(g: Graph, from: string, target: string): boolean {
  const seen = new Set<string>();
  const stack = [from];
  while (stack.length) {
    const x = stack.pop()!;
    if (x === target) return true;
    if (seen.has(x)) continue;
    seen.add(x);
    g.edges.filter(e => e.from === x).forEach(e => stack.push(e.to));
  }
  return false;
}

/** Returns the new graph, or an error string explaining why the edge is refused. */
export function connect(g: Graph, from: string, port: string, to: string): Graph | string {
  const a = g.nodes.find(n => n.id === from), b = g.nodes.find(n => n.id === to);
  if (!a || !b) return 'Unknown node';
  if (from === to) return 'A node cannot connect to itself';
  if (portCount(a) === 0) return `${LABELS[a.type]} nodes are an end point and have no output`;
  if (isStart(b)) return 'The start node has no input';
  if (Number(port) >= portCount(a)) return 'No such output';
  if (g.edges.some(e => e.from === from && e.port === port && e.to === to)) return 'Already connected';
  if (reaches(g, to, from)) return 'That would create a loop';
  return { ...g, edges: [...g.edges, { from, port, to }] };
}

/** After a switch rule is removed, drop edges from ports that no longer exist. */
export function prunePorts(g: Graph): Graph {
  const by = new Map(g.nodes.map(n => [n.id, n]));
  return { ...g, edges: g.edges.filter(e => { const n = by.get(e.from); return !!n && Number(e.port) < portCount(n); }) };
}

/** Client-side hints shown before Save; the server still validates. */
export function problems(g: Graph): string[] {
  const out: string[] = [];
  const t = g.nodes.find(isStart);
  if (t?.type === 'inject') { if (!t.seconds || t.seconds < 60) out.push('Timer interval must be at least 60 seconds'); }
  else if (!t || !t.device_id || !t.point_id) out.push('Set the device and point on the reading node');
  if (!g.nodes.some(n => n.type === 'notify')) out.push('Add at least one Notify node');
  for (const n of g.nodes) {
    if (!isStart(n) && !g.edges.some(e => e.to === n.id)) out.push(`${n.name || n.id} has no input`);
    if (n.type === 'notify' && !n.channel_id) out.push(`${n.name || n.id} needs a channel`);
  }
  return out;
}

export function edgePath(g: Graph, e: GEdge): string {
  const a = g.nodes.find(n => n.id === e.from)!, b = g.nodes.find(n => n.id === e.to)!;
  const ports = portCount(a);
  const x1 = a.x + NODE_W, y1 = a.y + (NODE_H * (Number(e.port) + 1)) / (ports + 1);
  const x2 = b.x, y2 = b.y + NODE_H / 2;
  const dx = Math.max(40, Math.abs(x2 - x1) / 2);
  return `M${x1},${y1} C${x1 + dx},${y1} ${x2 - dx},${y2} ${x2},${y2}`;
}

export function portY(n: GNode, port: number): number {
  return n.y + (NODE_H * (port + 1)) / (portCount(n) + 1);
}
