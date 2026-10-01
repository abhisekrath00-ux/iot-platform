// Undo/redo for the flow editor. Edits made within COALESCE_MS of each other
// (typing in a field, dragging a node) count as one step.
import type { Graph } from './graph';

export const COALESCE_MS = 700;
export const MAX_HISTORY = 50;

export interface History { past: Graph[]; future: Graph[]; at: number; }
export const emptyHistory = (): History => ({ past: [], future: [], at: 0 });

export function record(h: History, before: Graph, now: number): History {
  if (now - h.at < COALESCE_MS && h.past.length > 0) return { ...h, future: [], at: now };
  return { past: [...h.past, before].slice(-MAX_HISTORY), future: [], at: now };
}

export function undo(h: History, current: Graph): { h: History; g: Graph } | null {
  if (h.past.length === 0) return null;
  const g = h.past[h.past.length - 1];
  return { g, h: { past: h.past.slice(0, -1), future: [current, ...h.future].slice(0, MAX_HISTORY), at: 0 } };
}

export function redo(h: History, current: Graph): { h: History; g: Graph } | null {
  if (h.future.length === 0) return null;
  const [g, ...rest] = h.future;
  return { g, h: { past: [...h.past, current].slice(-MAX_HISTORY), future: rest, at: 0 } };
}
