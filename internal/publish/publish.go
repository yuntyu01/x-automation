// Package publish는 예약된 묶음을 X에 본문 → 자기 답글 순서로 게시하고, 실패·불명확 결과를 안전하게 다룬다.
//
// 안전 원칙 (지시서 5.3·12)
//   - 게시 의도(sending)를 먼저 기록하고 보낸다. 성공 ID는 받는 즉시 저장한다
//   - X에는 멱등성 키가 없다. 보낸 뒤 결과를 모르면 unknown(결과 확인 필요)으로 두고 자동으로 다시 보내지 않는다
//   - 본문이 올라간 뒤 답글이 실패하면 본문을 다시 올리지 않고 실패한 답글부터 이어 간다
//   - 한 번에 한 묶음만 진행하고(DB 고유 색인), 본문 시작 간격을 DB 기록으로 지킨다
//   - 새 외부 요청을 시작하기 전마다 실게시 스위치·자동 운영·일시정지·연결·허용 시간대·작업 소유권을 확인한다
package publish

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yuntyu01/x-automation/internal/budget"
	"github.com/yuntyu01/x-automation/internal/clock"
	"github.com/yuntyu01/x-automation/internal/connection"
	"github.com/yuntyu01/x-automation/internal/integration"
	"github.com/yuntyu01/x-automation/internal/integration/xapi"
	"github.com/yuntyu01/x-automation/internal/jobs"
	"github.com/yuntyu01/x-automation/internal/media"
	"github.com/yuntyu01/x-automation/internal/notification"
	"github.com/yuntyu01/x-automation/internal/schedule"
	"github.com/yuntyu01/x-automation/internal/settings"
	"github.com/yuntyu01/x-automation/internal/store"
	"github.com/yuntyu01/x-automation/internal/thread"
)

const (
	JobKind       = "publish.run"
	dueGrace      = 10 * time.Minute // schedule.overdueGrace와 같다: 이보다 늦은 예약은 시작하지 않는다
	mediaIDMaxAge = 20 * time.Hour   // X 미디어 ID 만료(24시간) 전에 다시 올린다
	maxAttempts   = 8
)

var (
	ErrBusy     = errors.New("다른 묶음을 게시하는 중이에요. 끝난 뒤 다시 해 주세요")
	ErrNotFound = errors.New("게시 기록을 찾을 수 없어요")
)

type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string { return "입력값을 확인해 주세요" }

// WarningError는 확인하면 진행할 수 있는 경고다 ("지금 게시"가 허용 구간·간격 밖일 때).
type WarningError struct{ Warnings []string }

func (e *WarningError) Error() string { return fmt.Sprint(e.Warnings) }

// Gates는 서버 설정의 게시 스위치다.
type Gates struct {
	Live              bool // 실제 X (목업이면 false: 메모리 대역에만 "게시")
	PublishingEnabled bool // PUBLISHING_ENABLED
}

type Deps struct {
	Pool     *pgxpool.Pool
	Clock    clock.Clock
	Queue    *jobs.Queue
	Conn     *connection.Service
	Settings *settings.Repo
	Schedule *schedule.Service
	Media    *media.Service
	Budget   *budget.Service
	Notify   *notification.Service
	Log      *slog.Logger
	Gates    Gates
}

type Service struct {
	Deps
	conns *connection.Repo
	kick  func()
}

func NewService(d Deps) *Service {
	if d.Clock == nil {
		d.Clock = clock.System{}
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	return &Service{Deps: d, conns: connection.NewRepo(d.Pool), kick: func() {}}
}

func (s *Service) SetKick(fn func()) { s.kick = fn }

// blockReason은 지금 새 게시를 시작하면 안 되는 이유다. 비어 있으면 시작해도 된다.
func (s *Service) blockReason(ctx context.Context, st settings.Settings, automatic bool) (string, error) {
	if s.Gates.Live && !s.Gates.PublishingEnabled {
		return "서버에서 실게시가 꺼져 있어요 (PUBLISHING_ENABLED)", nil
	}
	if automatic && !st.AutomationEnabled {
		return "자동 운영이 꺼져 있어요", nil
	}
	if st.PublishingPaused {
		return "게시가 일시정지되어 있어요", nil
	}
	v, err := s.conns.View(ctx)
	if err != nil {
		return "", err
	}
	switch v.Status {
	case connection.Connected:
		return "", nil
	case connection.Expired:
		return "X 연결이 만료됐어요. 다시 연결해 주세요", nil
	case connection.Errored:
		return "X 연결에 문제가 있어요: " + v.StatusDetail, nil
	}
	return "X 계정을 연결해야 해요", nil
}

// ── 실행 만들기 ───────────────────────────────────────────────────────

type partSrc struct {
	position int
	text     string
	media    []string
	ai       bool
	hasURL   bool
}

// createRun은 예약 하나의 게시 실행을 만든다 (부분·이미지 복사, revision 고정, 비용 예약, 작업 등록). tx 안에서 호출한다.
func (s *Service) createRun(ctx context.Context, tx pgx.Tx, st settings.Settings, reservationID, contentID, revisionID, trigger string) (string, error) {
	var publishable bool
	if err := tx.QueryRow(ctx, `SELECT publishable FROM content_revisions WHERE id = $1`, revisionID).Scan(&publishable); err != nil {
		return "", err
	}
	rows, err := tx.Query(ctx, `SELECT tp.position, tp.text,
		COALESCE(array_agg(pm.media_id ORDER BY pm.position) FILTER (WHERE pm.media_id IS NOT NULL), '{}'),
		COALESCE(bool_or(m.source = 'ai'), false)
		FROM thread_parts tp
		LEFT JOIN part_media pm ON pm.revision_id = tp.revision_id AND pm.part_index = tp.position
		LEFT JOIN media_assets m ON m.id = pm.media_id
		WHERE tp.revision_id = $1 GROUP BY tp.position, tp.text ORDER BY tp.position`, revisionID)
	if err != nil {
		return "", err
	}
	parts, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (partSrc, error) {
		var p partSrc
		err := r.Scan(&p.position, &p.text, &p.media, &p.ai)
		return p, err
	})
	if err != nil {
		return "", err
	}
	// 게시 직전에 지금 한도로 다시 확인한다 (승인 뒤 장문 설정이 바뀐 경우 등)
	limit := thread.V3.MaxWeighted
	if st.Policy.Thread.LongPostEnabled {
		limit = thread.LongPostLimit
	}
	if !publishable || len(parts) == 0 {
		return "", &ValidationError{Fields: map[string]string{"content": "게시할 수 없는 상태의 글이에요 (길이·이미지 문제)"}}
	}
	var cost int64
	for i := range parts {
		p := thread.ParseLimit(parts[i].text, thread.V3, limit)
		if !p.Valid {
			return "", &ValidationError{Fields: map[string]string{"content": fmt.Sprintf("%d번째 부분이 지금 한도(%d자)를 넘어요", i+1, limit)}}
		}
		parts[i].hasURL = thread.HasURL(parts[i].text)
		cost += budget.PostMicros(st.Policy.Pricing, parts[i].hasURL)
	}
	var runID string
	err = tx.QueryRow(ctx, `INSERT INTO publish_runs (reservation_id, content_id, revision_id, trigger) VALUES ($1, $2, $3, $4) RETURNING id`,
		reservationID, contentID, revisionID, trigger).Scan(&runID)
	if store.IsUniqueViolation(err) {
		return "", ErrBusy
	}
	if err != nil {
		return "", err
	}
	for _, p := range parts {
		if _, err := tx.Exec(ctx, `INSERT INTO publish_parts (run_id, position, text, media_ids, made_with_ai) VALUES ($1, $2, $3, $4, $5)`,
			runID, p.position, p.text, p.media, p.ai && st.Policy.Image.MadeWithAI); err != nil {
			return "", err
		}
	}
	// 게시에 쓰는 revision은 바뀌지 않게 고정한다 (이후 편집은 새 revision)
	if _, err := tx.Exec(ctx, `UPDATE content_revisions SET frozen_at = COALESCE(frozen_at, now()) WHERE id = $1`, revisionID); err != nil {
		return "", err
	}
	if _, _, err := s.Queue.Enqueue(ctx, tx, JobKind, runID, map[string]string{"run_id": runID}, jobs.EnqueueOptions{MaxAttempts: maxAttempts}); err != nil {
		return "", err
	}
	// 묶음 중간에 답글을 끊지 않게 시작 전에 전체 비용을 확보한다 (한도가 있을 때만)
	if err := s.Budget.Reserve(ctx, "post:"+runID, budget.XPost, cost); err != nil {
		return "", err
	}
	return runID, nil
}

// Dispatch는 시각이 된 준비 완료 예약 하나를 게시 실행으로 넘긴다. 막혔으면 이유를 예약에 남긴다.
func (s *Service) Dispatch(ctx context.Context) (string, error) {
	st, err := s.Settings.Get(ctx)
	if err != nil {
		return "", err
	}
	now := s.Clock.Now()
	var resID string
	err = s.Pool.QueryRow(ctx, `SELECT id FROM reservations WHERE state = 'assigned' AND slot_at <= $1 AND slot_at > $2 ORDER BY slot_at LIMIT 1`,
		now, now.Add(-dueGrace)).Scan(&resID)
	if store.IsNoRows(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	block := func(reason string) (string, error) {
		_, err := s.Pool.Exec(ctx, `UPDATE reservations SET block_reason = $2, updated_at = now() WHERE id = $1 AND block_reason IS DISTINCT FROM $2`, resID, reason)
		return "", err
	}
	reason, err := s.blockReason(ctx, st, true)
	if err != nil {
		return "", err
	}
	if reason != "" {
		if st.AutomationEnabled && s.Notify != nil {
			_ = s.Notify.Notify(ctx, notification.New{Severity: notification.Warning, Category: "publish", GroupKey: "publish:blocked",
				Title: "예약 게시를 시작하지 못했어요", Body: reason, Link: "/settings/connections"})
		}
		return block(reason)
	}
	w, err := s.Schedule.Window(ctx, now)
	if err != nil {
		return "", err
	}
	if !w.Allowed {
		return block(w.Reason)
	}
	var last *time.Time
	if err := s.Pool.QueryRow(ctx, `SELECT max(body_started_at) FROM publish_runs WHERE body_started_at IS NOT NULL`).Scan(&last); err != nil {
		return "", err
	}
	if last != nil && now.Sub(*last) < time.Duration(w.Rules.BaseIntervalMin)*time.Minute {
		return block(fmt.Sprintf("앞 게시와 기본 간격(%d분)을 지키려고 기다려요", w.Rules.BaseIntervalMin))
	}
	var runID string
	err = store.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		var contentID, revisionID string
		err := tx.QueryRow(ctx, `UPDATE reservations SET state = 'publishing', block_reason = NULL, updated_at = now()
			WHERE id = $1 AND state = 'assigned' RETURNING content_id, revision_id`, resID).Scan(&contentID, &revisionID)
		if store.IsNoRows(err) {
			return nil // 다른 인스턴스·사용자가 먼저 바꿨다 (취소·지금 게시)
		}
		if err != nil {
			return err
		}
		runID, err = s.createRun(ctx, tx, st, resID, contentID, revisionID, "schedule")
		return err
	})
	var ve *ValidationError
	switch {
	case errors.As(err, &ve):
		for _, v := range ve.Fields {
			_, _ = s.Pool.Exec(ctx, `UPDATE reservations SET state = 'waiting', wait_reason = $2 WHERE id = $1 AND state = 'assigned'`, resID, "수정 필요: "+v)
		}
		return "", nil
	case errors.Is(err, ErrBusy):
		return block("다른 묶음을 게시하는 중이라 끝나면 시작해요")
	case budget.IsExceeded(err):
		return block(err.Error())
	case err != nil:
		return "", err
	}
	if runID != "" {
		s.kick()
	}
	return runID, nil
}

// PostNowInput은 "지금 게시" 요청이다. 자동 시간표와 다른 수동 예외로 기록한다.
type PostNowInput struct {
	ContentID string `json:"content_id"`
	Confirm   bool   `json:"confirm"`
}

// PostNow는 글 하나를 지금 게시한다. 허용 구간·간격·하루 수량을 벗어나면 경고하고, 확인해야 진행한다.
// 그 글이 다른 예약에 있으면 그 예약에서 빼서 같은 글이 두 번 실행되지 않게 한다.
func (s *Service) PostNow(ctx context.Context, in PostNowInput) (string, error) {
	st, err := s.Settings.Get(ctx)
	if err != nil {
		return "", err
	}
	reason, err := s.blockReason(ctx, st, false)
	if err != nil {
		return "", err
	}
	if reason != "" {
		return "", &ValidationError{Fields: map[string]string{"content_id": reason}}
	}
	now := s.Clock.Now()
	w, err := s.Schedule.Window(ctx, now)
	if err != nil {
		return "", err
	}
	var warnings []string
	if !w.Allowed {
		warnings = append(warnings, w.Reason+" (수동 예외로 기록돼요)")
	}
	var last *time.Time
	_ = s.Pool.QueryRow(ctx, `SELECT max(body_started_at) FROM publish_runs WHERE body_started_at IS NOT NULL`).Scan(&last)
	if last != nil && now.Sub(*last) < time.Duration(w.Rules.BaseIntervalMin)*time.Minute {
		warnings = append(warnings, fmt.Sprintf("직전 게시와 %d분밖에 떨어지지 않았어요 (기본 간격 %d분)", int(now.Sub(*last).Minutes()), w.Rules.BaseIntervalMin))
	}
	if n, target, err := s.Schedule.TodayCount(ctx, now); err == nil && n >= target {
		warnings = append(warnings, fmt.Sprintf("오늘 이미 %d묶음을 게시했어요 (하루 목표 %d)", n, target))
	}
	if len(warnings) > 0 && !in.Confirm {
		return "", &WarningError{Warnings: warnings}
	}
	var runID string
	err = store.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		if err := schedule.LockTx(ctx, tx); err != nil {
			return err
		}
		var cur, appr *string
		var archived bool
		err := tx.QueryRow(ctx, `SELECT current_revision_id, approved_revision_id, archived_at IS NOT NULL FROM contents WHERE id = $1 FOR UPDATE`, in.ContentID).
			Scan(&cur, &appr, &archived)
		if store.IsNoRows(err) || archived {
			return &ValidationError{Fields: map[string]string{"content_id": "보관됐거나 없는 글이에요"}}
		}
		if err != nil {
			return err
		}
		rev := cur
		if st.OperationMode == settings.ModeReview {
			if appr == nil {
				return &ValidationError{Fields: map[string]string{"content_id": "검토 후 게시 모드에서는 승인한 글만 게시할 수 있어요"}}
			}
			rev = appr
		}
		var busy bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM reservations WHERE content_id = $1 AND state IN ('publishing', 'partial', 'unknown'))`, in.ContentID).Scan(&busy); err != nil {
			return err
		}
		if busy {
			return &ValidationError{Fields: map[string]string{"content_id": "이 글은 이미 게시 중이거나 결과 확인이 필요해요"}}
		}
		// 기존 예약에서 빼서 같은 글이 자동으로 한 번 더 실행되지 않게 한다 (그 예약은 빈 예약으로 남는다)
		if _, err := tx.Exec(ctx, `UPDATE reservations SET content_id = NULL, revision_id = NULL, state = 'open', wait_reason = NULL, block_reason = NULL,
			delay_note = '이 예약의 글은 지금 게시했어요', updated_at = now() WHERE content_id = $1 AND state IN ('open', 'assigned', 'waiting')`, in.ContentID); err != nil {
			return err
		}
		note := "지금 게시 (수동)"
		if len(warnings) > 0 {
			note = "지금 게시 (수동 예외: " + warnings[0] + ")"
		}
		var resID string
		if err := tx.QueryRow(ctx, `INSERT INTO reservations (slot_at, original_slot_at, local_date, content_id, revision_id, manual, pinned, state, delay_note)
			VALUES ($1, $1, $2::date, $3, $4, true, true, 'publishing', $5) RETURNING id`,
			now, now.In(w.Loc).Format("2006-01-02"), in.ContentID, rev, note).Scan(&resID); err != nil {
			return err
		}
		runID, err = s.createRun(ctx, tx, st, resID, in.ContentID, *rev, "manual")
		return err
	})
	if err != nil {
		return "", err
	}
	s.kick()
	return runID, nil
}

// ── 실행 ──────────────────────────────────────────────────────────────

type run struct {
	id, reservationID, contentID, trigger, state string
	slotAt                                       time.Time
}

type part struct {
	position     int
	text         string
	media        []string
	xMedia       []string
	mediaAt      *time.Time
	state        string
	postID       *string
	notBefore    *time.Time
	attempts     int
	madeWithAI   bool
	errorClass   *string
	intentAt     *time.Time
	succeededAt  *time.Time
	errorMessage *string
}

func (s *Service) loadRun(ctx context.Context, db store.DB, id string) (run, []part, error) {
	var r run
	err := db.QueryRow(ctx, `SELECT pr.id, pr.reservation_id, pr.content_id, pr.trigger, pr.state, rs.slot_at FROM publish_runs pr
		JOIN reservations rs ON rs.id = pr.reservation_id WHERE pr.id = $1`, id).Scan(&r.id, &r.reservationID, &r.contentID, &r.trigger, &r.state, &r.slotAt)
	if store.IsNoRows(err) {
		return r, nil, ErrNotFound
	}
	if err != nil {
		return r, nil, err
	}
	rows, err := db.Query(ctx, `SELECT position, text, media_ids, x_media_ids, x_media_uploaded_at, state, x_post_id, not_before, attempts, made_with_ai,
		error_class, intent_at, succeeded_at, error FROM publish_parts WHERE run_id = $1 ORDER BY position`, id)
	if err != nil {
		return r, nil, err
	}
	parts, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (part, error) {
		var p part
		err := row.Scan(&p.position, &p.text, &p.media, &p.xMedia, &p.mediaAt, &p.state, &p.postID, &p.notBefore, &p.attempts, &p.madeWithAI,
			&p.errorClass, &p.intentAt, &p.succeededAt, &p.errorMessage)
		return p, err
	})
	return r, parts, err
}

func (s *Service) attempt(ctx context.Context, kind, ref string, started time.Time, outcome string, err error) {
	var status int
	var msg, reqID *string
	var ie *integration.Error
	if errors.As(err, &ie) {
		status = ie.HTTPStatus
		m := ie.Message
		msg = &m
		if ie.RequestID != "" {
			reqID = &ie.RequestID
		}
	} else if err != nil {
		m := err.Error()
		msg = &m
	}
	_, _ = s.Pool.Exec(ctx, `INSERT INTO operation_attempts (kind, ref_type, ref_id, started_at, finished_at, outcome, http_status, request_id, error)
		VALUES ($1, 'publish_part', $2, $3, now(), $4, NULLIF($5, 0), $6, $7)`, kind, ref, started, outcome, status, reqID, msg)
}

func userMessage(err error) string {
	var ie *integration.Error
	if errors.As(err, &ie) {
		return ie.Message
	}
	return err.Error()
}

// HandleRun은 게시 실행 작업이다 (jobs.Handler). 부분을 순서대로, 이미 성공한 부분은 건너뛰며 보낸다.
func (s *Service) HandleRun(ctx context.Context, j *jobs.Job) jobs.Result {
	var payload struct {
		RunID string `json:"run_id"`
	}
	if err := j.Decode(&payload); err != nil {
		return jobs.Result{Outcome: jobs.Failed, Err: err, Class: "review"}
	}
	r, parts, err := s.loadRun(ctx, s.Pool, payload.RunID)
	if err != nil {
		return jobs.Result{Outcome: jobs.RetryLater, Err: err}
	}
	switch r.state {
	case "completed", "canceled", "failed", "unknown":
		return jobs.OK()
	}
	st, err := s.Settings.Get(ctx)
	if err != nil {
		return jobs.Result{Outcome: jobs.RetryLater, Err: err}
	}
	bodyDone := len(parts) > 0 && parts[0].state == "succeeded"
	if reason, err := s.blockReason(ctx, st, r.trigger == "schedule"); err != nil {
		return jobs.Result{Outcome: jobs.RetryLater, Err: err}
	} else if reason != "" {
		return s.blocked(ctx, r, parts, reason)
	}
	cred, err := s.Conn.Token(ctx, false)
	if err != nil {
		if integration.ClassOf(err) == integration.Retryable && !errors.Is(err, connection.ErrNotConnected) {
			return jobs.Result{Outcome: jobs.RetryLater, Err: err}
		}
		return s.blocked(ctx, r, parts, userMessage(err))
	}
	var prevID string
	for i := range parts {
		p := &parts[i]
		switch p.state {
		case "succeeded":
			prevID = *p.postID
			continue
		case "unknown":
			return s.finishUnknown(ctx, r, p, errors.New("이전 시도의 결과가 불명확해요"))
		case "sending":
			// 보내던 중에 작업이 끊겼다 (서버 재시작·lease 만료). 실제로 올라갔는지 모르므로 다시 보내지 않는다
			return s.finishUnknown(ctx, r, p, errors.New("보내는 중에 작업이 끊겨 결과를 알 수 없어요"))
		case "failed":
			return jobs.Result{Outcome: jobs.Failed, Err: errors.New(deref(p.errorMessage)), Class: deref(p.errorClass)}
		}
		now := s.Clock.Now()
		if p.notBefore != nil && now.Before(*p.notBefore) {
			return jobs.Result{Outcome: jobs.Snoozed, At: *p.notBefore, Err: errors.New("답글 간격")}
		}
		// 허용 시간대: 예약 게시는 매 요청마다 확인한다. 수동 게시는 사용자가 확인했다
		if r.trigger == "schedule" {
			w, err := s.Schedule.Window(ctx, now)
			if err != nil {
				return jobs.Result{Outcome: jobs.RetryLater, Err: err}
			}
			if !w.Allowed {
				if p.position == 0 {
					return s.blocked(ctx, r, parts, w.Reason)
				}
				if w.NextStart.IsZero() {
					return jobs.Result{Outcome: jobs.Snoozed, At: now.Add(time.Hour), Err: errors.New("남은 답글: 다음 허용 구간 없음")}
				}
				_, _ = s.Pool.Exec(ctx, `UPDATE publish_runs SET note = $2, updated_at = now() WHERE id = $1`, r.id,
					fmt.Sprintf("남은 답글 예약됨: %s에 이어서 올려요", w.NextStart.In(w.Loc).Format("1/2 15:04")))
				return jobs.Result{Outcome: jobs.Snoozed, At: w.NextStart, Err: errors.New("남은 답글은 다음 허용 구간에")}
			}
		}
		// 이미지 업로드 (먼저 올리고 media_id를 게시에 붙인다)
		if len(p.media) > 0 && (len(p.xMedia) != len(p.media) || p.mediaAt == nil || now.Sub(*p.mediaAt) > mediaIDMaxAge) {
			if !cred.MediaWrite {
				return s.finishFailed(ctx, r, p, &integration.Error{Class: integration.Config, Provider: "x", Op: "media",
					Message: "이미지 업로드 권한(media.write)이 없어요. X를 다시 연결해 주세요"}, bodyDone)
			}
			ids, res := s.uploadMedia(ctx, j, r, p, cred)
			if res != nil {
				if res.Outcome == jobs.Failed {
					return s.finishFailed(ctx, r, p, res.Err, bodyDone)
				}
				return *res
			}
			p.xMedia = ids
		}
		if ok, err := s.Queue.StillOwner(ctx, j); err != nil || !ok {
			return jobs.Result{Outcome: jobs.RetryLater, Err: errors.New("작업 소유권을 잃어 새 요청을 시작하지 않아요")}
		}
		// 전송 의도를 먼저 남긴다. 이 뒤로 끊기면 결과 불명확으로 다룬다
		tag, err := s.Pool.Exec(ctx, `UPDATE publish_parts SET state = 'sending', intent_at = $3, attempts = attempts + 1, in_reply_to = NULLIF($4, '')
			WHERE run_id = $1 AND position = $2 AND state = 'pending'`, r.id, p.position, now, prevID)
		if err != nil || tag.RowsAffected() == 0 {
			return jobs.Result{Outcome: jobs.RetryLater, Err: fmt.Errorf("의도 기록 실패: %v", err)}
		}
		if p.position == 0 {
			if _, err := s.Pool.Exec(ctx, `UPDATE publish_runs SET state = 'running', started_at = COALESCE(started_at, $2), body_started_at = $2, note = NULL, updated_at = now() WHERE id = $1`,
				r.id, now); err != nil {
				return jobs.Result{Outcome: jobs.RetryLater, Err: err}
			}
		}
		req := xapi.PostRequest{Text: p.text, MediaIDs: p.xMedia, ReplyToID: prevID, MadeWithAI: p.madeWithAI && len(p.xMedia) > 0}
		post, err := s.Conn.Client().CreatePost(ctx, cred.AccessToken, req)
		if err != nil && integration.ClassOf(err) == integration.Config {
			var ie *integration.Error
			if errors.As(err, &ie) && ie.HTTPStatus == 401 {
				// 401은 게시되지 않은 응답이다: 토큰을 한 번 갱신해 다시 보낸다
				if c2, rerr := s.Conn.Token(ctx, true); rerr == nil {
					cred = c2
					post, err = s.Conn.Client().CreatePost(ctx, cred.AccessToken, req)
				}
			}
		}
		ref := fmt.Sprintf("%s:%d", r.id, p.position)
		if err != nil {
			class := integration.ClassOf(err)
			switch {
			case class == integration.Unknown:
				s.attempt(ctx, "x.post", ref, now, "unknown", err)
				return s.finishUnknown(ctx, r, p, err)
			case class == integration.Retryable && j.Attempts < j.MaxAttempts:
				s.attempt(ctx, "x.post", ref, now, "error", err)
				// 보내지 못한 요청(연결 실패·429): 되돌려 다시 시도한다
				_, _ = s.Pool.Exec(ctx, `UPDATE publish_parts SET state = 'pending', error = $3, error_class = 'retryable' WHERE run_id = $1 AND position = $2 AND state = 'sending'`,
					r.id, p.position, userMessage(err))
				at := s.Clock.Now().Add(jobs.Backoff(j.Attempts, 30*time.Second, 15*time.Minute))
				var ie *integration.Error
				if errors.As(err, &ie) && ie.RetryAfter > 0 {
					at = s.Clock.Now().Add(ie.RetryAfter)
				}
				return jobs.Result{Outcome: jobs.RetryLater, Err: err, At: at}
			default:
				s.attempt(ctx, "x.post", ref, now, "error", err)
				return s.finishFailed(ctx, r, p, err, bodyDone)
			}
		}
		s.attempt(ctx, "x.post", ref, now, "success", nil)
		url := xapi.PostURL(cred.Username, post.ID)
		if _, err := s.Pool.Exec(ctx, `UPDATE publish_parts SET state = 'succeeded', x_post_id = $3, x_url = $4, succeeded_at = $5, error = NULL, error_class = NULL
			WHERE run_id = $1 AND position = $2 AND state IN ('sending', 'unknown')`, r.id, p.position, post.ID, url, s.Clock.Now()); err != nil {
			// 성공 ID를 저장하지 못하면 다시 보내면 안 된다: 불명확으로 둔다
			s.Log.Error("failed to store x post id", "run", r.id, "position", p.position, "post_id", post.ID)
			return jobs.Result{Outcome: jobs.Unknown, Err: err}
		}
		_ = s.Budget.Record(ctx, budget.Entry{Category: budget.XPost, RefKey: ref, Units: map[string]any{"posts": 1, "url": thread.HasURL(p.text)},
			Micros: budget.PostMicros(st.Policy.Pricing, thread.HasURL(p.text))}, "")
		_ = s.Budget.Confirm(ctx, budget.XPost, ref) // 앞선 불명확 기록이 있었다면 (중복 위험을 감수한 재시도) 확정으로 바꾼다
		if len(p.media) > 0 {
			_, _ = s.Pool.Exec(ctx, `UPDATE media_assets SET last_published_at = $2, retention_anchor_at = $2 WHERE id = ANY($1)`, p.media, s.Clock.Now())
		}
		p.state, p.postID, prevID = "succeeded", &post.ID, post.ID
		if p.position == 0 {
			bodyDone = true
			if _, err := s.Schedule.AfterBodyStart(ctx, r.reservationID, now); err != nil {
				s.Log.Warn("shift following slots failed", "err", err.Error())
			}
		}
		// 다음 답글은 설정한 간격 뒤에 (본문 성공을 확인한 다음)
		if i+1 < len(parts) {
			w, _ := s.Schedule.Window(ctx, now)
			if gap := time.Duration(w.Rules.ReplyGapSec) * time.Second; gap > 0 {
				nb := s.Clock.Now().Add(gap)
				_, _ = s.Pool.Exec(ctx, `UPDATE publish_parts SET not_before = $3 WHERE run_id = $1 AND position = $2`, r.id, parts[i+1].position, nb)
				return jobs.Result{Outcome: jobs.Snoozed, At: nb, Err: errors.New("답글 간격")}
			}
		}
	}
	return s.finishCompleted(ctx, r)
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (s *Service) uploadMedia(ctx context.Context, j *jobs.Job, r run, p *part, cred connection.Credentials) ([]string, *jobs.Result) {
	var ids []string
	for _, mid := range p.media {
		data, ct, err := s.Media.File(ctx, mid, false)
		if err != nil {
			return nil, &jobs.Result{Outcome: jobs.Failed, Err: &integration.Error{Class: integration.Review, Provider: "app", Op: "media",
				Message: "첨부 이미지를 읽을 수 없어요 (정리됐거나 저장소 문제): " + err.Error()}}
		}
		started := s.Clock.Now()
		id, err := s.Conn.Client().UploadImage(ctx, cred.AccessToken, data, ct)
		ref := fmt.Sprintf("%s:%d:%s", r.id, p.position, mid)
		if err != nil {
			s.attempt(ctx, "x.media", ref, started, "error", err)
			if integration.ClassOf(err) == integration.Retryable || integration.ClassOf(err) == integration.Unknown {
				if j.Attempts < j.MaxAttempts {
					return nil, &jobs.Result{Outcome: jobs.RetryLater, Err: err, At: s.Clock.Now().Add(jobs.Backoff(j.Attempts, 30*time.Second, 15*time.Minute))}
				}
			}
			return nil, &jobs.Result{Outcome: jobs.Failed, Err: err}
		}
		s.attempt(ctx, "x.media", ref, started, "success", nil)
		ids = append(ids, id)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE publish_parts SET x_media_ids = $3, x_media_uploaded_at = $4 WHERE run_id = $1 AND position = $2`,
		r.id, p.position, ids, s.Clock.Now()); err != nil {
		return nil, &jobs.Result{Outcome: jobs.RetryLater, Err: err}
	}
	return ids, nil
}

// blocked는 시작 조건이 안 맞을 때다. 아무것도 보내지 않았으면 실행을 취소하고 예약을 준비 완료로 돌려 다시 시작할 수 있게 한다.
// 본문이 이미 올라갔으면 남은 답글을 기다린다.
func (s *Service) blocked(ctx context.Context, r run, parts []part, reason string) jobs.Result {
	sent := false
	for _, p := range parts {
		if p.state != "pending" {
			sent = true
		}
	}
	if !sent {
		_ = store.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `UPDATE publish_runs SET state = 'canceled', note = $2, updated_at = now() WHERE id = $1 AND state IN ('pending', 'running')`, r.id, reason); err != nil {
				return err
			}
			newState := "assigned"
			if r.trigger == "manual" {
				newState = "canceled" // 지금 게시는 다시 자동으로 시작하지 않는다
			}
			_, err := tx.Exec(ctx, `UPDATE reservations SET state = $2, block_reason = $3, updated_at = now() WHERE id = $1 AND state = 'publishing'`, r.reservationID, newState, reason)
			return err
		})
		_ = s.Budget.Release(ctx, "post:"+r.id)
		return jobs.Result{Outcome: jobs.Failed, Err: errors.New(reason), Class: "config"}
	}
	_, _ = s.Pool.Exec(ctx, `UPDATE publish_runs SET note = $2, updated_at = now() WHERE id = $1`, r.id, "남은 답글 대기: "+reason)
	_, _ = s.Pool.Exec(ctx, `UPDATE reservations SET block_reason = $2 WHERE id = $1`, r.reservationID, reason)
	return jobs.Result{Outcome: jobs.Snoozed, At: s.Clock.Now().Add(2 * time.Minute), Err: errors.New(reason)}
}

func (s *Service) finishUnknown(ctx context.Context, r run, p *part, err error) jobs.Result {
	msg := userMessage(err)
	_ = store.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE publish_parts SET state = 'unknown', error = $3, error_class = 'unknown' WHERE run_id = $1 AND position = $2`, r.id, p.position, msg); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE publish_runs SET state = 'unknown', note = $2, updated_at = now() WHERE id = $1`, r.id, msg); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE reservations SET state = 'unknown', updated_at = now() WHERE id = $1`, r.reservationID)
		return err
	})
	// 과금됐는지 모르므로 보수적으로 추정 비용을 '불명확'으로 기록한다 (사용자가 확인하면 확정·삭제)
	var micros int64
	if st, err := s.Settings.Get(ctx); err == nil {
		micros = budget.PostMicros(st.Policy.Pricing, thread.HasURL(p.text))
	}
	_ = s.Budget.Record(ctx, budget.Entry{Category: budget.XPost, RefKey: fmt.Sprintf("%s:%d", r.id, p.position), Uncertain: true,
		Micros: micros, Units: map[string]any{"posts": 1, "uncertain": true}}, "")
	label := "본문"
	if p.position > 0 {
		label = fmt.Sprintf("답글 %d", p.position)
	}
	if s.Notify != nil {
		_ = s.Notify.Notify(ctx, notification.New{Severity: notification.Critical, Category: "publish", GroupKey: "publish:unknown:" + r.id,
			Title: "게시 결과 확인이 필요해요", Body: fmt.Sprintf("%s을(를) 보냈지만 결과를 알 수 없어요. 중복 게시를 막으려고 다시 보내지 않았어요. X에서 확인한 뒤 처리해 주세요: %s", label, msg),
			Link: "/publish"})
	}
	return jobs.Result{Outcome: jobs.Unknown, Err: err}
}

func (s *Service) finishFailed(ctx context.Context, r run, p *part, err error, bodyDone bool) jobs.Result {
	msg := userMessage(err)
	class := string(integration.ClassOf(err))
	runState, resState := "failed", "failed"
	if bodyDone {
		runState, resState = "partial", "partial"
	}
	_ = store.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE publish_parts SET state = 'failed', error = $3, error_class = $4 WHERE run_id = $1 AND position = $2`, r.id, p.position, msg, class); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE publish_runs SET state = $2, note = $3, updated_at = now() WHERE id = $1`, r.id, runState, msg); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE reservations SET state = $2, updated_at = now() WHERE id = $1`, r.reservationID, resState)
		return err
	})
	if !bodyDone {
		_ = s.Budget.Release(ctx, "post:"+r.id)
	}
	if s.Notify != nil {
		title := "게시하지 못했어요"
		body := "본문을 올리지 못했어요: " + msg
		if bodyDone {
			title = "본문은 올라갔지만 답글이 실패했어요"
			body = fmt.Sprintf("답글 %d을(를) 올리지 못했어요: %s. 본문은 다시 올리지 않고, 확인 후 답글부터 다시 시도할 수 있어요", p.position, msg)
		}
		_ = s.Notify.Notify(ctx, notification.New{Severity: notification.Warning, Category: "publish", GroupKey: "publish:failed:" + r.id, Title: title, Body: body, Link: "/publish"})
	}
	return jobs.Result{Outcome: jobs.Failed, Err: err, Class: class}
}

func (s *Service) finishCompleted(ctx context.Context, r run) jobs.Result {
	err := store.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE publish_runs SET state = 'completed', completed_at = $2, note = NULL, updated_at = now() WHERE id = $1`, r.id, s.Clock.Now()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE reservations SET state = 'published', block_reason = NULL, updated_at = now() WHERE id = $1`, r.reservationID)
		return err
	})
	if err != nil {
		return jobs.Result{Outcome: jobs.RetryLater, Err: err}
	}
	_ = s.Budget.Settle(ctx, "post:"+r.id)
	if s.Notify != nil {
		_ = s.Notify.Resolve(ctx, "publish:blocked")
	}
	return jobs.OK()
}

var statusIDRe = regexp.MustCompile(`(?:status(?:es)?/|^)(\d{5,25})(?:\D|$)`)

// ParsePostRef는 X 게시물 주소나 숫자 ID에서 ID를 꺼낸다.
func ParsePostRef(s string) (string, bool) {
	m := statusIDRe.FindStringSubmatch(s)
	if m == nil {
		return "", false
	}
	return m[1], true
}
