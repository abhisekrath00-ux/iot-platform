import { useEffect, useState } from 'react';
import { api } from '../lib/api';

type ExportRow = { id: string; format: string; web_link: string; status: string; error: string; created_at: string };

// Drive export for one report: uploads a fresh render to the connected
// Google Drive (HTML file, or CSV converted into a Google Sheet). Every
// state is honest: without a Workspace connection the server answers 409
// and that message is shown, nothing pretends to have exported.
export default function ReportDrive({ reportId }: { reportId: string }) {
  const [latest, setLatest] = useState<ExportRow | null>(null);
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState('');
  const load = () => api<{ exports: ExportRow[] }>(`/v1/reports/${reportId}/exports`)
    .then(r => setLatest(r.exports.find(e => e.status === 'exported') || null)).catch(() => {});
  useEffect(() => { load(); }, [reportId]);
  const doExport = async (format: 'html' | 'sheet') => {
    setBusy(format); setErr('');
    try {
      const r = await api<{ web_link: string }>(`/v1/reports/${reportId}/export/drive`, { method: 'POST', body: JSON.stringify({ format }) });
      setLatest({ id: '', format, web_link: r.web_link, status: 'exported', error: '', created_at: new Date().toISOString() });
      load();
    } catch (e: any) { setErr(String(e?.message || e)); }
    setBusy('');
  };
  return (
    <span data-testid={`report-drive-${reportId}`} style={{ display: 'contents' }}>
      <span style={{ display: 'inline-flex', gap: 8, alignItems: 'center' }}>
        <button className="ghost" disabled={!!busy} onClick={() => doExport('html')}
          title="Upload a fresh HTML render as a new file in the connected Google Drive">
          {busy === 'html' ? 'Exporting…' : 'Drive HTML'}</button>
        <button className="ghost" disabled={!!busy} onClick={() => doExport('sheet')}
          title="Upload the CSV render as a new Google Sheet in the connected Google Drive">
          {busy === 'sheet' ? 'Exporting…' : 'Drive Sheet'}</button>
        {latest && <a href={latest.web_link} target="_blank" rel="noreferrer">Last export ({latest.format})</a>}
      </span>
      {err && (
        <span role="alert" className="muted"
          style={{ flexBasis: '100%', maxWidth: 460, whiteSpace: 'normal', wordBreak: 'break-word', marginTop: 4, paddingRight: 130 }}>
          {err}
        </span>
      )}
    </span>
  );
}
