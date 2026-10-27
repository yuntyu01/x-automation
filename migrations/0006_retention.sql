-- 12단계: 게시 후 이미지 정리
--   - delete_claimed_at: 정리 작업이 이 이미지를 삭제 대상으로 선점한 시각. 프로세스가 중간에 죽어 'deleting'으로 남으면
--     일정 시간 뒤 다시 시도하고, 실패(delete_failed)는 간격을 두고 다시 시도한다
ALTER TABLE media_assets ADD COLUMN delete_claimed_at timestamptz;
CREATE INDEX media_assets_delete_retry_idx ON media_assets (delete_claimed_at) WHERE state IN ('deleting', 'delete_failed');
