import { MODE_LABEL, type OperationMode } from '../../api/types';

export const MODE_OPTIONS: { value: OperationMode; label: string; description: string }[] = [
  { value: 'publish_only', label: MODE_LABEL.publish_only, description: '직접 쓴 글과 이미지를 나누고 예약해 올려요' },
  { value: 'review', label: MODE_LABEL.review, description: '생성된 글은 검토 대기로 두고, 승인한 글만 올려요' },
  { value: 'full_auto', label: MODE_LABEL.full_auto, description: '설정대로 준비하고 검증을 통과한 글을 예약 게시해요' },
];

export function generationSummary(t: 'on_publish' | 'manual' | 'prepare') {
  return t === 'on_publish' ? '게시할 때' : t === 'manual' ? '버튼을 눌렀을 때' : '미리 자동 생성';
}
