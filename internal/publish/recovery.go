package publish

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yuntyu01/x-automation/internal/budget"
	"github.com/yuntyu01/x-automation/internal/integration/xapi"
	"github.com/yuntyu01/x-automation/internal/jobs"
	"github.com/yuntyu01/x-automation/internal/store"
)

// ── 조회 ──────────────────────────────────────────────────────────────

type PartView struct {
	Position    int        `json:"position"`
	Text        string     `json:"text"`
	MediaCount  int        `json:"media_count"`
	State       string     `json:"state"`
	PostID      *string    `json:"x_post_id"`
	URL         *string    `json:"x_url"`
	InReplyTo   *string    `json:"in_reply_to"`
	Attempts    int        `json:"attempts"`
	NotBefore   *time.Time `json:"not_before"`
	IntentAt    *time.Time `json:"intent_at"`
	SucceededAt *time.Time `json:"succeeded_at"`
	Error       *string    `json:"error"`
	ErrorClass  *string    `json:"error_class"`
	MadeWithAI  bool       `json:"made_with_ai"`
}

type RunView struct {
	ID            string     `json:"id"`
	ReservationID string     `json:"reservation_id"`
	ContentID     string     `json:"content_id"`
	Title         string     `json:"title"`
	Trigger       string     `json:"trigger"`
	State         string     `json:"state"`
	Note          *string    `json:"note"`
	SlotAt        time.Time  `json:"slot_at"`
	StartedAt     *time.Time `json:"started_at"`
	CompletedAt   *time.Time `json:"completed_at"`
	DismissedAt   *time.Time `json:"dismissed_at"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	Parts         []PartView `json:"parts"`
}

type RunList struct {
	Active    []RunView `json:"active"`    // 진행 중 (대기·전송·답글 간격)
	Attention []RunView `json:"attention"` // 사람이 확인해야 함 (일부 성공·결과 불명확·실패)
	Recent    []RunView `json:"recent"`    // 최근 끝난 실행
}

const runCols = `pr.id, pr.reservation_id, pr.content_id, c.title, pr.trigger, pr.state, pr.note, rs.slot_at, pr.started_at, pr.completed_at,
	pr.dismissed_at, pr.created_at, pr.updated_at`

func (s *Service) queryRuns(ctx context.Context, where string, args ...any) ([]RunView, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+runCols+` FROM publish_runs pr JOIN contents c ON c.id = pr.content_id
		JOIN reservations rs ON rs.id = pr.reservation_id WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (RunView, error) {
		var v RunView
		err := r.Scan(&v.ID, &v.ReservationID, &v.ContentID, &v.Title, &v.Trigger, &v.State, &v.Note, &v.SlotAt, &v.StartedAt, &v.CompletedAt,
			&v.DismissedAt, &v.CreatedAt, &v.UpdatedAt)
		v.Parts = []PartView{}
		return v, err
	})
	if err != nil || len(list) == 0 {
		return list, err
	}
	ids := make([]string, len(list))
	idx := map[string]int{}
	for i, v := range list {
		ids[i] = v.ID
		idx[v.ID] = i
	}
	prows, err := s.Pool.Query(ctx, `SELECT run_id, position, text, cardinality(media_ids), state, x_post_id, x_url, in_reply_to, attempts, not_before,
		intent_at, succeeded_at, error, error_class, made_with_ai FROM publish_parts WHERE run_id = ANY($1) ORDER BY run_id, position`, ids)
	if err != nil {
		return nil, err
	}
	defer prows.Close()
	for prows.Next() {
		var runID string
		var p PartView
		if err := prows.Scan(&runID, &p.Position, &p.Text, &p.MediaCount, &p.State, &p.PostID, &p.URL, &p.InReplyTo, &p.Attempts, &p.NotBefore,
			&p.IntentAt, &p.SucceededAt, &p.Error, &p.ErrorClass, &p.MadeWithAI); err != nil {
			return nil, err
		}
		list[idx[runID]].Parts = append(list[idx[runID]].Parts, p)
	}
	return list, prows.Err()
}

// ListRuns는 게시 화면용 목록이다.
func (s *Service) ListRuns(ctx context.Context) (RunList, error) {
	var out RunList
	var err error
	if out.Active, err = s.queryRuns(ctx, `pr.state IN ('pending', 'running') ORDER BY pr.created_at`); err != nil {
		return out, err
	}
	if out.Attention, err = s.queryRuns(ctx, `pr.state IN ('partial', 'unknown', 'failed') AND pr.dismissed_at IS NULL ORDER BY pr.updated_at DESC`); err != nil {
		return out, err
	}
	out.Recent, err = s.queryRuns(ctx, `pr.state IN ('completed', 'canceled') OR pr.dismissed_at IS NOT NULL ORDER BY pr.updated_at DESC LIMIT 20`)
	return out, err
}

// Run은 실행 하나다.
func (s *Service) Run(ctx context.Context, id string) (RunView, error) {
	list, err := s.queryRuns(ctx, `pr.id = $1`, id)
	if err != nil {
		return RunView{}, err
	}
	if len(list) == 0 {
		return RunView{}, ErrNotFound
	}
	return list[0], nil
}

// ── 복구 ──────────────────────────────────────────────────────────────

// resume은 확인을 마친 실행을 다시 진행한다: 남은 부분(pending)부터 새 작업으로 보낸다.
func (s *Service) resume(ctx context.Context, tx pgx.Tx, r run) error {
	if _, err := tx.Exec(ctx, `UPDATE publish_runs SET state = 'running', note = NULL, updated_at = now() WHERE id = $1`, r.id); err != nil {
		if store.IsUniqueViolation(err) {
			return ErrBusy
		}
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE reservations SET state = 'publishing', block_reason = NULL, updated_at = now() WHERE id = $1`, r.reservationID); err != nil {
		return err
	}
	// 같은 실행의 재개는 새 작업 키로 등록한다 (이전 작업은 이미 끝난 상태로 남는다)
	key := fmt.Sprintf("%s:%d", r.id, s.Clock.Now().UnixNano())
	_, _, err := s.Queue.Enqueue(ctx, tx, JobKind, key, map[string]string{"run_id": r.id}, jobs.EnqueueOptions{MaxAttempts: maxAttempts})
	return err
}

func (s *Service) lockRun(ctx context.Context, tx pgx.Tx, id string) (run, []part, error) {
	if _, err := tx.Exec(ctx, `SELECT 1 FROM publish_runs WHERE id = $1 FOR UPDATE`, id); err != nil {
		return run{}, nil, err
	}
	return s.loadRun(ctx, tx, id)
}

// ResolveInput은 결과 불명확 부분을 X에서 확인한 결과다.
type ResolveInput struct {
	Position int    `json:"position"`
	Posted   bool   `json:"posted"`   // X에 올라가 있었다
	PostRef  string `json:"post_ref"` // 올라갔으면 게시물 주소나 ID
}

// Resolve는 결과 불명확 부분을 사용자가 확인한 대로 정리한다.
// 올라갔으면 그 ID로 성공 처리하고 남은 답글을 이어 간다. 안 올라갔으면 그 부분부터 다시 보낸다.
func (s *Service) Resolve(ctx context.Context, runID string, in ResolveInput) error {
	var postID, url string
	if in.Posted {
		id, ok := ParsePostRef(in.PostRef)
		if !ok {
			return &ValidationError{Fields: map[string]string{"post_ref": "X 게시물 주소(…/status/숫자)나 숫자 ID를 넣어 주세요"}}
		}
		postID = id
		v, err := s.conns.View(ctx)
		if err != nil {
			return err
		}
		url = xapi.PostURL(v.Username, postID)
	}
	st, err := s.Settings.Get(ctx)
	if err != nil {
		return err
	}
	if !in.Posted {
		if reason, err := s.blockReason(ctx, st, false); err != nil {
			return err
		} else if reason != "" {
			return &ValidationError{Fields: map[string]string{"run": reason}}
		}
	}
	ref := fmt.Sprintf("%s:%d", runID, in.Position)
	completed := false
	err = store.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		r, parts, err := s.lockRun(ctx, tx, runID)
		if err != nil {
			return err
		}
		if r.state != "unknown" {
			return &ValidationError{Fields: map[string]string{"run": "결과 확인이 필요한 실행이 아니에요"}}
		}
		var p *part
		for i := range parts {
			if parts[i].position == in.Position {
				p = &parts[i]
			}
		}
		if p == nil || p.state != "unknown" {
			return &ValidationError{Fields: map[string]string{"position": "결과 확인이 필요한 부분이 아니에요"}}
		}
		if in.Posted {
			if _, err := tx.Exec(ctx, `UPDATE publish_parts SET state = 'succeeded', x_post_id = $3, x_url = $4, succeeded_at = COALESCE(intent_at, now()),
				error = NULL, error_class = NULL WHERE run_id = $1 AND position = $2`, runID, in.Position, postID, url); err != nil {
				return err
			}
			p.state = "succeeded"
		} else {
			if _, err := tx.Exec(ctx, `UPDATE publish_parts SET state = 'pending', error = NULL, error_class = NULL, not_before = NULL
				WHERE run_id = $1 AND position = $2`, runID, in.Position); err != nil {
				return err
			}
			p.state = "pending"
		}
		remaining := false
		for _, q := range parts {
			if q.state != "succeeded" {
				remaining = true
			}
		}
		if !remaining {
			completed = true
			now := s.Clock.Now()
			if _, err := tx.Exec(ctx, `UPDATE publish_runs SET state = 'completed', completed_at = $2, note = NULL, updated_at = now() WHERE id = $1`, runID, now); err != nil {
				return err
			}
			if err := anchorMedia(ctx, tx, runID, now); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `UPDATE reservations SET state = 'published', block_reason = NULL, updated_at = now() WHERE id = $1`, r.reservationID)
			return err
		}
		if in.Posted && parts[0].state == "succeeded" && in.Position == 0 {
			// 본문이 올라간 것으로 확인됐으니 뒤 예약 간격을 본문 시작 기준으로 맞춘다
			if _, err := tx.Exec(ctx, `UPDATE publish_runs SET body_started_at = COALESCE(body_started_at, now()) WHERE id = $1`, runID); err != nil {
				return err
			}
		}
		for _, q := range parts {
			if q.state == "unknown" || q.state == "failed" {
				// 다른 부분이 아직 확인·재시도를 기다린다: 상태만 갱신하고 진행하지 않는다
				_, err := tx.Exec(ctx, `UPDATE publish_runs SET updated_at = now() WHERE id = $1`, runID)
				return err
			}
		}
		return s.resume(ctx, tx, r)
	})
	if err != nil {
		return err
	}
	if in.Posted {
		_ = s.Budget.Confirm(ctx, budget.XPost, ref)
	} else {
		_ = s.Budget.Void(ctx, budget.XPost, ref)
	}
	if s.Notify != nil {
		_ = s.Notify.Resolve(ctx, "publish:unknown:"+runID)
	}
	if completed {
		_ = s.Budget.Settle(ctx, "post:"+runID)
	} else {
		s.kick()
	}
	return nil
}

// Retry는 실패·결과 불명확 부분부터 다시 보낸다. 결과 불명확 부분은 중복 게시 위험을 확인해야 한다.
func (s *Service) Retry(ctx context.Context, runID string, acceptDuplicateRisk bool) error {
	st, err := s.Settings.Get(ctx)
	if err != nil {
		return err
	}
	if reason, err := s.blockReason(ctx, st, false); err != nil {
		return err
	} else if reason != "" {
		return &ValidationError{Fields: map[string]string{"run": reason}}
	}
	err = store.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		r, parts, err := s.lockRun(ctx, tx, runID)
		if err != nil {
			return err
		}
		if r.state != "partial" && r.state != "failed" && r.state != "unknown" {
			return &ValidationError{Fields: map[string]string{"run": "다시 시도할 수 있는 상태가 아니에요"}}
		}
		for _, p := range parts {
			if p.state == "unknown" && !acceptDuplicateRisk {
				return &WarningError{Warnings: []string{
					"결과를 모르는 부분이 있어요. X에서 먼저 확인해 주세요. 이미 올라가 있었다면 다시 보내면 같은 글이 두 번 올라가요",
				}}
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE publish_parts SET state = 'pending', error = NULL, error_class = NULL, not_before = NULL
			WHERE run_id = $1 AND state IN ('failed', 'unknown')`, runID); err != nil {
			return err
		}
		return s.resume(ctx, tx, r)
	})
	if err != nil {
		return err
	}
	if s.Notify != nil {
		_ = s.Notify.Resolve(ctx, "publish:unknown:"+runID)
		_ = s.Notify.Resolve(ctx, "publish:failed:"+runID)
	}
	s.kick()
	return nil
}

// Abandon은 남은 부분을 보내지 않고 실행을 끝낸다. 무엇이든 보냈거나 보냈을 수 있으면 글을 자동 배치에서 빼서
// 같은 글이 다시 올라가지 않게 한다.
func (s *Service) Abandon(ctx context.Context, runID string) error {
	err := store.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		r, parts, err := s.lockRun(ctx, tx, runID)
		if err != nil {
			return err
		}
		if r.state != "partial" && r.state != "failed" && r.state != "unknown" {
			return &ValidationError{Fields: map[string]string{"run": "진행 중이거나 이미 끝난 실행은 중단할 수 없어요"}}
		}
		bodyOut := len(parts) > 0 && parts[0].state == "succeeded"
		if _, err := tx.Exec(ctx, `UPDATE publish_runs SET state = 'canceled', dismissed_at = now(), note = '사용자가 남은 부분을 보내지 않고 끝냈어요', updated_at = now() WHERE id = $1`, runID); err != nil {
			return err
		}
		// 끝낸 묶음에서 이미 올라간 이미지는 이 시각부터 보관 기간을 센다
		if err := anchorMedia(ctx, tx, runID, s.Clock.Now()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE publish_parts SET state = 'failed', error = COALESCE(error, '중단'), error_class = COALESCE(error_class, 'canceled')
			WHERE run_id = $1 AND state = 'pending'`, runID); err != nil {
			return err
		}
		resState := "canceled"
		if bodyOut {
			resState = "published"
		}
		if _, err := tx.Exec(ctx, `UPDATE reservations SET state = $2, block_reason = NULL, delay_note = '게시 중단', updated_at = now() WHERE id = $1`, r.reservationID, resState); err != nil {
			return err
		}
		// 다시 시간표에 자동으로 올라가지 않게 한다 (보관함에서 직접 다시 배치할 수 있다)
		_, err = tx.Exec(ctx, `UPDATE contents SET schedule_hold = true, updated_at = now() WHERE id = $1`, r.contentID)
		return err
	})
	if err != nil {
		return err
	}
	_ = s.Budget.Release(ctx, "post:"+runID)
	if s.Notify != nil {
		_ = s.Notify.Resolve(ctx, "publish:unknown:"+runID)
		_ = s.Notify.Resolve(ctx, "publish:failed:"+runID)
	}
	return nil
}

// Reconcile은 작업이 끝났는데 실행이 진행 중으로 남은 경우(재시도 한도 초과·저장 실패)를 정리한다.
// 보내던 부분이 있으면 결과 불명확, 아니면 실패로 둔다. 다시 보내지 않는다.
func (s *Service) Reconcile(ctx context.Context) (int, error) {
	rows, err := s.Pool.Query(ctx, `SELECT pr.id FROM publish_runs pr
		WHERE pr.state IN ('pending', 'running') AND pr.updated_at < $1 AND NOT EXISTS (
			SELECT 1 FROM jobs j WHERE j.kind = $2 AND (j.unique_key = pr.id::text OR j.unique_key LIKE pr.id::text || ':%')
			AND j.state IN ('queued', 'running'))`, s.Clock.Now().Add(-time.Minute), JobKind)
	if err != nil {
		return 0, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, err
	}
	n := 0
	for _, id := range ids {
		r, parts, err := s.loadRun(ctx, s.Pool, id)
		if err != nil {
			return n, err
		}
		bodyDone := len(parts) > 0 && parts[0].state == "succeeded"
		var p *part
		for i := range parts {
			if parts[i].state != "succeeded" {
				p = &parts[i]
				break
			}
		}
		switch {
		case p == nil:
			s.finishCompleted(ctx, r)
		case p.state == "sending" || p.state == "unknown":
			s.finishUnknown(ctx, r, p, errors.New("작업이 끊겨 결과를 알 수 없어요"))
		default:
			s.finishFailed(ctx, r, p, errors.New("작업이 재시도 한도를 넘어 멈췄어요. 확인 후 다시 시도해 주세요"), bodyDone)
		}
		n++
	}
	return n, nil
}
