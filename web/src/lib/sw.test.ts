import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';

// The service worker is plain JS served from /sw.js; load its routing function the way a browser would see it.
const src = readFileSync(new URL('../../public/sw.js', import.meta.url), 'utf8');
const mod: { exports: { route?: (m: string, u: string, mode: string, o: string) => string } } = { exports: {} };
new Function('module', 'self', src)(mod, undefined);
const route = mod.exports.route!;
const O = 'https://iot.example.test';

describe('service worker routing', () => {
  it('never touches the API, auth or health endpoints', () => {
    for (const p of ['/v1/devices', '/v1/me', '/auth/login', '/auth/oidc/login', '/healthz', '/v1/reports/x/download?format=pdf']) {
      expect(route('GET', O + p, 'cors', O)).toBe('bypass');
      expect(route('GET', O + p, 'navigate', O)).toBe('bypass');
    }
  });
  it('never touches non-GET requests or other origins', () => {
    expect(route('POST', O + '/assets/app.js', 'cors', O)).toBe('bypass');
    expect(route('GET', 'https://cdn.example.com/assets/app.js', 'cors', O)).toBe('bypass');
  });
  it('caches hashed assets and falls back to the shell for navigations', () => {
    expect(route('GET', O + '/assets/index-abc123.js', 'no-cors', O)).toBe('asset');
    expect(route('GET', O + '/devices', 'navigate', O)).toBe('nav');
    expect(route('GET', O + '/', 'navigate', O)).toBe('nav');
  });
  it('does not cache the worker itself or arbitrary fetches', () => {
    expect(route('GET', O + '/sw.js', 'navigate', O)).toBe('bypass');
    expect(route('GET', O + '/random.json', 'cors', O)).toBe('bypass');
  });
});
