import { useState } from 'react';
import { ApiError, errorMessage } from '../../api/client';
import { useIntegrations, useSaveSettings, useSettings } from '../../api/queries';
import { SPLIT_LABEL, type Policy, type Settings, type SplitMode } from '../../api/types';
import { PageHeader } from '../../components/layout/AppShell';
import { Button } from '../../components/ui/Button';
import { Card, Section } from '../../components/ui/Card';
import { ChoiceList } from '../../components/ui/ChoiceList';
import { Row, RowGroup } from '../../components/ui/Row';
import { Sheet } from '../../components/ui/Sheet';
import { ErrorState, LoadingList } from '../../components/ui/States';
import { Switch } from '../../components/ui/Switch';
import { useToast } from '../../components/ui/Toast';
import { LONG_LIMIT, STANDARD_LIMIT } from '../../lib/thread';
import { BackButton } from './ConnectionsPage';
import styles from './GenerationSettingsPage.module.css';

export function ThreadSettingsPage() {
  const settings = useSettings();
  return (
    <>
      <PageHeader title="글 나누기" subtitle="새 글의 기본 나누기 방식과 한 게시물 한도" back={<BackButton to="/settings" />} />
      {settings.isLoading && <LoadingList rows={3} />}
      {settings.isError && <ErrorState error={settings.error} onRetry={() => settings.refetch()} />}
      {settings.data && <ThreadForm key={settings.data.version} settings={settings.data} />}
    </>
  );
}

type Thread = Policy['thread'];

const SPLIT_DESC: Record<SplitMode, string> = {
  two_parts: '절반 부근의 문단·문장 경계에서 본문과 답글 하나로 나눠요. 한쪽이 한도를 넘으면 그쪽만 답글을 더 이어요',
  overflow: '본문을 한도까지 채우고 넘치는 부분만 답글로 이어요',
  manual: '원문에 --- 만 있는 줄을 넣은 곳에서 나눠요',
};

function ThreadForm({ settings }: { settings: Settings }) {
  const save = useSaveSettings();
  const toast = useToast();
  const [t, setT] = useState<Thread>(settings.policy.thread);
  const [fields, setFields] = useState<Record<string, string>>({});
  const [confirmLong, setConfirmLong] = useState(false);
  const integrations = useIntegrations();
  const conn = integrations.data?.x.connection;
  // 연결된 계정 등급이 None이면 장문 게시가 확실히 안 된다 (X API 문서 기준). 연결 전이면 판단을 미룬다
  const longBlocked = conn?.status === 'connected' && conn.capabilities.long_post_possible === false;
  const dirty = JSON.stringify(t) !== JSON.stringify(settings.policy.thread);
  const set = <K extends keyof Thread>(k: K, v: Thread[K]) => setT((p) => ({ ...p, [k]: v }));

  async function submit() {
    setFields({});
    try {
      await save.mutateAsync({ ...settings, policy: { ...settings.policy, thread: t } });
      toast.show(
        t.long_post_enabled !== settings.policy.thread.long_post_enabled ? '저장했어요. 아직 승인되지 않은 글을 새 한도로 다시 나눴어요' : '저장했어요',
        'success',
      );
    } catch (e) {
      if (e instanceof ApiError && Object.keys(e.fields).length) setFields(e.fields);
      else toast.show(errorMessage(e), 'error');
    }
  }

  return (
    <div className={styles.stack}>
      <Section title="기본 나누기 방식" description="새로 만드는 글에 쓰여요. 글마다 편집 화면에서 바꿀 수 있어요">
        <ChoiceList
          name="기본 나누기 방식"
          value={t.split_mode}
          onChange={(v) => set('split_mode', v)}
          options={(['two_parts', 'overflow', 'manual'] as SplitMode[]).map((m) => ({ value: m, label: SPLIT_LABEL[m], description: SPLIT_DESC[m] }))}
        />
      </Section>

      <Section title="답글">
        <RowGroup>
          <Row
            label="번호 표기 (1/N)"
            description="게시물 끝에 (1/3)처럼 붙여요. 번호도 글자 수에 들어가요"
            trailing={<Switch label="번호 표기" checked={t.numbering} onChange={(v) => set('numbering', v)} />}
          />
          <Row
            label="최대 답글 수"
            description="0이면 제한 없어요. 넘는 글은 게시하지 않고 '수정 필요'로 둬요"
            trailing={
              <input
                className={styles.num}
                type="number"
                inputMode="numeric"
                min={0}
                max={25}
                aria-label="최대 답글 수"
                aria-invalid={fields['thread.max_replies'] ? true : undefined}
                value={String(t.max_replies)}
                onChange={(e) => set('max_replies', e.target.value === '' ? 0 : Number(e.target.value))}
              />
            }
          />
        </RowGroup>
        {fields['thread.max_replies'] && (
          <p className={styles.warn} role="alert">
            {fields['thread.max_replies']}
          </p>
        )}
      </Section>

      <Section title="한 게시물 한도">
        <RowGroup>
          <Row
            label="장문 게시"
            description={
              t.long_post_enabled
                ? `켜짐: 한 게시물 ${LONG_LIMIT.toLocaleString()}자까지 써요`
                : `꺼짐: X 일반 한도 ${STANDARD_LIMIT}자(가중)로 나눠요`
            }
            trailing={
              <Switch label="장문 게시" checked={t.long_post_enabled} disabled={longBlocked && !t.long_post_enabled} onChange={(v) => (v ? setConfirmLong(true) : set('long_post_enabled', false))} />
            }
          />
        </RowGroup>
        {longBlocked && (
          <p className={styles.warn} role="note">
            연결된 X 계정(@{conn?.username})의 구독 등급이 {String(conn?.capabilities.subscription_type || 'None')}이라 장문 게시를 쓸 수 없어요.
            {t.long_post_enabled && ' 지금 켜져 있으면 게시가 거부될 수 있으니 끄는 것을 권해요.'}
          </p>
        )}
        <p className={styles.muted}>
          장문 게시는 X 계정 등급과 API 권한이 모두 허용할 때만 돼요. 웹에서 길게 써진다고 API로도 된다는 보장은 없어요. 확인하기 전에는 꺼 두는 게 안전해요.
        </p>
      </Section>

      <Section title="글자 수는 이렇게 세요">
        <Card>
          <ul className={styles.rules}>
            <li>X와 같은 방식(공식 twitter-text)으로 세요. 한글·한자·일본어는 2자, 영문·숫자·기본 문장부호는 1자예요</li>
            <li>링크는 길이와 관계없이 23자예요. 링크가 있으면 X 게시 단가가 달라질 수 있어요</li>
            <li>이모지는 하나에 2자예요. 아주 최근 이모지 조합은 더 길게 세어질 수 있어요 (한도를 넘지 않는 쪽으로 계산)</li>
            <li>원문은 그대로 보관하고, 게시할 때만 줄 끝 공백·3줄 이상 빈 줄·앞뒤 공백을 정리해요</li>
            <li>문단 → 문장 → 줄 → 공백 순으로 자르고, 링크·이모지·글자 중간에서는 자르지 않아요. AI가 요약하거나 내용을 바꾸지 않아요</li>
          </ul>
        </Card>
      </Section>

      {dirty && (
        <div className={styles.saveBar} role="region" aria-label="저장">
          <span>저장하지 않은 변경이 있어요</span>
          <Button variant="secondary" onClick={() => setT(settings.policy.thread)}>
            되돌리기
          </Button>
          <Button busy={save.isPending} onClick={submit}>
            저장
          </Button>
        </div>
      )}

      <Sheet
        open={confirmLong}
        onClose={() => setConfirmLong(false)}
        title="장문 게시를 켤까요?"
        description="X 계정과 API에서 장문 게시가 되는 것을 확인했을 때만 켜 주세요. 확인되지 않은 채 켜면 게시가 거부되고, 그 글은 '문제 발생'으로 남아요."
        footer={
          <>
            <Button variant="secondary" onClick={() => setConfirmLong(false)}>
              취소
            </Button>
            <Button
              onClick={() => {
                set('long_post_enabled', true);
                setConfirmLong(false);
              }}
            >
              확인했어요, 켜기
            </Button>
          </>
        }
      >
        <p className={styles.muted}>저장하면 아직 승인되지 않은 글을 새 한도로 다시 나눠요. 이미 승인된 글은 그대로이고, 게시 직전에 그때의 한도로 다시 확인해요.</p>
      </Sheet>
    </div>
  );
}
