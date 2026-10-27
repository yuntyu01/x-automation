// 자체 앱 아이콘 (다른 서비스 로고를 쓰지 않는다)
export function AppMark({ size = 32 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 512 512" aria-hidden="true" focusable="false">
      <rect width="512" height="512" rx="112" fill="var(--primary)" />
      <path d="M150 168h212M150 256h150M150 344h96" stroke="#fff" strokeWidth="40" strokeLinecap="round" />
      <circle cx="362" cy="344" r="34" fill="#fff" />
    </svg>
  );
}
