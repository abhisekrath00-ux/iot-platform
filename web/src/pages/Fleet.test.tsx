// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, Root } from 'react-dom/client';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import Fleet from './Fleet';
import { api } from '../lib/api';
vi.mock('../lib/api', () => ({ api: vi.fn(), download: vi.fn().mockResolvedValue(undefined) }));
let root: Root; let box: HTMLDivElement;
beforeEach(() => { (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true; box = document.createElement('div'); document.body.append(box); root = createRoot(box); });
afterEach(async () => { await act(async () => root.unmount()); box.remove(); vi.clearAllMocks(); });

const device = { id: 'd1', name: 'Boiler', profile: 'modbus-tcp', config: {} };
function mockApi(me: object) {
  vi.mocked(api).mockImplementation((url: string) => {
    if (url === '/v1/me') return Promise.resolve(me) as any;
    if (url === '/v1/devices') return Promise.resolve([device]) as any;
    if (url === '/v1/alerts') return Promise.resolve([]) as any;
    if (url.startsWith('/v1/telemetry/latest')) return Promise.resolve([]) as any;
    if (url === '/v1/fleet' || url === '/v1/assets') return Promise.reject(new Error('403: not available to customer-scoped users')) as any;
    return Promise.resolve([]) as any;
  });
}
const calls = () => vi.mocked(api).mock.calls.map(c => String(c[0]));

test('scoped user: no fleet/assets/assistant calls, no write CTAs, no fake-healthy pills', async () => {
  mockApi({ customer_id: 'c1' });
  await act(async () => root.render(<MemoryRouter><Fleet /></MemoryRouter>));
  await act(async () => {});
  expect(calls()).not.toContain('/v1/fleet');
  expect(calls()).not.toContain('/v1/assets');
  expect(box.textContent).not.toContain('Add a device');
  expect(box.textContent).not.toContain('Claim a gateway');
  expect(box.querySelector('input[placeholder*="critical alerts"]')).toBeNull();
  // metrics show as not available instead of green pills beside '-' values
  expect(box.textContent).toContain('not available');
  expect(box.textContent).not.toContain('403');
  // allowed data still renders
  expect(box.textContent).toContain('Boiler');
  expect(box.textContent).toContain('only this customer');
});

test('scoped empty state points at the admin, not onboarding', async () => {
  vi.mocked(api).mockImplementation((url: string) => {
    if (url === '/v1/me') return Promise.resolve({ customer_id: 'c1' }) as any;
    if (url === '/v1/devices' || url === '/v1/alerts') return Promise.resolve([]) as any;
    return Promise.resolve([]) as any;
  });
  await act(async () => root.render(<MemoryRouter><Fleet /></MemoryRouter>));
  await act(async () => {});
  expect(box.textContent).toContain('No devices in this customer yet');
  expect(box.textContent).toContain('workspace admin');
});

test('unscoped user keeps fleet totals and the ask bar', async () => {
  vi.mocked(api).mockImplementation((url: string) => {
    if (url === '/v1/me') return Promise.resolve({}) as any;
    if (url === '/v1/fleet') return Promise.resolve({ gateways: 2, active_gateways: 1, devices: 7, stale_devices: 0, open_alerts: 3 }) as any;
    if (url === '/v1/assets') return Promise.resolve([]) as any;
    if (url === '/v1/devices') return Promise.resolve([device]) as any;
    if (url === '/v1/alerts') return Promise.resolve([]) as any;
    if (url.startsWith('/v1/telemetry/latest')) return Promise.resolve([]) as any;
    return Promise.resolve([]) as any;
  });
  await act(async () => root.render(<MemoryRouter><Fleet /></MemoryRouter>));
  await act(async () => {});
  expect(calls()).toContain('/v1/fleet');
  expect(box.textContent).toContain('1/2');
  expect(box.textContent).toContain('action');
});

test('failed fleet load is visibly unavailable, not a green badge beside a dash', async () => {
  vi.mocked(api).mockImplementation((url: string) => {
    if (url === '/v1/me') return Promise.resolve({}) as any;
    if (url === '/v1/fleet') return Promise.reject(new Error('503: fleet unavailable')) as any;
    if (url === '/v1/assets') return Promise.resolve([]) as any;
    if (url === '/v1/devices') return Promise.resolve([]) as any;
    if (url === '/v1/alerts') return Promise.resolve([]) as any;
    return Promise.resolve([]) as any;
  });
  await act(async () => root.render(<MemoryRouter><Fleet /></MemoryRouter>));
  await act(async () => {});
  expect(box.textContent).toContain('Fleet totals could not be loaded');
  expect(box.textContent).toContain('not available');
  // no healthy-looking badge may survive a failed load
  expect(box.querySelector('.pill.ok')).toBeNull();
});
