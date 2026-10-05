import { describe, expect, it } from 'vitest';
import { safeDownload } from './ChatPanel';

describe('safeDownload', () => {
  it('accepts only server-built report downloads', () => {
    expect(safeDownload('/v1/reports/r-1/download?format=csv')).toBe(true);
    expect(safeDownload('/v1/reports/r-1/download?format=xlsx')).toBe(true);
  });
  it('refuses anything else', () => {
    for (const p of ['https://evil.example/x', '/v1/users', '/v1/reports/../users/download?format=csv', '/v1/reports/r-1/download?format=exe',
      '/v1/reports/r-1/download?format=csv&x=1', '//evil.example/v1/reports/r/download?format=csv', '/v1/reports/a/b/download?format=csv']) {
      expect(safeDownload(p), p).toBe(false);
    }
  });
});
