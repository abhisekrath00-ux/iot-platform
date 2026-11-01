import { beforeEach, expect, test, vi } from 'vitest';
import { api } from './api';
import { loadPointCatalog } from './pointCatalog';
vi.mock('./api', () => ({ api: vi.fn() }));
const row = (n: number) => ({ device_id: 'd', device_name: 'Meter', point_id: `p${n}`, unit: 'C' });
beforeEach(() => { vi.mocked(api).mockReset(); });
test('loads more than5000 rows and encodes opaque cursor', async () => {
  let n = 0;
  vi.mocked(api).mockImplementation(async path => {
    if (n) expect(path).toContain(`after=${encodeURIComponent(`page ${n}+`)}`);
    const page = n++;
    return { items: Array.from({ length: page < 5 ? 1000 : 2 }, (_, i) => row(page * 1000 + i)), ...(page < 5 ? { next_cursor: `page ${n}+` } : {}) };
  });
  expect(await loadPointCatalog()).toHaveLength(5002);
});
test('later failure rejects whole traversal', async () => {
  vi.mocked(api).mockResolvedValueOnce({ items: [row(1)], next_cursor: 'next' }).mockRejectedValueOnce(Error('503'));
  await expect(loadPointCatalog()).rejects.toThrow('503');
});
test.each([
  { items: null }, { items: [{}] }, { items: [row(1)], next_cursor: 9 },
  { items: [], next_cursor: 'next' }, { items: Array.from({length:1001},(_,i)=>row(i)) }
])('rejects malformed pages', async response => {
  vi.mocked(api).mockResolvedValue(response); await expect(loadPointCatalog()).rejects.toThrow();
});
test('duplicate rows and repeated cursors fail instead of partial success', async () => {
  vi.mocked(api).mockResolvedValueOnce({ items: [row(1)], next_cursor: 'a' }).mockResolvedValueOnce({ items: [row(1)] });
  await expect(loadPointCatalog()).rejects.toThrow('changed');
  vi.mocked(api).mockResolvedValueOnce({ items: [row(1)], next_cursor: 'a' }).mockResolvedValueOnce({ items: [row(2)], next_cursor: 'a' });
  await expect(loadPointCatalog()).rejects.toThrow('repeated');
});
test('100-page cap is an error, not silent truncation', async () => {
  let n=0;vi.mocked(api).mockImplementation(async()=>({items:[row(n++)],next_cursor:String(n)}));
  await expect(loadPointCatalog()).rejects.toThrow('100000');expect(api).toHaveBeenCalledTimes(100);
});
