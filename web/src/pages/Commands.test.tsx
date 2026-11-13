// @vitest-environment jsdom
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { expect, test, vi } from 'vitest';
import Commands from './Commands';
import { api } from '../lib/api';
vi.mock('../lib/api', () => ({ api: vi.fn() }));
vi.mock('../components/ControlTargets', () => ({ default: () => null }));
test('unknown command delivery is explicit and offers no retry or approval', async () => {
  (globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
  vi.mocked(api).mockResolvedValue([{ request_id: 'r', device_id: 'Fixture device', action: 'alarm.set', status: 'outcome_unknown', requested_by: 'user' }]);
  const box = document.createElement('div'); document.body.append(box); const root = createRoot(box);
  try {
    await act(async () => root.render(<Commands />));
    expect(box.textContent).toContain('Delivery unknown');
    expect(box.textContent).toContain('may or may not have happened');
    expect(box.textContent).toContain('No automatic retry');
    expect(box.querySelector('button')).toBeNull();
  } finally { await act(async () => root.unmount()); box.remove(); vi.clearAllMocks(); }
});
