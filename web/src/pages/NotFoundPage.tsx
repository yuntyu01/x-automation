import { Link } from 'react-router';
import { PageHeader } from '../components/layout/AppShell';
import { Card } from '../components/ui/Card';
import { EmptyState } from '../components/ui/States';

export function NotFoundPage() {
  return (
    <>
      <PageHeader title="페이지를 찾을 수 없어요" />
      <Card>
        <EmptyState title="주소가 바뀌었거나 없는 화면이에요">
          <Link to="/">홈으로 가기</Link>
        </EmptyState>
      </Card>
    </>
  );
}
