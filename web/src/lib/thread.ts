// 가중 글자 수 (공식 twitter-text config v3). 서버(internal/thread)와 같은 fixture로 대조한다.
import twitter from 'twitter-text';

export const STANDARD_LIMIT = 280;
export const LONG_LIMIT = 25000;
export const MANUAL_SEPARATOR = '\n---\n';

// 게시할 수 없는 문자 (twitter-text invalid_chars: U+FFFE, U+FEFF, U+FFFF)
const INVALID_CHARS = new RegExp('[' + String.fromCharCode(0xfffe, 0xfeff, 0xffff) + ']');

export interface LengthInfo {
  weighted: number;
  valid: boolean;
  limit: number;
}

export function measure(text: string, limit = STANDARD_LIMIT): LengthInfo {
  const p = twitter.parseTweet(text);
  // twitter-text의 valid는 280 기준이라 장문 한도는 직접 판정한다
  const weighted = p.weightedLength;
  const hasVisible = /\S/.test(text);
  const invalidChars = INVALID_CHARS.test(text);
  return { weighted, limit, valid: hasVisible && !invalidChars && weighted <= limit };
}

export function hasUrl(text: string): boolean {
  return twitter.extractUrls(text).length > 0;
}

// 직접 나누기 원문 ↔ 부분 목록
const manualRe = /^[ \t]*-{3,}[ \t]*$/m;
export function splitManual(raw: string): string[] {
  return raw.replace(/\r\n/g, '\n').split(new RegExp(manualRe.source, 'm')).map((p) => p.trim());
}

export function joinManual(parts: string[]): string {
  return parts.map((p) => p.trim()).join(MANUAL_SEPARATOR);
}
