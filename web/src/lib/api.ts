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

export interface Device { id: string; profile: string; name: string; gateway_id: string; created_at: string; }
export interface LatestPoint { point_id: string; value: number; unit: string; quality: string; observed_at: string; }
export interface CommandRow { request_id: string; device_id: string; action: string; status: string; requested_by: string; approved_by: string | null; created_at: string; }
export interface AlertRow { id: string; severity: string; message: string; status: string; created_at: string; }

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

export const NETWORK_DRIVERS = ['modbus-tcp', 'opcua'];
