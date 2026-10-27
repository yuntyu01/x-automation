import type { ReactNode } from 'react';
import { Card } from '../../components/ui/Card';
import styles from './SetupPage.module.css';

export function StepCard({ title, desc, children }: { title: string; desc?: string; children: ReactNode }) {
  return (
    <Card>
      <h2 className={styles.stepTitle}>{title}</h2>
      {desc && <p className={styles.stepDesc}>{desc}</p>}
      <div className={styles.stepBody}>{children}</div>
    </Card>
  );
}
