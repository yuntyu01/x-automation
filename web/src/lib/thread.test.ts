import { describe, expect, it } from 'vitest';
import { joinManual, measure, splitManual } from './thread';

describe('measure', () => {
  it('counts hangul as 2 and urls as 23', () => {
    expect(measure('가나다').weighted).toBe(6);
    expect(measure('Hi http://test.co').weighted).toBe(26);
    expect(measure('가'.repeat(140)).valid).toBe(true);
    expect(measure('가'.repeat(141)).valid).toBe(false);
  });
  it('counts emoji sequences as 2', () => {
    expect(measure('👨‍👩‍👧‍👦').weighted).toBe(2);
    expect(measure('🇰🇷').weighted).toBe(2);
  });
  it('rejects whitespace-only and invalid chars', () => {
    expect(measure('   ').valid).toBe(false);
    expect(measure('A' + String.fromCharCode(0xffff)).valid).toBe(false);
  });
});

describe('manual parts', () => {
  it('round-trips', () => {
    const parts = ['본문', '답글 하나', '답글 둘'];
    expect(splitManual(joinManual(parts))).toEqual(parts);
    expect(splitManual('a\n  ---  \nb')).toEqual(['a', 'b']);
  });
});
