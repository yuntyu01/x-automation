import { describe, expect, it } from 'vitest';
import { safeNext } from './nav';

describe('safeNext', () => {
  it('keeps in-app paths with query', () => {
    expect(safeNext('/contents/123?q=1')).toBe('/contents/123?q=1');
  });
  it('rejects external and protocol-relative targets', () => {
    for (const bad of ['https://evil.example', '//evil.example', '/\\evil.example', 'javascript:alert(1)', null, '']) {
      expect(safeNext(bad)).toBe('/');
    }
  });
  it('does not loop back to login', () => {
    expect(safeNext('/login?next=/x')).toBe('/');
  });
});
