export function parseStages(text: string): { stages: number[]; error: string } {
  const parts = text.split(',').map((s) => s.trim()).filter(Boolean);
  const stages = parts.map(Number);
  if (!parts.length || stages.some((n) => !Number.isInteger(n))) return { stages: [], error: 'Use whole numbers separated by commas, e.g. 10,50,100.' };
  if (stages.length > 10) return { stages: [], error: 'At most 10 stages.' };
  for (let i = 0; i < stages.length; i++) {
    if (stages[i] < 1 || stages[i] > 100) return { stages: [], error: 'Each stage is a percent from 1 to 100.' };
    if (i > 0 && stages[i] <= stages[i - 1]) return { stages: [], error: 'Stages must increase.' };
  }
  if (stages[stages.length - 1] !== 100) return { stages: [], error: 'The last stage must be 100.' };
  return { stages, error: '' };
}

export interface CampaignRow { id: string; name: string; state: string; stages: number[]; stage_index: number; acked: number; failed: number; open: number; }

/** Buttons that make sense for a campaign state. The server enforces the same rules. */
export function allowedActions(c: Pick<CampaignRow, 'state' | 'stage_index' | 'stages' | 'open' | 'failed'>): string[] {
  const last = c.stage_index >= c.stages.length - 1;
  switch (c.state) {
    case 'draft': return ['start', 'abort'];
    case 'running': return [...(c.open === 0 && c.failed === 0 && !last ? ['advance'] : []), 'pause', 'abort', 'rollback'];
    case 'paused': return ['start', 'abort', 'rollback'];
    case 'done': return ['rollback'];
    default: return [];
  }
}

export function progress(c: Pick<CampaignRow, 'acked' | 'failed' | 'open'>): { total: number; pct: number } {
  const total = c.acked + c.failed + c.open;
  return { total, pct: total ? Math.round((c.acked / total) * 100) : 0 };
}
