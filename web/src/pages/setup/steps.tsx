import { Link } from 'react-router';
import { StepCard } from './StepCard';
import styles from './SetupPage.module.css';

// 프롬프트 단계: 기존 프롬프트를 붙여넣어 등록한다
export function SetupPromptStep() {
  return (
    <StepCard title="쓰던 프롬프트를 등록해요" desc="소설·썰·명언 등 기존 프롬프트를 그대로 붙여넣으면 돼요. 직접 쓴 글만 올릴 거라면 건너뛰어도 돼요.">
      <Link to="/prompts" className={styles.link}>
        프롬프트 관리로 이동
      </Link>
    </StepCard>
  );
}

// 시간표 단계: 새벽·낮 허용 구간과 하루 묶음 수
export function SetupScheduleStep() {
  return (
    <StepCard title="언제 올릴지 정해요" desc="새벽과 낮처럼 여러 구간을 정할 수 있어요. 아침·저녁은 직접 운영하는 시간으로 비워 둘 수 있어요.">
      <Link to="/schedule/rules" className={styles.link}>
        시간표 설정으로 이동
      </Link>
    </StepCard>
  );
}
