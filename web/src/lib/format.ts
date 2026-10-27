// 표시용 포맷 도우미 (한국어)

const tz = 'Asia/Seoul';

export function formatDateTime(iso: string | null | undefined, opts: { timeZone?: string } = {}): string {
  if (!iso) return '';
  const d = new Date(iso);
  return new Intl.DateTimeFormat('ko-KR', {
    timeZone: opts.timeZone ?? tz,
    month: 'numeric',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  }).format(d);
}

export function formatTime(iso: string | null | undefined, timeZone = tz): string {
  if (!iso) return '';
  return new Intl.DateTimeFormat('ko-KR', { timeZone, hour: '2-digit', minute: '2-digit', hour12: false }).format(new Date(iso));
}

export function formatDate(iso: string | null | undefined, timeZone = tz): string {
  if (!iso) return '';
  return new Intl.DateTimeFormat('ko-KR', { timeZone, month: 'long', day: 'numeric', weekday: 'short' }).format(new Date(iso));
}

export function relativeTime(iso: string, now = Date.now()): string {
  const diff = (new Date(iso).getTime() - now) / 1000;
  const abs = Math.abs(diff);
  const rtf = new Intl.RelativeTimeFormat('ko-KR', { numeric: 'auto' });
  if (abs < 60) return rtf.format(Math.round(diff), 'second');
  if (abs < 3600) return rtf.format(Math.round(diff / 60), 'minute');
  if (abs < 86400) return rtf.format(Math.round(diff / 3600), 'hour');
  return rtf.format(Math.round(diff / 86400), 'day');
}

// 금액은 백만분의 1 단위 정수로 받는다
export function formatMoney(micros: number, currency = 'USD'): string {
  return new Intl.NumberFormat('ko-KR', { style: 'currency', currency, maximumFractionDigits: 4 }).format(micros / 1e6);
}
