// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest';
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
