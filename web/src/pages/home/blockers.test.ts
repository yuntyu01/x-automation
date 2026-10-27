import { describe, expect, it } from 'vitest';
import { blockers } from './blockers';

describe('blockers', () => {
  it('explains every reason publishing is inactive', () => {
    const r = blockers({ publishingEnabled: false, mock: false, xConnected: false, automation: false, publishingPaused: true });
    expect(r).toHaveLength(4);
    expect(r[0]).toContain('PUBLISHING_ENABLED');
  });
  it('is empty when everything is ready', () => {
    expect(blockers({ publishingEnabled: true, mock: false, xConnected: true, automation: true, publishingPaused: false })).toEqual([]);
  });
});
