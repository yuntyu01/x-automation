import type { ReactNode } from 'react';
import styles from './BottomCTA.module.css';

// 화면 아래에 고정되는 주 행동 버튼 영역. secondary를 주면 왼쪽에 좁은 보조 버튼(이전 등)을 둔다
export function BottomCTA({ children, secondary }: { children: ReactNode; secondary?: ReactNode }) {
  return (
    <>
      <div className={styles.spacer} aria-hidden />
      <div className={styles.bar}>
        <div className={styles.inner}>
          {secondary && <div className={styles.narrow}>{secondary}</div>}
          {children}
        </div>
      </div>
    </>
  );
}
