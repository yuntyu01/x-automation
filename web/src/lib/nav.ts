// 로그인 후 돌아갈 경로. 외부 주소로 튕기지 않게 앱 내부 경로만 허용한다
export function safeNext(raw: string | null): string {
  if (!raw || !raw.startsWith('/') || raw.startsWith('//') || raw.startsWith('/\\') || raw.startsWith('/login')) return '/';
  return raw;
}
