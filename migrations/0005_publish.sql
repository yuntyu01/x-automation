-- 10단계: 게시 실행
--   - block_reason: 게시를 시작하지 못하게 막은 조건 (실게시 꺼짐·연결 필요·일시정지·구간 밖). 상태·대기 사유와 따로 둔다
--   - body_started_at: 본문 전송을 시작한 시각 (실제 간격 계산·하루 수량)
--   - x_media_uploaded_at: X 미디어 ID는 일정 시간 뒤 만료되므로 오래되면 다시 올린다
--   - made_with_ai: AI 생성 이미지가 붙은 게시에 공개 표시를 보낼지 (설정)
ALTER TABLE reservations ADD COLUMN block_reason text;
ALTER TABLE publish_runs ADD COLUMN body_started_at timestamptz;
ALTER TABLE publish_runs ADD COLUMN note text;
ALTER TABLE publish_parts ADD COLUMN x_media_uploaded_at timestamptz;
ALTER TABLE publish_parts ADD COLUMN made_with_ai boolean NOT NULL DEFAULT false;
-- 한 번에 하나의 묶음만 게시를 진행한다 (계정 하나, 본문 시작 순서·간격 보장)
CREATE UNIQUE INDEX publish_runs_one_active_uq ON publish_runs ((true)) WHERE state IN ('pending', 'running');
-- 중단한 실행이라도 본문이 X에 올라갔으면 '게시됨'으로 본다 (자동 재배치로 같은 글이 다시 올라가지 않게)
ALTER TABLE publish_runs ADD COLUMN dismissed_at timestamptz;
CREATE OR REPLACE VIEW content_overview AS
SELECT
    c.id, c.title, c.category, c.tags, c.source, c.is_demo, c.current_revision_id, c.approved_revision_id,
    c.generation_state, c.generation_error, c.archived_at, c.edit_version, c.created_at, c.updated_at,
    r.number AS revision_number, r.raw_text, r.split_mode, r.image_mode, r.review_state, r.publishable,
    r.validation,
    (SELECT count(*) FROM thread_parts tp WHERE tp.revision_id = r.id) AS part_count,
    ar.id AS active_reservation_id, ar.slot_at AS active_slot_at, ar.state AS reservation_state, ar.wait_reason,
    lr.state AS last_run_state, lr.completed_at AS last_published_at,
    CASE
        WHEN c.archived_at IS NOT NULL THEN 'archived'
        WHEN c.generation_state = 'failed' OR (lr.state IN ('partial', 'unknown', 'failed') AND lr.dismissed_at IS NULL) THEN 'problem'
        WHEN ar.id IS NOT NULL THEN 'scheduled'
        WHEN EXISTS (SELECT 1 FROM publish_runs pr WHERE pr.content_id = c.id AND (pr.state = 'completed'
             OR EXISTS (SELECT 1 FROM publish_parts pp WHERE pp.run_id = pr.id AND pp.position = 0 AND pp.state IN ('succeeded', 'unknown')))) THEN 'published'
        WHEN c.generation_state IN ('queued', 'running') THEN 'draft'
        WHEN NOT r.publishable THEN 'draft'
        WHEN (SELECT operation_mode FROM app_settings WHERE id = 1) = 'review'
             AND c.approved_revision_id IS DISTINCT FROM c.current_revision_id THEN 'review'
        ELSE 'ready'
    END AS stage
FROM contents c
JOIN content_revisions r ON r.id = c.current_revision_id
LEFT JOIN LATERAL (
    SELECT id, slot_at, state, wait_reason FROM reservations
    WHERE content_id = c.id AND state IN ('assigned', 'waiting', 'publishing', 'partial', 'unknown')
    ORDER BY slot_at LIMIT 1
) ar ON true
LEFT JOIN LATERAL (
    SELECT state, completed_at, dismissed_at FROM publish_runs WHERE content_id = c.id ORDER BY created_at DESC LIMIT 1
) lr ON true;
