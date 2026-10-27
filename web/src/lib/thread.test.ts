import { describe, expect, it } from 'vitest';
import { joinManual, measure, splitManual } from './thread';
import goldenRaw from '../../../testdata/thread/golden.json?raw';
import conformanceRaw from '../../../testdata/twitter-text/conformance.json?raw';
import twitter from 'twitter-text';

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

// 서버(Go internal/thread)와 같은 기대값을 공식 라이브러리가 그대로 내는지 확인한다.
// 라이브러리 버전이 바뀌었는데 golden.json을 다시 만들지 않았으면 여기서 실패한다 (npm run gen:thread).

interface Golden {
  twitter_text: string;
  cases: { text: string; v3: [number, boolean, number, number]; v2: [number, boolean, number, number]; urls: [number, number, number][] }[];
}

describe('shared fixtures', () => {
  const golden = JSON.parse(goldenRaw) as Golden;
  const conformance = JSON.parse(conformanceRaw) as { weighted_v3: { text: string; expected: { weightedLength: number; valid: boolean } }[] };

  it('official library matches golden.json used by the Go server', () => {
    expect(golden.cases.length).toBeGreaterThan(100);
    for (const c of golden.cases) {
      const p = twitter.parseTweet(c.text);
      expect([p.weightedLength, p.valid, p.permillage, p.validRangeEnd], c.text.slice(0, 40)).toEqual(c.v3);
      const urls = twitter.extractUrlsWithIndices(c.text).map((u) => [u.indices[0], u.indices[1], u.url.length]);
      expect(urls, c.text.slice(0, 40)).toEqual(c.urls);
    }
  });

  it('app measure agrees with the official v3 conformance cases', () => {
    for (const c of conformance.weighted_v3) {
      const m = measure(c.text);
      expect(m.weighted, c.text.slice(0, 40)).toBe(c.expected.weightedLength);
      if (/\S/.test(c.text)) expect(m.valid, c.text.slice(0, 40)).toBe(c.expected.valid);
    }
  });
});
