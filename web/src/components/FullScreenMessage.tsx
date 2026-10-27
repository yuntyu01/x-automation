import { Spinner } from './ui/Spinner';
import { Button } from './ui/Button';
import styles from './FullScreenMessage.module.css';

interface Props {
  title: string;
  body?: string;
  busy?: boolean;
  action?: { label: string; onClick: () => void };
}

export function FullScreenMessage({ title, body, busy, action }: Props) {
  return (
    <div className={styles.wrap} role={busy ? 'status' : 'alert'} aria-live="polite">
      {busy && <Spinner size={28} />}
      <h1 className={styles.title}>{title}</h1>
      {body && <p className={styles.body}>{body}</p>}
      {action && (
        <Button variant="secondary" onClick={action.onClick}>
          {action.label}
        </Button>
      )}
    </div>
  );
}
