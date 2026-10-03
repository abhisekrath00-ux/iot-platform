import { useEffect, useMemo, useState } from 'react';
import Empty from '../components/Empty';
import { api, Device } from '../lib/api';
import { fitZoom, metresPerPx, TILE, tilesFor, worldPx } from '../lib/mercator';

interface MDev { id: string; name: string; lat: number; lon: number; inside: string[]; }
interface Fence { id: string; name: string; lat: number; lon: number; radius_m: number; }
interface MapData { devices: MDev[]; geofences: Fence[]; note: string; }

const W = 760, H = 460, PAD = 40;

/** Site map drawn as plain SVG. No tile server, so it works air-gapped; the plot is relative, not a street map. */
export default function MapPage() {
  const [data, setData] = useState<MapData>({ devices: [], geofences: [], note: '' });
  const [devices, setDevices] = useState<Device[]>([]);
  const [devId, setDevId] = useState('');
  const [lat, setLat] = useState(''); const [lon, setLon] = useState('');
  const [fname, setFname] = useState(''); const [flat, setFlat] = useState(''); const [flon, setFlon] = useState(''); const [frad, setFrad] = useState('200');
  const [msg, setMsg] = useState('');
  const [cfg, setCfg] = useState<{ tiles: boolean; attribution: string }>({ tiles: false, attribution: '' });
  const [imgs, setImgs] = useState<Record<string, string>>({});
  useEffect(() => { api<{ tiles: boolean; attribution: string }>('/v1/map/config').then(setCfg).catch(() => undefined); }, []);
  const load = () => {
    api<MapData>('/v1/map').then(setData).catch(e => setMsg(String(e)));
    api<Device[]>('/v1/devices').then(setDevices).catch(() => {});
  };
  useEffect(load, []);
  const act = async (fn: () => Promise<unknown>, ok: string) => { setMsg(''); try { await fn(); setMsg(ok); load(); } catch (e) { setMsg(String(e)); } };

  const proj = useMemo(() => {
    const pts: [number, number][] = [];
    data.devices.forEach(d => pts.push([d.lat, d.lon]));
    data.geofences.forEach(f => {
      const dLat = f.radius_m / 111195, dLon = f.radius_m / (111195 * Math.max(0.01, Math.cos(f.lat * Math.PI / 180)));
      pts.push([f.lat - dLat, f.lon - dLon], [f.lat + dLat, f.lon + dLon]);
    });
    if (pts.length === 0) return null;
    if (cfg.tiles) {
      const z = fitZoom(pts, W - 2 * PAD, H - 2 * PAD);
      const wp = pts.map(([la, lo]) => worldPx(la, lo, z));
      const cx = (Math.min(...wp.map(q => q[0])) + Math.max(...wp.map(q => q[0]))) / 2;
      const cy = (Math.min(...wp.map(q => q[1])) + Math.max(...wp.map(q => q[1]))) / 2;
      const midLat = (Math.min(...pts.map(q => q[0])) + Math.max(...pts.map(q => q[0]))) / 2;
      return {
        x: (lo: number) => W / 2 + worldPx(0, lo, z)[0] - cx,
        y: (la: number) => H / 2 + worldPx(la, 0, z)[1] - cy,
        mPerPx: metresPerPx(midLat, z),
        tiles: tilesFor(cx, cy, z, W, H),
      };
    }
    const lats = pts.map(p => p[0]), lons = pts.map(p => p[1]);
    const mid = (Math.min(...lats) + Math.max(...lats)) / 2;
    const k = Math.max(0.01, Math.cos(mid * Math.PI / 180));
    const minLat = Math.min(...lats), minLon = Math.min(...lons);
    const spanY = Math.max(1e-6, Math.max(...lats) - minLat), spanX = Math.max(1e-6, (Math.max(...lons) - minLon) * k);
    const scale = Math.min((W - 2 * PAD) / spanX, (H - 2 * PAD) / spanY);
    return {
      x: (lo: number) => PAD + (lo - minLon) * k * scale + (W - 2 * PAD - spanX * scale) / 2,
      y: (la: number) => H - PAD - (la - minLat) * scale - (H - 2 * PAD - spanY * scale) / 2,
      mPerPx: 111195 / scale,
      tiles: [] as ReturnType<typeof tilesFor>,
    };
  }, [data, cfg]);

  // Tiles are fetched through the API (it relays the deployer's tile server) and shown from blob URLs.
  useEffect(() => {
    if (!proj || proj.tiles.length === 0) return;
    let dead = false;
    const token = localStorage.getItem('iot.token') ?? '';
    proj.tiles.slice(0, 40).forEach(t => {
      const k = `${t.z}/${t.x}/${t.y}`;
      if (imgs[k]) return;
      fetch(`/v1/map/tiles/${k}`, { headers: { Authorization: `Bearer ${token}` } }).then(r => (r.ok ? r.blob() : null)).then(b => { if (b && !dead) setImgs(m => (m[k] ? m : { ...m, [k]: URL.createObjectURL(b) })); }).catch(() => undefined);
    });
    return () => { dead = true; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [proj]);

  return (
    <>
      <h1>Map</h1>
      <p className="muted">Where devices are and which zones they sit in. Positions are entered by people, not read from telemetry. {cfg.tiles ? 'Drawn over map tiles from your own tile server. ' : 'Plotted on a plain canvas (no map tiles), so it works with no internet. '}Zones are circles; there are no enter or exit alerts.</p>
      {msg && <p className="muted" role="status">{msg}</p>}
      {!proj
        ? <Empty title="Nothing on the map yet" hint="Give a device a position below, and optionally add a zone." />
        : (
          <div className="card" style={{ maxWidth: W + 40 }}>
            <svg role="img" aria-label="Site map" width="100%" viewBox={`0 0 ${W} ${H}`} style={{ display: 'block' }}>
              {proj.tiles.map(t => imgs[`${t.z}/${t.x}/${t.y}`] && <image key={`${t.z}/${t.x}/${t.y}/${t.px}`} href={imgs[`${t.z}/${t.x}/${t.y}`]} x={t.px} y={t.py} width={TILE} height={TILE} />)}
              {data.geofences.map(f => (
                <g key={f.id}>
                  <circle cx={proj.x(f.lon)} cy={proj.y(f.lat)} r={f.radius_m / proj.mPerPx} fill="var(--accent, #4f8cff)" fillOpacity="0.1" stroke="var(--accent, #4f8cff)" strokeDasharray="5 4" />
                  <text x={proj.x(f.lon)} y={proj.y(f.lat) - f.radius_m / proj.mPerPx - 6} textAnchor="middle" fontSize="12" fill="currentColor">{f.name}</text>
                </g>
              ))}
              {data.devices.map(d => (
                <g key={d.id}>
                  <circle cx={proj.x(d.lon)} cy={proj.y(d.lat)} r="6" fill={d.inside.length ? 'var(--ok, #248a3d)' : 'var(--muted, #6e6e73)'} stroke="#fff" strokeWidth="1.5"><title>{d.name}{d.inside.length ? ` (in ${d.inside.join(', ')})` : ''}</title></circle>
                  <text x={proj.x(d.lon) + 9} y={proj.y(d.lat) + 4} fontSize="12" fill="currentColor">{d.name}</text>
                </g>
              ))}
            </svg>
            <div className="muted" style={{ fontSize: 12 }}>Scale: 100 px is about {Math.round(proj.mPerPx * 100)} m. Green devices are inside a zone.{cfg.tiles && cfg.attribution ? ` Map: ${cfg.attribution}` : ''}</div>
          </div>
        )}
      {data.devices.length > 0 && (
        <table style={{ maxWidth: 640, marginTop: 14 }}>
          <thead><tr><th>Device</th><th>Position</th><th>Inside</th></tr></thead>
          <tbody>{data.devices.map(d => <tr key={d.id}><td>{d.name}</td><td>{d.lat.toFixed(5)}, {d.lon.toFixed(5)}</td><td>{d.inside.join(', ') || '-'}</td></tr>)}</tbody>
        </table>
      )}
      <div className="card" style={{ maxWidth: 640, marginTop: 20 }}>
        <b>Set a device position</b>
        <label htmlFor="mp-dev">Device</label>
        <select id="mp-dev" value={devId} onChange={e => setDevId(e.target.value)}><option value="">Select a device</option>{devices.map(d => <option key={d.id} value={d.id}>{d.name || d.id}</option>)}</select>
        <label htmlFor="mp-lat">Latitude</label><input id="mp-lat" inputMode="decimal" value={lat} onChange={e => setLat(e.target.value)} placeholder="12.9716" />
        <label htmlFor="mp-lon">Longitude</label><input id="mp-lon" inputMode="decimal" value={lon} onChange={e => setLon(e.target.value)} placeholder="77.5946" />
        <div style={{ marginTop: 14, display: 'flex', gap: 8 }}>
          <button disabled={!devId || lat === '' || lon === ''} onClick={() => act(() => api(`/v1/devices/${devId}/location`, { method: 'PUT', body: JSON.stringify({ lat: Number(lat), lon: Number(lon) }) }), 'Saved.')}>Save position</button>
          <button className="ghost" disabled={!devId} onClick={() => act(() => api(`/v1/devices/${devId}/location`, { method: 'PUT', body: JSON.stringify({ lat: null, lon: null }) }), 'Cleared.')}>Clear</button>
        </div>
      </div>
      <div className="card" style={{ maxWidth: 640, marginTop: 20 }}>
        <b>Zones</b>
        <ul style={{ padding: 0, listStyle: 'none' }}>
          {data.geofences.map(f => <li key={f.id} style={{ display: 'flex', gap: 10, alignItems: 'center', padding: '4px 0' }}><span>{f.name}</span><span className="muted">{f.radius_m} m at {f.lat.toFixed(4)}, {f.lon.toFixed(4)}</span><span style={{ flex: 1 }} /><button className="ghost" aria-label={`Delete ${f.name}`} onClick={() => act(() => api(`/v1/geofences/${f.id}`, { method: 'DELETE' }).catch(() => {}), 'Deleted.')}>Delete</button></li>)}
          {data.geofences.length === 0 && <li className="muted">No zones yet.</li>}
        </ul>
        <form onSubmit={e => { e.preventDefault(); act(() => api('/v1/geofences', { method: 'POST', body: JSON.stringify({ name: fname, lat: Number(flat), lon: Number(flon), radius_m: Number(frad) }) }), 'Zone added.').then(() => setFname('')); }}>
          <label htmlFor="mz-name">Name</label><input id="mz-name" value={fname} onChange={e => setFname(e.target.value)} required maxLength={80} />
          <label htmlFor="mz-lat">Centre latitude</label><input id="mz-lat" inputMode="decimal" value={flat} onChange={e => setFlat(e.target.value)} required />
          <label htmlFor="mz-lon">Centre longitude</label><input id="mz-lon" inputMode="decimal" value={flon} onChange={e => setFlon(e.target.value)} required />
          <label htmlFor="mz-rad">Radius (metres)</label><input id="mz-rad" inputMode="decimal" value={frad} onChange={e => setFrad(e.target.value)} required />
          <div style={{ marginTop: 14 }}><button type="submit">Add zone</button></div>
        </form>
      </div>
    </>
  );
}
