-- 09단계: 시간표 운영에 필요한 값
--   - 사용자가 예약에서 뺀 콘텐츠는 다시 자동 배치하지 않는다 (직접 다시 넣을 때까지)
--   - 미준비 시 준비된 다른 글로 대체하는 것은 사용자가 고른 경우만
--   - 같은 시각에 진행 중인 예약이 둘 생기지 않게 한다 (여러 인스턴스가 동시에 시간표를 채워도)
ALTER TABLE contents ADD COLUMN schedule_hold boolean NOT NULL DEFAULT false;

ALTER TABLE schedule_rules ADD COLUMN substitute_unready boolean NOT NULL DEFAULT false;
ALTER TABLE schedule_rules ADD COLUMN version integer NOT NULL DEFAULT 1;

CREATE UNIQUE INDEX reservations_slot_active_uq ON reservations (slot_at)
    WHERE state IN ('open', 'assigned', 'waiting', 'publishing', 'partial', 'unknown', 'published');
