// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import ReportDrive from './ReportDrive';
import { api } from '../lib/api';
vi.mock('../lib/api', () => ({ api: vi.fn() }));
let root: Root; let box: HTMLDivElement;
beforeEach(() => { (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true; box = document.createElement('div'); document.body.append(box); root = createRoot(box); });
afterEach(async () => { await act(async () => root.unmount()); box.remove(); vi.clearAllMocks(); });

const exported = { id: 'e1', format: 'html', web_link: 'https://drive.example/d1', status: 'exported', error: '', created_at: '2026-10-21T10:00:00Z' };
function mockApi(post?: (url: string, init: any) => Promise<any>) {
  let lastExport: any = null; // after a successful POST the history returns the new link
  vi.mocked(api).mockImplementation(async (url: string, init?: any) => {
    if (url.endsWith('/exports') && !init) return { exports: [lastExport || exported] } as any;
    if (url.endsWith('/export/drive') && init?.method === 'POST') {
      const r = await (post ? post(url, init) : Promise.resolve({ web_link: 'https://drive.example/new' }));
      lastExport = { ...exported, web_link: r.web_link, format: JSON.parse(init.body).format };
      return r as any;
    }
    return Promise.reject(new Error('not mocked: ' + url)) as any;
  });
}

test('shows the latest successful export link on load', async () => {
  mockApi();
  await act(async () => root.render(<ReportDrive reportId="rep-1" />));
  const a = box.querySelector('a')!;
  expect(a.href).toBe('https://drive.example/d1');
  expect(a.textContent).toContain('Last export (html)');
});

test('html export posts the html format and refreshes the link', async () => {
  let body = '';
  mockApi((_u, init) => { body = init.body; return Promise.resolve({ web_link: 'https://drive.example/new' }); });
  await act(async () => root.render(<ReportDrive reportId="rep-1" />));
  await act(async () => { (Array.from(box.querySelectorAll('button')).find(b => b.textContent === 'Drive HTML') as HTMLButtonElement).click(); });
  expect(JSON.parse(body).format).toBe('html');
  expect(box.querySelector('a')!.href).toBe('https://drive.example/new');
});

test('sheet export posts the sheet format', async () => {
  let body = '';
  mockApi((_u, init) => { body = init.body; return Promise.resolve({ web_link: 'https://drive.example/new' }); });
  await act(async () => root.render(<ReportDrive reportId="rep-1" />));
  await act(async () => { (Array.from(box.querySelectorAll('button')).find(b => b.textContent === 'Drive Sheet') as HTMLButtonElement).click(); });
  expect(JSON.parse(body).format).toBe('sheet');
});

test('a refused export (no Workspace connection) is shown as an alert, not a link', async () => {
  mockApi(() => Promise.reject(new Error('connect Google Workspace with the Drive reports surface first (Settings > Integrations > Google)')));
  await act(async () => root.render(<ReportDrive reportId="rep-1" />));
  await act(async () => { (Array.from(box.querySelectorAll('button')).find(b => b.textContent === 'Drive HTML') as HTMLButtonElement).click(); });
  expect(box.querySelector('[role="alert"]')!.textContent).toContain('connect Google Workspace');
  expect(box.querySelector('a')!.href).toBe('https://drive.example/d1'); // old link unchanged
});
