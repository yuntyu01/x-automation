import { CircleAlert, CircleCheck, CircleDashed, Clock, Info, LoaderCircle, TriangleAlert } from 'lucide-react';
import type { ReactNode } from 'react';
import styles from './Badge.module.css';

export type Tone = 'neutral' | 'info' | 'success' | 'warning' | 'danger' | 'progress' | 'scheduled';

const ICONS: Record<Tone, ReactNode> = {
  neutral: <CircleDashed size={14} aria-hidden />,
  info: <Info size={14} aria-hidden />,
  success: <CircleCheck size={14} aria-hidden />,
  warning: <TriangleAlert size={14} aria-hidden />,
  danger: <CircleAlert size={14} aria-hidden />,
  progress: <LoaderCircle size={14} aria-hidden className={styles.spin} />,
  scheduled: <Clock size={14} aria-hidden />,
};

// 상태는 색만으로 구분하지 않는다: 아이콘 + 이름을 함께 쓴다
export function Badge({ tone = 'neutral', children, icon = true }: { tone?: Tone; children: ReactNode; icon?: boolean }) {
  return (
    <span className={[styles.badge, styles[tone]].join(' ')}>
      {icon && ICONS[tone]}
      {children}
    </span>
  );
}
