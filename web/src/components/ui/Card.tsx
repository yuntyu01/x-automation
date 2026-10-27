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

export function Section({ title, description, action, children }: { title?: string; description?: string; action?: ReactNode; children: ReactNode }) {
  return (
    <section className={styles.section}>
      {(title || action) && (
        <div className={styles.sectionHead}>
          <div>
            {title && <h2 className={styles.sectionTitle}>{title}</h2>}
            {description && <p className={styles.sectionDesc}>{description}</p>}
          </div>
          {action}
        </div>
      )}
      {children}
    </section>
  );
}
