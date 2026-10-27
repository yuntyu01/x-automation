import { FileText } from 'lucide-react';
import { PageHeader } from '../../components/layout/AppShell';
import { Card } from '../../components/ui/Card';
import { EmptyState } from '../../components/ui/States';

export function ContentsPage() {
  return (
    <>
      <PageHeader title="콘텐츠" />
      <Card>
        <EmptyState icon={<FileText size={28} aria-hidden />} title="아직 글이 없어요" body="직접 쓴 글을 등록하거나 프롬프트로 생성할 수 있어요." />
      </Card>
    </>
  );
}
