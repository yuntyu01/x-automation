import type { HTMLAttributes, ReactNode } from 'react';
import styles from './Card.module.css';

interface CardProps extends HTMLAttributes<HTMLElement> {
  as?: 'section' | 'div' | 'article' | 'li';
  padded?: boolean;
}

export function Card({ as: Tag = 'section', padded = true, className, ...rest }: CardProps) {
  return <Tag className={[styles.card, padded ? styles.padded : '', className ?? ''].join(' ')} {...rest} />;
}

export function CardTitle({ children, action, id }: { children: ReactNode; action?: ReactNode; id?: string }) {
  return (
    <div className={styles.titleRow}>
      <h2 className={styles.title} id={id}>
        {children}
      </h2>
      {action}
    </div>
  );
}

// card: 제목을 흰 카드 안에 넣는다 (토스 홈의 "최근 서비스"처럼). 내용이 목록·입력 칸 한 묶음일 때 쓴다
export function Section({ title, description, action, children, card }: { title?: string; description?: string; action?: ReactNode; children: ReactNode; card?: boolean }) {
  return (
    <section className={card ? [styles.card, styles.padded, styles.cardSection].join(' ') : styles.section}>
      {(title || action) && (
        <div className={styles.sectionHeader}>
          <div className={styles.sectionHead}>
            {title && <h2 className={styles.sectionTitle}>{title}</h2>}
            {action}
          </div>
          {description && <p className={styles.sectionDesc}>{description}</p>}
        </div>
      )}
      {children}
    </section>
  );
}
