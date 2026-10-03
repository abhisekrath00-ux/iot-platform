// Web Mercator helpers for the map tile layer (256 px tiles, the usual slippy-map scheme).
export const TILE = 256;

export function worldPx(lat: number, lon: number, z: number): [number, number] {
  const n = TILE * 2 ** z;
  const s = Math.sin((Math.max(-85.0511, Math.min(85.0511, lat)) * Math.PI) / 180);
  return [((lon + 180) / 360) * n, (0.5 - Math.log((1 + s) / (1 - s)) / (4 * Math.PI)) * n];
}

/** Highest zoom (at most maxZ) at which all points fit inside w x h pixels. */
export function fitZoom(pts: [number, number][], w: number, h: number, maxZ = 17): number {
  for (let z = maxZ; z > 0; z--) {
    const p = pts.map(([la, lo]) => worldPx(la, lo, z));
    const xs = p.map(q => q[0]), ys = p.map(q => q[1]);
    if (Math.max(...xs) - Math.min(...xs) <= w && Math.max(...ys) - Math.min(...ys) <= h) return z;
  }
  return 1;
}

/** Tiles covering a viewport centred on world pixel (cx, cy), with the pixel offset of each tile's top left. */
export function tilesFor(cx: number, cy: number, z: number, w: number, h: number): { z: number; x: number; y: number; px: number; py: number }[] {
  const max = 2 ** z;
  const x0 = Math.floor((cx - w / 2) / TILE), x1 = Math.floor((cx + w / 2) / TILE);
  const y0 = Math.floor((cy - h / 2) / TILE), y1 = Math.floor((cy + h / 2) / TILE);
  const out: { z: number; x: number; y: number; px: number; py: number }[] = [];
  for (let y = y0; y <= y1; y++) {
    if (y < 0 || y >= max) continue;
    for (let x = x0; x <= x1; x++) {
      const wx = ((x % max) + max) % max;
      out.push({ z, x: wx, y, px: x * TILE - (cx - w / 2), py: y * TILE - (cy - h / 2) });
    }
  }
  return out;
}

/** Metres per pixel at a latitude and zoom. */
export const metresPerPx = (lat: number, z: number): number => (156543.03392 * Math.cos((lat * Math.PI) / 180)) / 2 ** z;
