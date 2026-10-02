/** One-line summary of a stored flow. Step-list flows have a trigger and steps;
 * graph flows built in the editor have neither, so every field is optional. */
export interface FlowSummaryDef {
  trigger?: { device_id: string; point_id: string; op: string; value: number } | null;
  steps?: unknown[] | null;
  graph?: { nodes?: unknown[] | null } | null;
  cooldown_seconds?: number;
  latch?: boolean;
}

export function flowSummary(d: FlowSummaryDef | null | undefined): string {
  if (!d) return '';
  const parts: string[] = [];
  if (d.trigger && d.trigger.device_id) parts.push(`${d.trigger.device_id}/${d.trigger.point_id} ${d.trigger.op} ${d.trigger.value}`);
  if (d.steps) parts.push(`${d.steps.length} steps`);
  else if (d.graph?.nodes) parts.push(`graph, ${d.graph.nodes.length} nodes`);
  else if (d.graph) parts.push('graph flow');
  if (d.cooldown_seconds) parts.push(`quiet for ${Math.round(d.cooldown_seconds / 60)} min after a notification`);
  if (d.latch) parts.push('latched');
  return parts.join(' - ');
}
