import { useState, type FormEvent } from 'react';
import { Navigate, useNavigate, useSearchParams } from 'react-router';
import { ApiError, errorMessage } from '../api/client';
import { useAuth } from '../auth/AuthProvider';
import { safeNext } from '../lib/nav';
import { Button } from '../components/ui/Button';
import { TextField } from '../components/ui/TextField';
import { AppMark } from '../components/AppMark';
import styles from './LoginPage.module.css';

export function LoginPage() {
  const auth = useAuth();
  const nav = useNavigate();
  const [params] = useSearchParams();
  const next = safeNext(params.get('next'));
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [fields, setFields] = useState<Record<string, string>>({});

  if (auth.status === 'authenticated') return <Navigate to={next} replace />;

  async function submit(e: FormEvent) {
    e.preventDefault();
    setError('');
    const f: Record<string, string> = {};
    if (!username.trim()) f.username = '아이디를 입력하세요';
    if (!password) f.password = '비밀번호를 입력하세요';
    setFields(f);
    if (Object.keys(f).length) return;
    setBusy(true);
    try {
      await auth.login(username.trim(), password);
      nav(next, { replace: true });
    } catch (err) {
      if (err instanceof ApiError && Object.keys(err.fields).length) setFields(err.fields);
      else setError(errorMessage(err));
      setPassword('');
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className={styles.page}>
      <form className={styles.card} onSubmit={submit} noValidate aria-labelledby="login-title">
        <div className={styles.brand}>
          <AppMark size={48} />
          <h1 id="login-title" className={styles.title}>
            X 자동 게시
          </h1>
          <p className={styles.sub}>관리자 계정으로 로그인하세요</p>
        </div>
        <TextField
          label="아이디"
          name="username"
          autoComplete="username"
          autoCapitalize="none"
          spellCheck={false}
          value={username}
          onChange={(e) => setUsername(e.target.value)}
          error={fields.username}
        />
        <TextField
          label="비밀번호"
          name="password"
          type="password"
          autoComplete="current-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          error={fields.password}
        />
        {error && (
          <p className={styles.error} role="alert">
            {error}
          </p>
        )}
        <Button type="submit" size="lg" block busy={busy}>
          로그인
        </Button>
        <p className={styles.note}>회원가입과 비밀번호 찾기는 없어요. 비밀번호는 서버 설정에서 바꿉니다.</p>
      </form>
    </main>
  );
}
