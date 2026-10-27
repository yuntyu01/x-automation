import { describe, expect, it } from 'vitest';
import { addDays, dayLabel, localDate, minutesToHM } from './format';

describe('date helpers', () => {
  it('works in Asia/Seoul regardless of the device time zone', () => {
    expect(localDate(new Date('2026-10-27T15:30:00Z'))).toBe('2026-10-28'); // KST 00:30
    expect(addDays('2026-12-31', 1)).toBe('2027-01-01');
    expect(dayLabel('2026-10-27')).toBe('10월 27일 (화)');
    expect(minutesToHM(1440)).toBe('00:00');
    expect(minutesToHM(1020)).toBe('17:00');
  });
});
