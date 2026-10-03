// API client. The token comes from the session; dev builds accept a dev token
// in localStorage. OIDC login flow lands before pilot.
export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const token = localStorage.getItem('iot.token') ?? '';
  const res = await fetch(path, {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      ...(init?.headers ?? {})
    }
  });
  if (!res.ok) throw new Error(`${res.status}: ${await res.text()}`);
  return res.json();
}

export interface Device { id: string; profile: string; name: string; gateway_id: string; created_at: string; tags?: string[]; }
export interface LatestPoint { point_id: string; value: number; unit: string; quality: string; observed_at: string; }
export interface CommandRow { request_id: string; device_id: string; action: string; status: string; requested_by: string; approved_by: string | null; created_at: string; }
export interface AlertRow { id: string; severity: string; message: string; status: string; created_at: string; acknowledged_by?: string | null; resolved_by?: string | null; assigned_to?: string | null; shelved?: boolean; }
export interface AlertDetail extends AlertRow { acknowledged_at?: string | null; resolved_at?: string | null; comments: { author: string; body: string; created_at: string }[]; }

/** Authenticated file download (the token lives in localStorage, so a plain link will not do). */
export async function download(path: string, filename: string): Promise<void> {
  const token = localStorage.getItem('iot.token') ?? '';
  const res = await fetch(path, { headers: token ? { Authorization: `Bearer ${token}` } : {} });
  if (!res.ok) throw new Error(`${res.status}: ${await res.text()}`);
  const url = URL.createObjectURL(await res.blob());
  const a = document.createElement('a');
  a.href = url; a.download = filename; a.click();
  URL.revokeObjectURL(url);
}

export const NETWORK_DRIVERS = ['modbus-tcp', 'opcua', 'snmp', 'bacnet', 'iec104', 'dnp3', 'coap', 'iec61850'];
// Later network drivers: tested against simulators only (docs/connectors.md). f = the point address field.
export const EXTRA_DRIVERS: Record<string, { label: string; f: 'oid' | 'ioa' | 'key'; ph: string; reg?: boolean; unit?: boolean }> = {
  snmp: { label: 'SNMP v2c/v3 (switches, UPS, PDUs)', f: 'oid', ph: '.1.3.6.1.2.1.1.3.0' },
  bacnet: { label: 'BACnet/IP (building automation)', f: 'key', ph: 'ai:1' },
  iec104: { label: 'IEC 60870-5-104 (utility RTU)', f: 'ioa', ph: '100', unit: true },
  dnp3: { label: 'DNP3 master (utility)', f: 'key', ph: 'ai | bi | ctr | bo', reg: true, unit: true },
  coap: { label: 'CoAP (constrained devices)', f: 'key', ph: 'sensors/temp#v' },
  iec61850: { label: 'IEC 61850 MMS (partial, read-only)', f: 'key', ph: 'LD0/MMXU1.TotW.mag.f' },
};
