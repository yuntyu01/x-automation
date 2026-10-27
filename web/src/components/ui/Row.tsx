import { ChevronRight } from 'lucide-react';
import type { ReactNode } from 'react';
import { Link } from 'react-router';
import styles from './Row.module.css';

interface RowProps {
  label: ReactNode;
  description?: ReactNode;
  value?: ReactNode;
  icon?: ReactNode;
  iconTone?: 'grey' | 'blue' | 'red' | 'orange' | 'green';
  to?: string;
  onClick?: () => void;
  trailing?: ReactNode; // 스위치 등
  tone?: 'default' | 'danger';
}

// 목록 행: 왼쪽 아이콘 상자와 항목명, 오른쪽 현재 값·스위치·이동 표시
export function Row({ label, description, value, icon, iconTone = 'grey', to, onClick, trailing, tone = 'default' }: RowProps) {
  const inner = (
    <>
      {icon && <span className={[styles.icon, iconTone !== 'grey' ? styles[`t-${iconTone}`] : ''].join(' ')}>{icon}</span>}
      <span className={styles.text}>
        <span className={[styles.label, tone === 'danger' ? styles.danger : ''].join(' ')}>{label}</span>
        {description && <span className={styles.desc}>{description}</span>}
      </span>
      {value !== undefined && <span className={styles.value}>{value}</span>}
      {trailing}
      {(to || onClick) && !trailing && <ChevronRight size={22} className={styles.chev} aria-hidden />}
    </>
  );
  if (to)
    return (
      <Link to={to} className={[styles.row, styles.interactive].join(' ')}>
        {inner}
      </Link>
    );
  if (onClick)
    return (
      <button type="button" onClick={onClick} className={[styles.row, styles.interactive].join(' ')}>
        {inner}
      </button>
    );
  return <div className={styles.row}>{inner}</div>;
}

export function RowGroup({ children }: { children: ReactNode }) {
  return <div className={styles.group}>{children}</div>;
}
