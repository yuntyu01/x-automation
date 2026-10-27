import type { ButtonHTMLAttributes, ReactNode } from 'react';
import { Spinner } from './Spinner';
import styles from './Button.module.css';

export type ButtonVariant = 'primary' | 'secondary' | 'ghost' | 'danger' | 'soft';

interface Props extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  size?: 'lg' | 'md' | 'sm';
  block?: boolean;
  busy?: boolean;
  icon?: ReactNode;
}

export function Button({ variant = 'primary', size = 'md', block, busy, icon, children, className, disabled, ...rest }: Props) {
  const cls = [styles.btn, styles[variant], styles[size], block ? styles.block : '', className ?? ''].join(' ');
  return (
    <button type="button" className={cls} disabled={disabled || busy} aria-busy={busy || undefined} {...rest}>
      {busy ? <Spinner size={18} /> : icon}
      {children && <span>{children}</span>}
    </button>
  );
}
