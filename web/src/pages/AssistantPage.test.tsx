// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import Assistant from './Assistant';
import { api } from '../lib/api';
vi.mock('../lib/api', () => ({ api: vi.fn(), download: vi.fn().mockResolvedValue(undefined) }));

let root: Root; let box: HTMLDivElement;
beforeEach(() => { (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true; Element.prototype.scrollIntoView = vi.fn(); box = document.createElement('div'); document.body.append(box); root = createRoot(box); });
afterEach(async () => { await act(async () => root.unmount()); box.remove(); vi.clearAllMocks(); });
const buttons = () => Array.from(box.querySelectorAll('button'));
const button = (s: string) => buttons().find(b => b.textContent === s);

function mockMe(me: Record<string, unknown>) {
  vi.mocked(api).mockImplementation((path: string) => {
    if (path === '/v1/me') return Promise.resolve(me) as any;
    if (path === '/v1/assistant/memory') return Promise.resolve({ enabled: false, notes: [] }) as any;
    return Promise.reject(new Error('unexpected ' + path));
  });
}

test('customer-scoped user gets a non-actionable explanation and no memory request is fired', async () => {
  mockMe({ role: 'viewer', customer_id: 'c1', customer_name: 'Acme' });
  await act(async () => root.render(<Assistant />));
  expect(box.textContent).toContain('not available for customer portal accounts');
  expect(box.textContent).not.toContain('Private memory');
  expect(buttons()).toHaveLength(0); // nothing actionable
  const paths = vi.mocked(api).mock.calls.map(c => c[0]);
  expect(paths).toEqual(['/v1/me']); // no automatic denied requests
});

test('viewer sees only the read-only template chips', async () => {
  mockMe({ role: 'viewer' });
  await act(async () => root.render(<Assistant />));
  expect(box.textContent).toContain('Private memory'); // memory section still offered
  expect(button('Set up an energy meter')).toBeTruthy();
  expect(button('Invite a user')).toBeFalsy();
  expect(button('Add a device')).toBeFalsy();
  expect(button('Acknowledge an alert')).toBeFalsy();
  expect(box.textContent).toContain('Some templates need a higher role');
});

test('a 403 on Confirm becomes a non-actionable explanation: no Confirm retry, nothing written twice', async () => {
  vi.mocked(api).mockImplementation((path: string, init?: any) => {
    if (path === '/v1/me') return Promise.resolve({ role: 'admin' }) as any;
    if (path === '/v1/assistant/memory') return Promise.resolve({ enabled: false, notes: [] }) as any;
    if (path === '/v1/users/invites') return Promise.reject(new Error('403: insufficient role'));
    return Promise.reject(new Error('unexpected ' + path));
  });
  await act(async () => root.render(<Assistant />));
  await act(async () => button('Invite a user')!.click());
  const input = box.querySelector('input[aria-label="Your answer"]') as HTMLInputElement;
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!;
    setter.call(input, 'viewer-attempt@example.invalid');
    input.dispatchEvent(new Event('input', { bubbles: true }));
  });
  await act(async () => button('Next')!.click());
  await act(async () => button('Viewer (read only)')!.click());
  expect(button('Confirm')).toBeTruthy();
  await act(async () => button('Confirm')!.click());
  expect(box.textContent).toContain('Your role does not allow this change. Nothing was written.');
  expect(button('Confirm')).toBeFalsy(); // retrying the same answers cannot help
  expect(button('Close')).toBeTruthy();
});

test('failed identity lookup shows retry, never privileged actions', async () => {
  let calls = 0;
  vi.mocked(api).mockImplementation((path: string) => {
    if (path === '/v1/me') { calls++; return calls === 1 ? Promise.reject(new Error('503: unavailable')) : Promise.resolve({ role: 'viewer' }) as any; }
    if (path === '/v1/assistant/memory') return Promise.resolve({ enabled: false, notes: [] }) as any;
    return Promise.reject(new Error('unexpected ' + path));
  });
  await act(async () => root.render(<Assistant />));
  expect(box.textContent).toContain('Could not check your account');
  expect(button('Invite a user')).toBeFalsy();
  expect(button('Set up an energy meter')).toBeFalsy();
  expect(vi.mocked(api).mock.calls.map(c => c[0])).toEqual(['/v1/me']); // nothing fired on a guess
  await act(async () => button('Try again')!.click());
  expect(button('Set up an energy meter')).toBeTruthy(); // recovered: viewer sees the read-only guide
});
