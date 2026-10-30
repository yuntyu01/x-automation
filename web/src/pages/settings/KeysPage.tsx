import { ChevronDown, LifeBuoy } from 'lucide-react';
import { PageHeader } from '../../components/layout/AppShell';
import { Section } from '../../components/ui/Card';
import { ApiKeyGuide } from '../ApiKeyGuide';
import { BackButton } from './ConnectionsPage';
import { ISSUE_GROUPS } from './connectIssues';
import styles from './KeysPage.module.css';

// API 키 연결: 키 받는 방법(X 개발자 콘솔·Google AI Studio)과 연결이 안 될 때 확인할 것.
// 키 값 자체는 서버 환경변수에만 넣고 이 화면에는 보이지 않는다.
export function KeysPage() {
  const jump = (id: string) => document.getElementById(id)?.scrollIntoView({ behavior: 'smooth', block: 'start' });
  return (
    <>
      <PageHeader title="API 키 연결" subtitle="키 받는 방법과 연결이 안 될 때 확인할 것" back={<BackButton to="/settings" />} />
      <div className={styles.stack}>
        <div className={styles.jump} role="navigation" aria-label="이 화면 바로 가기">
          <button type="button" onClick={() => jump('key-guide')}>
            키 받는 방법
          </button>
          <button type="button" onClick={() => jump('trouble')}>
            연결이 안 될 때
          </button>
        </div>

        <ApiKeyGuide />

        <section className={styles.trouble} aria-labelledby="trouble">
          <div className={styles.head}>
            <LifeBuoy size={20} aria-hidden />
            <div>
              <h2 id="trouble">연결이 안 될 때</h2>
              <p>화면에 보인 문구로 찾아요. 누르면 원인과 해결 방법이 나와요.</p>
            </div>
          </div>
          {ISSUE_GROUPS.map((g) => (
            <Section card key={g.id} title={g.title} description={g.description}>
              <ul className={styles.issues}>
                {g.issues.map((issue) => (
                  <li key={issue.see}>
                    <details className={styles.issue}>
                      <summary>
                        <span>{issue.see}</span>
                        <ChevronDown className={styles.chevron} size={18} aria-hidden />
                      </summary>
                      <div className={styles.body}>
                        <p className={styles.label}>왜 생기나요</p>
                        <ul className={styles.why}>
                          {issue.why.map((w, i) => (
                            <li key={i}>{w}</li>
                          ))}
                        </ul>
                        <p className={styles.label}>이렇게 해 보세요</p>
                        <p className={styles.fix}>{issue.fix}</p>
                      </div>
                    </details>
                  </li>
                ))}
              </ul>
            </Section>
          ))}
        </section>
      </div>
    </>
  );
}
