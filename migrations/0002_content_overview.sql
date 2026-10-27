-- 콘텐츠 보관함 필터용 단계 계산. 저장된 상태는 책임별 컬럼에 그대로 두고, 화면 필터만 여기서 합친다.
CREATE VIEW content_overview AS
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
        WHEN c.generation_state = 'failed' OR lr.state IN ('partial', 'unknown', 'failed') THEN 'problem'
        WHEN ar.id IS NOT NULL THEN 'scheduled'
        WHEN EXISTS (SELECT 1 FROM publish_runs pr WHERE pr.content_id = c.id AND pr.state = 'completed') THEN 'published'
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
    SELECT state, completed_at FROM publish_runs WHERE content_id = c.id ORDER BY created_at DESC LIMIT 1
) lr ON true;
