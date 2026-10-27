import { CalendarClock } from 'lucide-react';
import { PageHeader } from '../../components/layout/AppShell';
import { Card } from '../../components/ui/Card';
import { EmptyState } from '../../components/ui/States';

export function SchedulePage() {
  return (
    <>
      <PageHeader title="예약" />
      <Card>
        <EmptyState icon={<CalendarClock size={28} aria-hidden />} title="예약이 없어요" body="시간표를 만들면 새벽·낮 허용 구간에 맞춰 예약이 채워져요." />
      </Card>
    </>
  );
}
