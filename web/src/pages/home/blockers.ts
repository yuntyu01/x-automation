// 자동 게시가 왜 비활성인지 사람이 읽을 수 있게 모은다
export function blockers(opts: { publishingEnabled: boolean; mock: boolean; xConnected: boolean; automation: boolean; publishingPaused: boolean }) {
  const out: string[] = [];
  if (opts.mock) out.push('목업 모드라 실제로 게시하지 않아요');
  else if (!opts.publishingEnabled) out.push('서버의 실게시 허용(PUBLISHING_ENABLED)이 꺼져 있어요');
  if (!opts.xConnected) out.push('X 계정이 연결되지 않았어요');
  if (!opts.automation) out.push('자동 운영을 아직 시작하지 않았어요');
  if (opts.publishingPaused) out.push('게시가 일시정지되어 있어요');
  return out;
}
