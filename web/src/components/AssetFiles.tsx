import { useEffect, useRef, useState } from 'react';
import { api, download } from '../lib/api';

interface F { id: string; filename: string; size_bytes: number; sha256: string; uploaded_by: string | null; created_at: string; }
const kb = (n: number) => (n >= 1048576 ? `${(n / 1048576).toFixed(1)} MB` : `${Math.max(1, Math.round(n / 1024))} KB`);

/** Manuals, drawings and photos attached to one asset. Up to 5 MB each: pdf, png, jpg, txt, csv. */
export default function AssetFiles({ assets }: { assets: { id: string; name: string }[] }) {
  const [assetId, setAssetId] = useState('');
  const [files, setFiles] = useState<F[]>([]);
  const [msg, setMsg] = useState('');
  const input = useRef<HTMLInputElement>(null);
  const load = () => { if (assetId) api<F[]>(`/v1/assets/${assetId}/files`).then(setFiles).catch(e => setMsg(String(e))); else setFiles([]); };
  useEffect(load, [assetId]);

  const upload = async (file: File) => {
    setMsg('');
    const token = localStorage.getItem('iot.token') ?? '';
    const res = await fetch(`/v1/assets/${assetId}/files?name=${encodeURIComponent(file.name)}`, { method: 'POST', body: file, headers: token ? { Authorization: `Bearer ${token}` } : {} });
    if (!res.ok) setMsg(`${res.status}: ${await res.text()}`); else { setMsg('Uploaded.'); load(); }
    if (input.current) input.current.value = '';
  };

  return (
    <div className="card" style={{ maxWidth: 640, marginBottom: 20 }}>
      <b>Asset files</b>
      <label htmlFor="af-asset">Asset</label>
      <select id="af-asset" value={assetId} onChange={e => setAssetId(e.target.value)}>
        <option value="">Select an asset</option>
        {assets.map(a => <option key={a.id} value={a.id}>{a.name}</option>)}
      </select>
      {assetId && (
        <>
          <table style={{ marginTop: 10 }}>
            <thead><tr><th>File</th><th>Size</th><th>By</th><th /></tr></thead>
            <tbody>
              {files.map(f => (
                <tr key={f.id}>
                  <td><button className="ghost" onClick={() => download(`/v1/assets/${assetId}/files/${f.id}`, f.filename)}>{f.filename}</button></td>
                  <td>{kb(f.size_bytes)}</td><td>{f.uploaded_by ?? '-'}</td>
                  <td><button className="ghost" aria-label={`Delete ${f.filename}`} onClick={() => api(`/v1/assets/${assetId}/files/${f.id}`, { method: 'DELETE' }).catch(() => {}).then(load)}>Delete</button></td>
                </tr>
              ))}
              {files.length === 0 && <tr><td colSpan={4} className="muted">No files on this asset.</td></tr>}
            </tbody>
          </table>
          <label htmlFor="af-file">Upload (pdf, png, jpg, txt, csv; 5 MB max)</label>
          <input id="af-file" ref={input} type="file" accept=".pdf,.png,.jpg,.jpeg,.txt,.csv" onChange={e => { const f = e.target.files?.[0]; if (f) upload(f); }} />
        </>
      )}
      {msg && <p className="muted" role="status">{msg}</p>}
    </div>
  );
}
