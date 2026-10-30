import { Check, Copy, ExternalLink, KeyRound, ShieldAlert } from 'lucide-react';
import { useState, type ReactNode } from 'react';
import { Link } from 'react-router';
import { apiUrl } from '../api/client';
import { copyText } from '../lib/hooks';
import styles from './ApiKeyGuide.module.css';

// API 키 받는 방법: X 개발자 콘솔(console.x.com)과 Google AI Studio.
// 메뉴 이름은 2026-10 기준 공식 문서·안내를 따랐고, 콘솔 화면은 바뀔 수 있다.
export function ApiKeyGuide() {
  const origin = typeof window === 'undefined' ? '' : window.location.origin;
  // 프론트를 따로 호스팅하면(VITE_API_BASE_URL) 콜백은 API 서버 주소가 된다
  const callback = origin ? new URL(apiUrl('/api/x/oauth/callback'), origin).href : '/api/x/oauth/callback';

  return (
    <section className={styles.wrap} aria-labelledby="key-guide">
      <div className={styles.head}>
        <KeyRound size={20} aria-hidden />
        <div>
          <h2 id="key-guide">API 키 받는 방법</h2>
          <p>받은 키는 서버 환경변수에만 넣어요. 이 화면이나 채팅에는 붙여 넣지 마세요.</p>
        </div>
      </div>

      <article className={styles.card}>
        <header className={styles.cardHead}>
          <div>
            <h3>X API 키</h3>
            <p>X 개발자 콘솔 · 5분 정도</p>
          </div>
          <a className={styles.open} href="https://console.x.com" target="_blank" rel="noreferrer">
            콘솔 열기 <ExternalLink size={15} aria-hidden />
          </a>
        </header>
        <ol className={styles.steps}>
          <Step title="콘솔에 로그인">
            <a href="https://console.x.com" target="_blank" rel="noreferrer">
              console.x.com
            </a>
            에 게시할 X 계정으로 로그인해요.
          </Step>
          <Step title="크레딧 충전">
            Billing → Credits에서 카드를 등록하고 충전해요. 게시할 때마다 차감돼요(글 1건 $0.015, 링크가 있으면 $0.20 · 2026-10-27 확인).
          </Step>
          <Step title="앱 만들기">Apps에서 새 앱을 만들어요. 이름은 알아보기 쉬운 걸로 아무거나 괜찮아요.</Step>
          <Step title="사용자 인증 설정">
            앱의 User authentication settings에서 이렇게 고르고 저장해요.
            <dl className={styles.fields}>
              <Field label="앱 권한" value="Read and write" />
              <Field label="앱 종류" value="Web App (Confidential client)" />
              <Field label="Callback URI" value={callback} copy />
              <Field label="Website URL" value={origin} copy />
            </dl>
          </Step>
          <Step title="키 복사">
            Keys and tokens에서 <b>OAuth 2.0 Client ID</b>와 <b>Client Secret</b>을 복사해요. Secret은 한 번만 보여요. 잃어버리면 다시 만들어야 하고, 그러면 전에 쓰던 값은 못 써요.
          </Step>
          <Step title="서버에 넣기">
            KoDeploy 환경변수에 넣고 저장해요(저장하면 재시작돼요). <code>X_REDIRECT_URI</code>는 위 Callback URI와 글자 하나까지 같아야 해요.
            <EnvBlock lines={['X_CLIENT_ID=복사한 Client ID', 'X_CLIENT_SECRET=복사한 Client Secret', `X_REDIRECT_URI=${callback}`]} />
          </Step>
          <Step title="계정 연결">
            <Link to="/settings/connections">설정 → 연결</Link>에서 <b>X 계정 연결</b>을 누르고 X에서 허용해요.
          </Step>
        </ol>
      </article>

      <article className={styles.card}>
        <header className={styles.cardHead}>
          <div>
            <h3>Gemini API 키</h3>
            <p>Google AI Studio · 2분 정도</p>
          </div>
          <a className={styles.open} href="https://aistudio.google.com/apikey" target="_blank" rel="noreferrer">
            AI Studio 열기 <ExternalLink size={15} aria-hidden />
          </a>
        </header>
        <ol className={styles.steps}>
          <Step title="AI Studio에 로그인">
            <a href="https://aistudio.google.com/apikey" target="_blank" rel="noreferrer">
              aistudio.google.com
            </a>
            에 Google 계정으로 로그인해요.
          </Step>
          <Step title="키 만들기">
            왼쪽 메뉴 <b>Get API key</b> → <b>Create API key</b>를 눌러요. 처음이면 새 프로젝트로 만들고, 이미 있으면 그 프로젝트를 골라요.
          </Step>
          <Step title="키 복사">
            만들어진 키(<code>AIza</code>로 시작)를 복사해요. 비밀번호처럼 다뤄 주세요.
          </Step>
          <Step title="이미지도 쓰려면 결제 연결" optional>
            글 생성은 무료 한도로 돼요. 이미지는 무료 한도가 없어서, AI Studio의 Billing에서 그 프로젝트에 결제 계정을 연결해야 해요. 예산 알림도 함께 켜 두면 좋아요.
          </Step>
          <Step title="서버에 넣기">
            KoDeploy 환경변수에 넣고 저장해요.
            <EnvBlock lines={['AI_PROVIDER=gemini', 'AI_API_KEY=복사한 키', 'AI_TEXT_MODEL=gemini-3.5-flash-lite', 'AI_IMAGE_MODEL=gemini-3.1-flash-lite-image']} />
          </Step>
          <Step title="잘 되는지 확인">
            <Link to="/settings/connections">설정 → 연결</Link>에서 AI 생성이 <b>설정됨</b>인지 보고, 콘텐츠 → 새 글 → <b>AI로 생성</b>으로 1개 만들어 봐요.
          </Step>
        </ol>
      </article>

      <p className={styles.caution}>
        <ShieldAlert size={16} aria-hidden /> 키가 밖에 알려졌다면 콘솔에서 바로 지우고 새로 만든 뒤 환경변수를 바꿔요. 콘솔 메뉴 이름은 바뀔 수 있어요.
      </p>
    </section>
  );
}

function Step({ title, optional, children }: { title: string; optional?: boolean; children: ReactNode }) {
  return (
    <li className={styles.step}>
      <span className={styles.num} aria-hidden />
      <div className={styles.stepBody}>
        <p className={styles.stepTitle}>
          {title}
          {optional && <span className={styles.optional}>선택</span>}
        </p>
        <div className={styles.stepText}>{children}</div>
      </div>
    </li>
  );
}

function Field({ label, value, copy }: { label: string; value: string; copy?: boolean }) {
  return (
    <div className={styles.field}>
      <dt>{label}</dt>
      <dd>
        <code>{value}</code>
        {copy && <CopyButton text={value} label={`${label} 복사`} />}
      </dd>
    </div>
  );
}

function EnvBlock({ lines }: { lines: string[] }) {
  const text = lines.join('\n');
  return (
    <div className={styles.env}>
      <pre>{text}</pre>
      <CopyButton text={text} label="환경변수 복사" />
    </div>
  );
}

function CopyButton({ text, label }: { text: string; label: string }) {
  const [done, setDone] = useState(false);
  return (
    <button
      type="button"
      className={styles.copy}
      aria-label={label}
      onClick={async () => {
        if (await copyText(text)) {
          setDone(true);
          window.setTimeout(() => setDone(false), 1500);
        }
      }}
    >
      {done ? <Check size={16} aria-hidden /> : <Copy size={16} aria-hidden />}
      <span>{done ? '복사됨' : '복사'}</span>
    </button>
  );
}
