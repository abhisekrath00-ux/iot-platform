import { describe, expect, it } from 'vitest';
import { GUIDES, searchGuides } from './guides';

const ROUTES = ['/', '/devices', '/assets', '/customers', '/users', '/kpis', '/map', '/explorer', '/onboarding', '/edge-setup', '/fleet-updates', '/scan', '/dashboards', '/assistant', '/flows', '/alerts', '/commands', '/reports', '/profiles', '/audit', '/system', '/dev-tools', '/settings', '/resources'];

describe('guides', () => {
  it('have unique ids and real content', () => {
    expect(new Set(GUIDES.map((g) => g.id)).size).toBe(GUIDES.length);
    for (const g of GUIDES) { expect(g.title.length).toBeGreaterThan(5); expect(g.body.length).toBeGreaterThan(0); }
  });
  it('only link to pages that exist', () => {
    for (const g of GUIDES) for (const b of g.body) if ('link' in b) expect(ROUTES).toContain(b.link[1]);
  });
  it('are searchable', () => {
    expect(searchGuides('modbus').map((g) => g.id)).toContain('modbus-maps');
    expect(searchGuides('localhost')[0].id).toBe('server-address');
    expect(searchGuides('zzzz-nothing')).toEqual([]);
    expect(searchGuides('')).toHaveLength(GUIDES.length);
  });
  it('do not promise untested things as done', () => {
    const all = JSON.stringify(GUIDES).toLowerCase();
    expect(all).not.toContain('production-ready');
    expect(all).toContain('never end to end');
  });
});
