import { api } from './api';
export interface CatalogPoint { device_id: string; device_name: string; point_id: string; unit: string; }
interface CatalogPage { items: CatalogPoint[]; next_cursor?: string; }
// No partial result escapes a failed page. This is a current-read traversal,
// not a snapshot: refresh after concurrent configuration changes. Bound browser
// memory/work at 100000 rows; exceeding it is an explicit error, never truncation.
export async function loadPointCatalog(): Promise<CatalogPoint[]> {
  const out: CatalogPoint[] = [];
  const cursors = new Set<string>();
  const keys = new Set<string>();
  let after = '';
  for (let page = 0; page < 100; page++) {
    const r = await api<CatalogPage>(`/v1/points?page_size=1000${after ? `&after=${encodeURIComponent(after)}` : ''}`);
    if (!r || !Array.isArray(r.items) || r.items.length > 1000 || (r.next_cursor !== undefined && typeof r.next_cursor !== 'string')) throw new Error('The server returned an incomplete point catalog page.');
    for (const p of r.items) {
      if (!p || typeof p.device_id !== 'string' || !p.device_id || typeof p.device_name !== 'string' || typeof p.point_id !== 'string' || !p.point_id || typeof p.unit !== 'string') throw new Error('The server returned an invalid point catalog row.');
      const key = JSON.stringify([p.device_id, p.point_id]);
      if (keys.has(key)) throw new Error('The point catalog changed during loading. Refresh to try again.');
      keys.add(key); out.push(p);
    }
    if (!r.next_cursor) return out;
    if (!r.items.length || cursors.has(r.next_cursor)) throw new Error('The server repeated a point catalog cursor.');
    cursors.add(r.next_cursor); after = r.next_cursor;
  }
  throw new Error('The point catalog needs more than 100 pages (up to 100000 rows). A complete browser catalog is unavailable.');
}
