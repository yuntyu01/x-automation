import { useQueryClient } from '@tanstack/react-query';
import { ClipboardPaste, Lightbulb, Plus } from 'lucide-react';
import { useState } from 'react';
import { Link, useNavigate } from 'react-router';
import { errorMessage, post } from '../../api/client';
import { usePrompts } from '../../api/contents';
import type { Prompt } from '../../api/types';
import { PageHeader } from '../../components/layout/AppShell';
import { Badge } from '../../components/ui/Badge';
import { Button } from '../../components/ui/Button';
import { Card } from '../../components/ui/Card';
import { Segmented } from '../../components/ui/Segmented';
import { EmptyState, ErrorState, LoadingList } from '../../components/ui/States';
import { useToast } from '../../components/ui/Toast';
import { formatDateTime } from '../../lib/format';
import { BackButton } from '../settings/ConnectionsPage';
import styles from './PromptsPage.module.css';

export function PromptsPage() {
  const [show, setShow] = useState<'active' | 'all'>('active');
  const q = usePrompts(show === 'all');
  const nav = useNavigate();
  const toast = useToast();
  const qc = useQueryClient();

  async function addExample() {
    try {
      const p = await post<Prompt>('/api/prompts/example');
      await qc.invalidateQueries({ queryKey: ['prompts'] });
      toast.show('예시 프롬프트를 추가했어요. 쓰던 내용으로 바꿔 쓸 수 있어요', 'success');
      nav(`/prompts/${p.id}`);
    } catch (e) {
      toast.show(errorMessage(e), 'error');
    }
  }

  return (
    <>
      <PageHeader
        title="프롬프트"
        subtitle="쓰던 프롬프트를 그대로 붙여넣어 관리해요"
        back={<BackButton />}
        actions={
          <Button size="sm" icon={<Plus size={18} />} onClick={() => nav('/prompts/new')}>
            새 프롬프트
          </Button>
        }
      />
      <div className={styles.filters}>
        <Segmented
          label="표시"
          value={show}
          onChange={setShow}
          options={[
            { value: 'active', label: '사용 중' },
            { value: 'all', label: '비활성 포함' },
          ]}
        />
      </div>
      {q.isLoading && <LoadingList />}
      {q.isError && <ErrorState error={q.error} onRetry={() => q.refetch()} />}
      {q.data && q.data.items.length === 0 && (
        <Card>
          <EmptyState icon={<ClipboardPaste size={28} aria-hidden />} title="프롬프트 입력이 필요해요" body="AI로 글을 만들려면 쓰던 프롬프트를 등록해 주세요. 직접 쓴 글만 올린다면 없어도 돼요.">
            <Button icon={<ClipboardPaste size={18} />} onClick={() => nav('/prompts/new')}>
              프롬프트 붙여넣기
            </Button>
            <Button variant="secondary" icon={<Lightbulb size={18} />} onClick={addExample}>
              예시 보기
            </Button>
          </EmptyState>
        </Card>
      )}
      <ul className={styles.list}>
        {q.data?.items.map((p) => (
          <li key={p.id}>
            <Link to={`/prompts/${p.id}`} className={[styles.card, p.active ? '' : styles.inactive].join(' ')}>
              <div className={styles.head}>
                {p.category && <span className={styles.cat}>{p.category}</span>}
                {p.is_example && <Badge tone="warning">예시</Badge>}
                {!p.active && <Badge tone="neutral">비활성</Badge>}
                <span className={styles.ver}>v{p.current_version}</span>
              </div>
              <p className={styles.name}>{p.name}</p>
              {p.description && <p className={styles.desc}>{p.description}</p>}
              <p className={styles.meta}>
                변수 {p.version.variables.length}개 · 생성에 {p.usage_count}번 사용 · {formatDateTime(p.updated_at)} 수정
              </p>
            </Link>
          </li>
        ))}
      </ul>
    </>
  );
}
