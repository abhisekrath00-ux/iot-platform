// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import GoogleWorkspace from './GoogleWorkspace';
import { api } from '../lib/api';
vi.mock('../lib/api', () => ({ api: vi.fn(), download: vi.fn().mockResolvedValue(undefined) }));
let root: Root; let box: HTMLDivElement;
beforeEach(() => { (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true; box = document.createElement('div'); document.body.append(box); root = createRoot(box); window.history.replaceState({}, '', '/settings'); });
afterEach(async () => { await act(async () => root.unmount()); box.remove(); vi.clearAllMocks(); vi.restoreAllMocks(); });

test('not-configured deployment shows the operator note and no connect button', async () => {
  vi.mocked(api).mockImplementation((url: string) => {
    if (url === '/v1/integrations/google/status') return Promise.resolve({ configured: false, connected: false }) as any;
    return Promise.reject(new Error('not mocked')) as any;
  });
  await act(async () => root.render(<GoogleWorkspace />));
  expect(box.querySelector('[data-testid="google-not-configured"]')).toBeTruthy();
  expect(box.textContent).toContain('GOOGLE_CLIENT_ID');
  expect(box.textContent).not.toContain('Connect Google account');
});

test('configured and unconnected: connect posts the gmail surface and opens the consent URL', async () => {
  const opened: string[] = [];
  vi.spyOn(window, 'open').mockImplementation(((u: string) => { opened.push(u); return null; }) as any);
  vi.mocked(api).mockImplementation((url: string, init?: any) => {
    if (url === '/v1/integrations/google/status') return Promise.resolve({ configured: true, connected: false }) as any;
    if (url === '/v1/integrations/google/connect' && init?.method === 'POST') return Promise.resolve({ url: 'https://accounts.google.com/o/oauth2/auth?state=x' }) as any;
    return Promise.reject(new Error('not mocked: ' + url)) as any;
  });
  await act(async () => root.render(<GoogleWorkspace />));
  const btn = [...box.querySelectorAll('button')].find(b => b.textContent === 'Connect Google account')!;
  await act(async () => btn.click());
  expect(opened[0]).toContain('accounts.google.com');
  const call = vi.mocked(api).mock.calls.find(c => c[0] === '/v1/integrations/google/connect')!;
  expect(JSON.parse((call[1] as any).body).surfaces).toEqual(['gmail_send', 'calendar_oncall']);
  expect(box.textContent).toContain('Finish the sign-in');
});

test('connected state offers disconnect, which calls DELETE after confirm', async () => {
  vi.spyOn(window, 'confirm').mockReturnValue(true);
  const calls: string[] = [];
  vi.mocked(api).mockImplementation((url: string, init?: any) => {
    calls.push(url + ' ' + (init?.method || 'GET'));
    if (url === '/v1/integrations/google/status') return Promise.resolve({ configured: true, connected: true }) as any;
    if (url === '/v1/integrations/google' && init?.method === 'DELETE') return Promise.resolve({ connected: false }) as any;
    return Promise.reject(new Error('not mocked')) as any;
  });
  await act(async () => root.render(<GoogleWorkspace />));
  expect(box.querySelector('[data-testid="google-connected"]')).toBeTruthy();
  const btn = [...box.querySelectorAll('button')].find(b => b.textContent === 'Disconnect')!;
  await act(async () => btn.click());
  expect(calls).toContain('/v1/integrations/google DELETE');
});

test('non-admin (403 on status) sees no card', async () => {
  vi.mocked(api).mockImplementation(() => Promise.reject(new Error('403 forbidden')) as any);
  await act(async () => root.render(<GoogleWorkspace />));
  expect(box.querySelector('[data-testid="google-workspace-card"]')).toBeNull();
});

test('returning from the Google consent shows the success note and cleans the URL', async () => {
  window.history.replaceState({}, '', '/settings?google=connected');
  vi.mocked(api).mockImplementation((url: string) => {
    if (url === '/v1/integrations/google/status') return Promise.resolve({ configured: true, connected: true }) as any;
    return Promise.reject(new Error('not mocked')) as any;
  });
  await act(async () => root.render(<GoogleWorkspace />));
  expect(box.textContent).toContain('Google account connected');
  expect(window.location.search).toBe('');
});
