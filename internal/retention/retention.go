// Package retention은 게시가 끝난 이미지를 보관 기간(기본 14일) 뒤 저장소에서 지운다.
//
// 기준은 업로드 날짜가 아니라 그 이미지가 들어간 묶음의 게시 완료 시각(media_assets.retention_anchor_at)이고,
// 여러 묶음에서 쓰면 가장 늦은 완료 시각이다. 한 번도 게시하지 않은 이미지는 이 규칙으로 지우지 않는다.
// 아직 게시할 수 있는 글(작성 중·검토·준비·예약·게시 중·확인 필요)이나 끝나지 않은 게시 실행이 쓰는 이미지는 보호한다.
// 메타데이터·원문·게시 기록·X 링크는 남기고 저장소 객체만 지운다.
package retention

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yuntyu01/x-automation/internal/clock"
	"github.com/yuntyu01/x-automation/internal/media"
	"github.com/yuntyu01/x-automation/internal/notification"
	"github.com/yuntyu01/x-automation/internal/settings"
	"github.com/yuntyu01/x-automation/internal/store"
)

const (
	batch          = 100
	staleClaim     = 10 * time.Minute // 'deleting'으로 이만큼 남아 있으면 정리 중 프로세스가 죽은 것으로 본다
	retryGap       = time.Hour        // 실패한 삭제는 이 간격으로 다시 시도한다
	maxAttempts    = 10
	pendingTimeout = 24 * time.Hour // 저장 도중 끊겨 'pending'으로 남은 행
)

// protected는 m.id 이미지를 아직 지우면 안 되는 이유가 있는지다.
const protected = `(
	EXISTS (SELECT 1 FROM part_media pm
		JOIN content_revisions r ON r.id = pm.revision_id
		JOIN content_overview o ON o.id = r.content_id
		WHERE pm.media_id = m.id AND (r.id = o.current_revision_id OR r.id = o.approved_revision_id)
		  AND o.stage NOT IN ('published', 'archived'))
	OR EXISTS (SELECT 1 FROM reservations rs JOIN part_media pm ON pm.revision_id = rs.revision_id
		WHERE pm.media_id = m.id AND rs.state IN ('assigned', 'waiting', 'publishing', 'partial', 'unknown'))
	OR EXISTS (SELECT 1 FROM publish_parts pp JOIN publish_runs pr ON pr.id = pp.run_id
		WHERE m.id = ANY(pp.media_ids) AND pr.state IN ('pending', 'running', 'partial', 'unknown', 'failed') AND pr.dismissed_at IS NULL)
)`

type Service struct {
	pool     *pgxpool.Pool
	media    *media.Service
	settings *settings.Repo
	clock    clock.Clock
	notify   *notification.Service
	log      *slog.Logger
}

func NewService(pool *pgxpool.Pool, m *media.Service, sr *settings.Repo, c clock.Clock, n *notification.Service, log *slog.Logger) *Service {
	if c == nil {
		c = clock.System{}
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{pool: pool, media: m, settings: sr, clock: c, notify: n, log: log}
}

// Report는 정리 한 번의 결과다.
type Report struct {
	Claimed        int `json:"claimed"`         // 이번에 새로 삭제 대상으로 선점
	Retried        int `json:"retried"`         // 실패·중단된 삭제를 다시 시도
	Deleted        int `json:"deleted"`         // 저장소에서 지웠거나 이미 없음을 확인
	Failed         int `json:"failed"`          // 이번에 실패 (다음 정리 때 다시)
	PendingRemoved int `json:"pending_removed"` // 저장 도중 끊긴 행 정리
}

// Sweep은 보관 기간이 지난 이미지를 선점하고 지운다. 여러 인스턴스가 동시에 돌려도 같은 이미지를 두 번 처리하지 않는다.
func (s *Service) Sweep(ctx context.Context) (Report, error) {
	var rep Report
	st, err := s.settings.Get(ctx)
	if err != nil {
		return rep, err
	}
	days := st.Policy.Retention.Days
	if days < 1 {
		days = 14
	}
	now := s.clock.Now()

	tag, err := s.pool.Exec(ctx, `DELETE FROM media_assets m WHERE state = 'pending' AND created_at < $1
		AND NOT EXISTS (SELECT 1 FROM part_media pm WHERE pm.media_id = m.id)`, now.Add(-pendingTimeout))
	if err != nil {
		return rep, err
	}
	rep.PendingRemoved = int(tag.RowsAffected())

	var ids []string
	err = store.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		// ① 후보를 잠근다. 같은 순간 새 참조를 만드는 편집은 이미지 행을 FOR SHARE로 잡으므로 여기서 건너뛰어진다
		rows, err := tx.Query(ctx, `SELECT id FROM media_assets WHERE state = 'active' AND retention_anchor_at IS NOT NULL
			AND retention_anchor_at < $1 ORDER BY retention_anchor_at LIMIT $2 FOR UPDATE SKIP LOCKED`, now.AddDate(0, 0, -days), batch)
		if err != nil {
			return err
		}
		cand, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil || len(cand) == 0 {
			return err
		}
		// ② 잠근 뒤 새 문장으로 참조를 다시 확인한다 (그 사이 커밋된 참조까지 본다). 보호 대상은 그대로 둔다
		rows, err = tx.Query(ctx, `UPDATE media_assets m SET state = 'deleting', delete_claimed_at = $2
			WHERE m.id = ANY($1::uuid[]) AND m.state = 'active' AND NOT `+protected+` RETURNING m.id`, cand, now)
		if err != nil {
			return err
		}
		ids, err = pgx.CollectRows(rows, pgx.RowTo[string])
		return err
	})
	if err != nil {
		return rep, err
	}
	rep.Claimed = len(ids)

	// 실패했거나 중간에 끊긴 삭제 (이미 'deleting'·'delete_failed'라 새 참조가 생기지 않는다)
	rows, err := s.pool.Query(ctx, `UPDATE media_assets SET state = 'deleting', delete_claimed_at = $1
		WHERE id IN (SELECT id FROM media_assets WHERE ((state = 'deleting' AND delete_claimed_at < $2)
			OR (state = 'delete_failed' AND delete_attempts < $4 AND COALESCE(delete_claimed_at, created_at) < $3))
			LIMIT $5 FOR UPDATE SKIP LOCKED)
		RETURNING id`, now, now.Add(-staleClaim), now.Add(-retryGap), maxAttempts, batch)
	if err != nil {
		return rep, err
	}
	retry, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return rep, err
	}
	rep.Retried = len(retry)

	var lastErr string
	for _, id := range append(ids, retry...) {
		if err := s.media.DeleteClaimed(ctx, id); err != nil {
			rep.Failed++
			lastErr = err.Error()
			continue
		}
		rep.Deleted++
	}
	if rep.Deleted > 0 || rep.Failed > 0 || rep.PendingRemoved > 0 {
		s.log.Info("media retention sweep", "deleted", rep.Deleted, "failed", rep.Failed, "retried", rep.Retried, "pending_removed", rep.PendingRemoved, "days", days)
	}
	if s.notify != nil {
		if rep.Failed > 0 {
			_ = s.notify.Notify(ctx, notification.New{Severity: notification.Warning, Category: "retention", GroupKey: "retention:failed",
				Title: fmt.Sprintf("이미지 %d개를 정리하지 못했어요", rep.Failed), Body: "다음 정리 때 다시 시도해요. 마지막 오류: " + lastErr,
				Link: "/images?filter=failed"})
		} else if rep.Deleted > 0 {
			var left int
			_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM media_assets WHERE state = 'delete_failed'`).Scan(&left)
			if left == 0 {
				_ = s.notify.Resolve(ctx, "retention:failed")
			}
		}
		if rep.Claimed > 0 && rep.Deleted > 0 {
			_ = s.notify.Notify(ctx, notification.New{Severity: notification.Info, Category: "retention", GroupKey: "retention:done",
				Title: "보관 기간이 지난 이미지를 정리했어요", Body: fmt.Sprintf("게시 완료 후 %d일이 지난 이미지 %d개를 저장소에서 지웠어요. 게시 기록과 X 링크는 남아 있어요", days, rep.Deleted),
				Link: "/images?filter=cleaned"})
		}
	}
	return rep, nil
}
