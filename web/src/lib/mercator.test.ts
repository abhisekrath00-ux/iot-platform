import { describe, expect, it } from 'vitest';
import { fitZoom, metresPerPx, tilesFor, worldPx } from './mercator';

describe('mercator', () => {
  it('puts (0,0) at the middle of the world and the date line at the edges', () => {
    expect(worldPx(0, 0, 1)).toEqual([256, 256]);
    expect(worldPx(0, -180, 2)[0]).toBe(0);
    expect(worldPx(0, 180, 2)[0]).toBe(1024);
  });
  it('matches a known tile: London at z10 is tile 511,340', () => {
    const [x, y] = worldPx(51.5074, -0.1278, 10);
    expect([Math.floor(x / 256), Math.floor(y / 256)]).toEqual([511, 340]);
  });
  it('fits two points 1 km apart at a street-level zoom, and never above maxZ', () => {
    const z = fitZoom([[12.97, 77.59], [12.979, 77.59]], 600, 380);
    expect(z).toBeGreaterThanOrEqual(15);
    expect(z).toBeLessThanOrEqual(17);
    expect(fitZoom([[1, 1]], 600, 380)).toBe(17);
  });
  it('lists only valid tiles and wraps x', () => {
    const t = tilesFor(0, 128, 1, 300, 100);
    expect(t.every(q => q.x >= 0 && q.x < 2 && q.y >= 0 && q.y < 2)).toBe(true);
    expect(t.length).toBeGreaterThan(0);
  });
  it('metres per pixel halves per zoom level and shrinks away from the equator', () => {
    expect(metresPerPx(0, 1) / metresPerPx(0, 2)).toBeCloseTo(2);
    expect(metresPerPx(60, 5)).toBeLessThan(metresPerPx(0, 5));
  });
});
