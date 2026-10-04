import { useEffect, useState } from 'react';
import { api } from '../lib/api';

// Admin-only control: share a dashboard or report with one customer (and its sub-customers).
// The server refuses a share that would expose a device outside that customer.
export default function ShareWithCustomer({ path, value, onDone, onError }: { path: string; value?: string | null; onDone: () => void; onError: (m: string) => void }) {
  const [customers, setCustomers] = useState<{ id: string; name: string }[]>([]);
  useEffect(() => { api<typeof customers>('/v1/customers').then(setCustomers).catch(() => setCustomers([])); }, []);
  if (customers.length === 0) return null;
  const change = async (v: string) => {
    try {
      await api(path, { method: 'PUT', body: JSON.stringify({ customer_id: v || null }) });
      onDone();
    } catch (e) { onError(String(e)); }
  };
  return (
    <label style={{ display: 'inline-flex', gap: 6, alignItems: 'center' }}>
      <span className="muted">Shared with</span>
      <select aria-label="Share with customer" value={value ?? ''} onChange={e => change(e.target.value)} style={{ width: 170 }}>
        <option value="">Not shared</option>
        {customers.map(c => <option key={c.id} value={c.id}>{c.name}</option>)}
      </select>
    </label>
  );
}
