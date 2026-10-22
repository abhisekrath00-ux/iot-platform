export interface GroupRef { id: string; name: string; parent_id?: string | null; }

/** Display path for a (possibly nested) group: "Plant > Line 1 > Cell A".
    Malformed data (missing parent, loop) stops the walk instead of hanging. */
export function groupPath<T extends GroupRef>(g: T, all: T[]): string {
  const byId = new Map(all.map(x => [x.id, x]));
  const parts = [g.name || g.id];
  const seen = new Set([g.id]);
  let cur = g.parent_id || '';
  while (cur && !seen.has(cur)) {
    const p = byId.get(cur);
    if (!p) break;
    seen.add(cur);
    parts.unshift(p.name || p.id);
    cur = p.parent_id || '';
  }
  return parts.join(' > ');
}

/** Groups in display order: parents before children, alphabetical within a level. */
export function sortGroupsByTree<T extends GroupRef>(all: T[]): T[] {
  const byParent = new Map<string, T[]>();
  for (const g of all) {
    const k = g.parent_id || '';
    byParent.set(k, [...(byParent.get(k) ?? []), g]);
  }
  for (const v of byParent.values()) v.sort((a, b) => (a.name || a.id).localeCompare(b.name || b.id));
  const out: T[] = [];
  const walk = (parent: string, depth: number) => {
    if (depth > 12) return; // data guard, never a real tree
    for (const g of byParent.get(parent) ?? []) { out.push(g); walk(g.id, depth + 1); }
  };
  walk('', 0);
  // Orphans whose parent is missing from the list still appear.
  for (const g of all) if (!out.includes(g)) out.push(g);
  return out;
}
