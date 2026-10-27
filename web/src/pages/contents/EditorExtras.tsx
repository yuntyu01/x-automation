import type { ContentDetail } from '../../api/types';
import type { DraftState } from './ContentEditorPage';

// 편집 화면의 추가 영역 (이미지 배치·부분 재생성·예약). 기능이 연결되면 여기에 카드가 붙는다
export function EditorExtras(_props: {
  content: ContentDetail;
  draft: DraftState;
  update: (p: Partial<DraftState>) => void;
  onChanged: (d: ContentDetail) => void;
}) {
  return null;
}
