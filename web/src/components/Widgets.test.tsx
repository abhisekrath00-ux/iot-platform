// @vitest-environment jsdom
import { act } from 'react';
import { createRoot, Root } from 'react-dom/client';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import { AlarmsWidget } from './Widgets';
import { api } from '../lib/api';
vi.mock('../lib/api', () => ({ api: vi.fn().mockResolvedValue([]), download: vi.fn() }));
let root: Root; let box: HTMLDivElement;
beforeEach(() => { (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true; box = document.createElement('div'); document.body.append(box); root = createRoot(box); });
afterEach(async () => { await act(async () => root.unmount()); box.remove(); vi.clearAllMocks(); });

test('AlarmsWidget scopes its open-alerts query by group', async () => {
  await act(async () => root.render(<AlarmsWidget w={{ id: 'w1', type: 'alarms', title: 'Open alerts' } as any} groupId="g-1" />));
  const url = vi.mocked(api).mock.calls[0][0] as string;
  expect(url).toContain('status=open');
  expect(url).toContain('group_id=g-1');
  expect(url).not.toContain('asset_id');
});

test('AlarmsWidget combines asset, relation and group scopes', async () => {
  await act(async () => root.render(<AlarmsWidget w={{ id: 'w1', type: 'alarms', title: 'Open alerts' } as any} assetId="a-1" relation="feeds" groupId="g-1" />));
  const url = vi.mocked(api).mock.calls[0][0] as string;
  expect(url).toContain('asset_id=a-1');
  expect(url).toContain('relation=feeds');
  expect(url).toContain('group_id=g-1');
});
