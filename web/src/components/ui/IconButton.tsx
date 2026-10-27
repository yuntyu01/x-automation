import type { ButtonHTMLAttributes, ReactNode } from 'react';
import styles from './IconButton.module.css';

interface Props extends ButtonHTMLAttributes<HTMLButtonElement> {
  label: string; // 필수: 아이콘만 있는 버튼의 접근 가능한 이름
  children: ReactNode;
  badge?: number;
}

export function IconButton({ label, children, badge, className, ...rest }: Props) {
  return (
    <button type="button" aria-label={badge ? `${label} (읽지 않음 ${badge}개)` : label} className={[styles.btn, className ?? ''].join(' ')} {...rest}>
      {children}
      {badge ? <span className={styles.badge} aria-hidden>{badge > 99 ? '99+' : badge}</span> : null}
    </button>
  );
}
