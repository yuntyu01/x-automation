import { describe, expect, it } from 'vitest';
import { addDays, dayLabel, localDate, minutesToHM, postCostMicros } from './format';

describe('date helpers', () => {
  it('works in Asia/Seoul regardless of the device time zone', () => {
    expect(localDate(new Date('2026-10-27T15:30:00Z'))).toBe('2026-10-28'); // KST 00:30
    expect(addDays('2026-12-31', 1)).toBe('2027-01-01');
    expect(dayLabel('2026-10-27')).toBe('10월 27일 (화)');
    expect(minutesToHM(1440)).toBe('00:00');
    expect(minutesToHM(1020)).toBe('17:00');
  });
});

describe('postCostMicros', () => {
  const pricing = { verified_at: '2026-10-27', x_post_micros: 15_000, x_post_with_url_micros: 200_000 };
  const hasUrl = (t: string) => t.includes('http');
  it('부분마다 단가를 더하고 링크가 있는 부분은 링크 단가를 쓴다', () => {
    expect(postCostMicros(['본문', '답글 https://a.example', '답글2'], pricing, hasUrl)).toBe(230_000);
  });
  it('확인일이나 단가가 없으면 금액을 만들지 않는다', () => {
    expect(postCostMicros(['본문'], { ...pricing, verified_at: '' }, hasUrl)).toBeNull();
    expect(postCostMicros(['본문'], { ...pricing, x_post_micros: 0 }, hasUrl)).toBeNull();
  });
});
