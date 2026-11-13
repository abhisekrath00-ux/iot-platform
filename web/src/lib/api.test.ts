// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { api, download } from './api';

describe('api client', () => {
  beforeEach(() => {
    localStorage.clear();
    vi.restoreAllMocks();
  });

  it('sends the bearer token from localStorage', async () => {
    localStorage.setItem('iot.token', 'tok123');
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ ok: true }), { status: 200 }));
    const out = await api<{ ok: boolean }>('/v1/devices');
    expect(out.ok).toBe(true);
    const headers = new Headers(fetchMock.mock.calls[0][1]?.headers);
    expect(headers.get('Authorization')).toBe('Bearer tok123');
    expect(headers.get('Content-Type')).toBe('application/json');
  });

  it('sends no auth header without a token', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('[]', { status: 200 }));
    await api('/v1/devices');
    const headers = new Headers(fetchMock.mock.calls[0][1]?.headers);
    expect(headers.get('Authorization')).toBeNull();
  });

  it('surfaces non-ok status and body', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('nope', { status: 403 }));
    await expect(api('/v1/devices')).rejects.toThrow('403: nope');
  });
});


describe('authenticated URL boundary', () => {
  beforeEach(() => { localStorage.clear(); vi.restoreAllMocks(); });

  it.each([
    'https://untrusted.invalid/v1/export', 'http://untrusted.invalid/',
    '//untrusted.invalid/export', '/\\untrusted.invalid/export',
    '\\untrusted.invalid', 'v1/devices', '', ' https://untrusted.invalid/',
    '/\nuntrusted.invalid/export', '/v1/\rexport', '/v1/\texport',
    'data:text/plain,secret', 'javascript:alert(1)', '/v1/white space',
  ])('refuses nonlocal or ambiguous paths before fetch: %j', async path => {
    localStorage.setItem('iot.token', 'boundary-token');
    const fetchMock = vi.spyOn(globalThis, 'fetch');
    await expect(api(path)).rejects.toThrow('local application path');
    await expect(download(path, 'export.csv')).rejects.toThrow('local application path');
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('allows local query strings without interpreting their values as destinations', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('{}'));
    await api('/v1/reports/report%201?window_hours=24&note=https%3A%2F%2Fexample.invalid');
    expect(fetchMock.mock.calls[0][0]).toBe('/v1/reports/report%201?window_hours=24&note=https%3A%2F%2Fexample.invalid');
  });

  it('cannot be configured to follow redirects or send cross-origin cookies', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('{}'));
    await api('/v1/devices', { redirect: 'follow', credentials: 'include' });
    expect(fetchMock.mock.calls[0][1]).toMatchObject({ redirect: 'error', credentials: 'same-origin' });
  });

  it('download refuses redirects and surfaces failure without creating a file', async () => {
    localStorage.setItem('iot.token', 'boundary-token');
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockRejectedValue(new TypeError('redirect refused'));
    await expect(download('/v1/reports/r?format=csv', 'report.csv')).rejects.toThrow('redirect refused');
    expect(fetchMock.mock.calls[0][1]).toMatchObject({ redirect: 'error', credentials: 'same-origin', headers: { Authorization: 'Bearer boundary-token' } });
    expect(document.querySelector('a[download]')).toBeNull();
  });
});

describe('installer integrity before browser save', () => {
 afterEach(()=>vi.unstubAllGlobals());
  const bytes = new Uint8Array([77,90,1,2]);
  let create: ReturnType<typeof vi.fn<(obj: Blob | MediaSource) => string>>,click:ReturnType<typeof vi.spyOn>;
  beforeEach(async()=>{
    vi.restoreAllMocks();
    const {webcrypto}=await import('node:crypto');vi.stubGlobal('crypto',webcrypto);
    create=vi.fn<(obj: Blob | MediaSource) => string>().mockReturnValue('blob:verified-fixture');
    vi.stubGlobal('URL',class extends URL {static createObjectURL=create;static revokeObjectURL=vi.fn();});
    click=vi.spyOn(HTMLAnchorElement.prototype,'click').mockImplementation(()=>{});
    vi.spyOn(globalThis,'fetch').mockImplementation(async()=>new Response(bytes));
  });
  it('uses actual SHA256 and saves only byte-matching content',async()=>{
    const hash=await crypto.subtle.digest('SHA-256',bytes);const sha256=Array.from(new Uint8Array(hash),b=>b.toString(16).padStart(2,'0')).join('');
    await download('/v1/edge-packages/windows-amd64-exe','actual.exe',{size:bytes.length,sha256});
    expect(create).toHaveBeenCalledTimes(1);expect(click).toHaveBeenCalledTimes(1);
  });
  it('refuses same-size corruption with no object URL or save click',async()=>{
    await expect(download('/v1/edge-packages/windows-amd64-exe','actual.exe',{size:bytes.length,sha256:'0'.repeat(64)})).rejects.toThrow('SHA256 changed');expect(create).not.toHaveBeenCalled();expect(click).not.toHaveBeenCalled();
  });
  it('refuses wrong size before hashing or save',async()=>{
    await expect(download('/v1/edge-packages/windows-amd64-exe','actual.exe',{size:bytes.length+1,sha256:'0'.repeat(64)})).rejects.toThrow('size changed');expect(create).not.toHaveBeenCalled();expect(click).not.toHaveBeenCalled();
  });
  it('refuses unavailable secure crypto rather than silently skipping verification',async()=>{
    vi.stubGlobal('crypto',{});await expect(download('/v1/edge-packages/windows-amd64-exe','actual.exe',{size:bytes.length,sha256:'0'.repeat(64)})).rejects.toThrow('secure browser connection');expect(create).not.toHaveBeenCalled();expect(click).not.toHaveBeenCalled();
  });
  it('invalid inventory is refused before fetching',async()=>{
    vi.mocked(fetch).mockClear();await expect(download('/v1/edge-packages/windows-amd64-exe','actual.exe',{size:0,sha256:'invalid'})).rejects.toThrow('Invalid installer');expect(fetch).not.toHaveBeenCalled();
  });
});
