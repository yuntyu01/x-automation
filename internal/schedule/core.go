package schedule

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yuntyu01/x-automation/internal/generation"
	"github.com/yuntyu01/x-automation/internal/notification"
	"github.com/yuntyu01/x-automation/internal/settings"
	"github.com/yuntyu01/x-automation/internal/store"
)

// activeStates는 시각을 차지하고 하루 수량에 들어가는 예약 상태다 (취소·실패 제외).
const activeStates = `('open', 'assigned', 'waiting', 'publishing', 'partial', 'unknown', 'published')`

// pendingStates는 아직 실행하지 않아 바꿀 수 있는 예약 상태다.
const pendingStates = `('open', 'assigned', 'waiting')`

func countsByDate(ctx context.Context, db store.DB, from string) (map[string]int, error) {
	rows, err := db.Query(ctx, `SELECT to_char(local_date, 'YYYY-MM-DD'), count(*) FROM reservations
		WHERE state IN `+activeStates+` AND local_date >= $1::date GROUP BY 1`, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var d string
		var n int
		if err := rows.Scan(&d, &n); err != nil {
			return nil, err
		}
		out[d] = n
	}
	return out, rows.Err()
}

func insertSlots(ctx context.Context, tx pgx.Tx, slots []Slot, ruleVersion int) ([]string, error) {
	ids := make([]string, 0, len(slots))
	for _, sl := range slots {
		var id string
		if err := tx.QueryRow(ctx, `INSERT INTO reservations (slot_at, original_slot_at, jitter_seconds, local_date, rule_version)
			VALUES ($1, $1, $2, $3::date, $4) RETURNING id`, sl.At, sl.JitterSec, sl.LocalDate, ruleVersion).Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// Ensure는 마지막 예약 뒤로 확보 기간 끝까지 새 예약을 만든다. 이미 있는 예약의 시각은 바꾸지 않는다 (재시작해도 같다).
func (s *Service) Ensure(ctx context.Context) (int, error) {
	n := 0
	err := store.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.lock(ctx, tx); err != nil {
			return err
		}
		c, err := s.load(ctx, tx)
		if err != nil {
			return err
		}
		now := s.clock.Now()
		until := c.horizonEnd(now)
		// 기준점: 시간 고정이 아닌 가장 늦은 예약(실행된 것 포함). 그 뒤의 고정 예약은 간격 계산에만 쓴다
		var anchor *time.Time
		if err := tx.QueryRow(ctx, `SELECT max(slot_at) FROM reservations WHERE state IN `+activeStates+` AND NOT pinned`).Scan(&anchor); err != nil {
			return err
		}
		var prev time.Time
		if anchor != nil {
			prev = *anchor
		}
		pins, err := pinnedAfter(ctx, tx, prev)
		if err != nil {
			return err
		}
		counts, err := countsByDate(ctx, tx, now.In(c.loc).AddDate(0, 0, -1).Format(dateLayout))
		if err != nil {
			return err
		}
		earliest := now.Add(lead)
		if prev.After(earliest) {
			earliest = prev
		}
		slots := Generate(Params{Rules: c.rules, Loc: c.loc, Intervals: Intervals(c.windows, c.loc, now.Add(-24*time.Hour), until),
			Excluded: c.excluded, Counts: counts, Prev: prev, Pinned: pins, Earliest: earliest, Until: until, Rand: s.rand})
		ids, err := insertSlots(ctx, tx, slots, c.rules.PolicyVersion)
		n = len(ids)
		return err
	})
	return n, err
}

func pinnedAfter(ctx context.Context, db store.DB, t time.Time) ([]time.Time, error) {
	rows, err := db.Query(ctx, `SELECT slot_at FROM reservations WHERE state IN `+activeStates+` AND pinned AND slot_at > $1 ORDER BY slot_at`, t)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[time.Time])
}

// Tick은 1분마다 실행한다: 지난 예약 정리 → 예약 채우기 → 준비된 글 배치 → 상태·대기 사유 갱신.
func (s *Service) Tick(ctx context.Context) error {
	if err := s.CloseOverdue(ctx); err != nil {
		return fmt.Errorf("close overdue: %w", err)
	}
	if _, err := s.Ensure(ctx); err != nil {
		return fmt.Errorf("ensure: %w", err)
	}
	if _, err := s.AutoAssign(ctx); err != nil {
		return fmt.Errorf("auto assign: %w", err)
	}
	return s.Refresh(ctx)
}

// ── 준비 상태 ─────────────────────────────────────────────────────────

type pending struct {
	id          string
	slotAt      time.Time
	state       string
	contentID   *string
	revisionID  *string
	archived    bool
	genState    *string
	currentRev  *string
	approvedRev *string
	publishable *bool
	problem     *string
	boundGen    bool
	block       *string // 게시를 막은 조건 (publish가 기록)
}

func loadPending(ctx context.Context, db store.DB, review bool, until time.Time) ([]pending, error) {
	rows, err := db.Query(ctx, `SELECT r.id, r.slot_at, r.state, r.content_id, r.revision_id,
		c.archived_at IS NOT NULL, c.generation_state, c.current_revision_id, c.approved_revision_id,
		rv.publishable, rv.validation->'problems'->>0,
		EXISTS (SELECT 1 FROM generation_requests g WHERE g.reservation_id = r.id AND g.state IN ('queued', 'running')), r.block_reason
		FROM reservations r
		LEFT JOIN contents c ON c.id = r.content_id
		LEFT JOIN content_revisions rv ON rv.id = CASE WHEN $1 THEN c.approved_revision_id ELSE c.current_revision_id END
		WHERE r.state IN `+pendingStates+` AND r.slot_at < $2 ORDER BY r.slot_at, r.id`, review, until)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []pending
	for rows.Next() {
		var p pending
		var archived *bool
		if err := rows.Scan(&p.id, &p.slotAt, &p.state, &p.contentID, &p.revisionID, &archived, &p.genState, &p.currentRev, &p.approvedRev,
			&p.publishable, &p.problem, &p.boundGen, &p.block); err != nil {
			return nil, err
		}
		p.archived = archived != nil && *archived
		out = append(out, p)
	}
	return out, rows.Err()
}

// readiness는 예약의 다음 상태·대기 사유·게시할 revision이다.
func (p pending) readiness(review bool) (state string, reason *string, rev *string) {
	r := func(s string) *string { return &s }
	switch {
	case p.contentID == nil:
		if p.boundGen {
			return "open", r("생성 중"), nil
		}
		return "open", r("준비된 글 없음"), nil
	case p.genState != nil && (*p.genState == "queued" || *p.genState == "running"):
		return "waiting", r("생성 중"), p.revisionID
	case review && p.approvedRev == nil:
		return "waiting", r("검토 대기"), p.currentRev
	case p.publishable == nil || !*p.publishable:
		msg := "수정 필요"
		if p.problem != nil && *p.problem != "" {
			msg = "수정 필요: " + *p.problem
		}
		if review {
			return "waiting", r(msg), p.approvedRev
		}
		return "waiting", r(msg), p.currentRev
	}
	if review {
		return "assigned", nil, p.approvedRev // 승인 이후 수정분은 게시하지 않는다
	}
	return "assigned", nil, p.currentRev
}

// Refresh는 다가오는 예약의 상태·대기 사유·게시할 revision·생성 예정 시각을 맞춘다. 보관된 글은 예약에서 뺀다.
// 미준비 대체를 켰으면 곧 실행할 예약의 미준비 글을 준비된 글로 바꾼다.
func (s *Service) Refresh(ctx context.Context) error {
	return store.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.lock(ctx, tx); err != nil {
			return err
		}
		c, err := s.load(ctx, tx)
		if err != nil {
			return err
		}
		review := c.st.OperationMode == settings.ModeReview
		now := s.clock.Now()
		items, err := loadPending(ctx, tx, review, c.horizonEnd(now).AddDate(0, 0, 7))
		if err != nil {
			return err
		}
		var pool []poolItem
		if c.rules.SubstituteUnready {
			if pool, err = readyPool(ctx, tx); err != nil {
				return err
			}
		}
		for _, p := range items {
			if p.contentID != nil && p.archived {
				if err := release(ctx, tx, p.id, "보관된 글이라 예약에서 뺐어요"); err != nil {
					return err
				}
				continue
			}
			state, reason, rev := p.readiness(review)
			if state == "waiting" && len(pool) > 0 && !p.slotAt.After(now.Add(lead)) && (reason == nil || *reason != "생성 중") {
				// 사용자가 고른 경우만: 곧 실행할 예약의 미준비 글을 준비된 다른 글로 바꾼다 (원래 글은 보관함에 남는다)
				if err := release(ctx, tx, p.id, ""); err != nil {
					return err
				}
				if err := assign(ctx, tx, p.id, pool[0].id, review); err != nil {
					return err
				}
				pool = pool[1:]
				_, _ = tx.Exec(ctx, `UPDATE reservations SET delay_note = '준비되지 않은 글 대신 준비된 글로 바꿨어요' WHERE id = $1`, p.id)
				continue
			}
			var due *time.Time
			if p.contentID == nil {
				due = generation.DueAt(c.st, p.slotAt)
			}
			if _, err := tx.Exec(ctx, `UPDATE reservations SET state = $2, wait_reason = $3, revision_id = COALESCE($4, revision_id),
				generation_due_at = $5, updated_at = now()
				WHERE id = $1 AND state IN `+pendingStates+` AND (state IS DISTINCT FROM $2 OR wait_reason IS DISTINCT FROM $3
				OR ($4::uuid IS NOT NULL AND revision_id IS DISTINCT FROM $4) OR generation_due_at IS DISTINCT FROM $5)`,
				p.id, state, reason, rev, due); err != nil {
				return err
			}
		}
		return nil
	})
}

// ── 배치 ──────────────────────────────────────────────────────────────

type poolItem struct{ id string }

// readyPool은 자동 배치할 수 있는 준비 완료 글이다 (오래된 순, 예시·사용자가 뺀 글 제외).
func readyPool(ctx context.Context, db store.DB) ([]poolItem, error) {
	rows, err := db.Query(ctx, `SELECT c.id FROM content_overview o JOIN contents c ON c.id = o.id
		WHERE o.stage = 'ready' AND NOT o.is_demo AND NOT c.schedule_hold ORDER BY c.created_at, c.id`)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	out := make([]poolItem, len(ids))
	for i, id := range ids {
		out[i] = poolItem{id: id}
	}
	return out, err
}

// assign은 콘텐츠를 예약에 넣는다. 다른 진행 전 예약에 있던 같은 글은 그 예약에서 뺀다 (한 글은 한 예약에만).
func assign(ctx context.Context, tx pgx.Tx, reservationID, contentID string, review bool) error {
	var cur, appr *string
	err := tx.QueryRow(ctx, `SELECT current_revision_id, approved_revision_id FROM contents WHERE id = $1 AND archived_at IS NULL FOR UPDATE`, contentID).
		Scan(&cur, &appr)
	if store.IsNoRows(err) {
		return &ValidationError{Fields: map[string]string{"content_id": "보관됐거나 없는 글이에요"}}
	}
	if err != nil {
		return err
	}
	var busy bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM reservations WHERE content_id = $1 AND state IN ('publishing', 'partial', 'unknown'))`, contentID).Scan(&busy); err != nil {
		return err
	}
	if busy {
		return &ValidationError{Fields: map[string]string{"content_id": "이 글은 지금 게시 중이거나 확인이 필요한 상태예요"}}
	}
	if _, err := tx.Exec(ctx, `UPDATE reservations SET content_id = NULL, revision_id = NULL, state = 'open', wait_reason = NULL, version = version + 1, updated_at = now()
		WHERE content_id = $1 AND state IN `+pendingStates+` AND id <> $2`, contentID, reservationID); err != nil {
		return err
	}
	rev := cur
	if review && appr != nil {
		rev = appr
	}
	tag, err := tx.Exec(ctx, `UPDATE reservations SET content_id = $2, revision_id = $3, state = 'waiting', wait_reason = NULL, delay_note = NULL,
		block_reason = NULL, version = version + 1, updated_at = now() WHERE id = $1 AND state IN `+pendingStates, reservationID, contentID, rev)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotEditable
	}
	_, err = tx.Exec(ctx, `UPDATE contents SET schedule_hold = false WHERE id = $1`, contentID)
	return err
}

// release는 예약에서 콘텐츠를 뺀다 (예약은 빈 예약으로 남는다).
func release(ctx context.Context, tx pgx.Tx, reservationID, note string) error {
	_, err := tx.Exec(ctx, `UPDATE reservations SET content_id = NULL, revision_id = NULL, state = 'open', wait_reason = NULL, block_reason = NULL,
		delay_note = NULLIF($2, ''), version = version + 1, updated_at = now() WHERE id = $1 AND state IN `+pendingStates, reservationID, note)
	return err
}

// nextOpen은 now+lead 뒤의 가장 이른 빈 예약이다. after가 있으면 그 시각 뒤에서 찾는다.
func nextOpen(ctx context.Context, tx pgx.Tx, earliest time.Time, localDate string) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `SELECT id FROM reservations WHERE state = 'open' AND content_id IS NULL AND slot_at > $1
		AND ($2 = '' OR local_date = $2::date) ORDER BY slot_at LIMIT 1 FOR UPDATE`, earliest, localDate).Scan(&id)
	if store.IsNoRows(err) {
		return "", nil
	}
	return id, err
}

// AutoAssign은 빈 예약에 준비 완료 글을 오래된 순으로 넣는다 (기존 자료 우선).
func (s *Service) AutoAssign(ctx context.Context) (int, error) {
	n := 0
	err := store.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.lock(ctx, tx); err != nil {
			return err
		}
		st, err := s.settings.Get(ctx)
		if err != nil {
			return err
		}
		pool, err := readyPool(ctx, tx)
		if err != nil || len(pool) == 0 {
			return err
		}
		earliest := s.clock.Now().Add(lead)
		for _, p := range pool {
			id, err := nextOpen(ctx, tx, earliest, "")
			if err != nil || id == "" {
				return err
			}
			if err := assign(ctx, tx, id, p.id, st.OperationMode == settings.ModeReview); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}

// AssignToReservation은 특정 예약을 위해 만든 글을 그 예약에 넣는다 (generation.Assigner).
// 그사이 예약이 다른 글로 채워졌거나 닫혔으면 넣지 않는다 — 글은 보관함에 남아 다음 빈 예약에 배치된다.
func (s *Service) AssignToReservation(ctx context.Context, reservationID, contentID string) error {
	return store.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.lock(ctx, tx); err != nil {
			return err
		}
		var empty bool
		err := tx.QueryRow(ctx, `SELECT content_id IS NULL FROM reservations WHERE id = $1 AND state IN `+pendingStates+` FOR UPDATE`, reservationID).Scan(&empty)
		if store.IsNoRows(err) || (err == nil && !empty) {
			return errors.New("예약이 이미 채워졌거나 닫혔어요")
		}
		if err != nil {
			return err
		}
		st, err := s.settings.Get(ctx)
		if err != nil {
			return err
		}
		if err := assign(ctx, tx, reservationID, contentID, st.OperationMode == settings.ModeReview); err != nil {
			return err
		}
		// 게시할 때 생성: 예약 시각이 지난 뒤 생성이 끝났으면 지금으로 옮겨 바로 게시한다 (원래 시각은 original_slot_at에 남는다).
		// 그대로 두면 유예 시간이 지난 예약으로 보고 취소한다
		now := s.clock.Now()
		_, err = tx.Exec(ctx, `UPDATE reservations SET slot_at = $2, updated_at = now() WHERE id = $1 AND slot_at < $2
			AND NOT EXISTS (SELECT 1 FROM reservations o WHERE o.slot_at = $2 AND o.id <> $1
				AND o.state IN ('open', 'assigned', 'waiting', 'publishing', 'partial', 'unknown', 'published'))`, reservationID, now)
		return err
	})
}

// PlaceInNextOpen은 글을 가장 이른 빈 예약에 넣는다 (generation.Assigner, "생성 후 빈 예약에 배치").
func (s *Service) PlaceInNextOpen(ctx context.Context, contentID string) error {
	return store.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.lock(ctx, tx); err != nil {
			return err
		}
		st, err := s.settings.Get(ctx)
		if err != nil {
			return err
		}
		id, err := nextOpen(ctx, tx, s.clock.Now().Add(lead), "")
		if err != nil {
			return err
		}
		if id == "" {
			return errors.New("빈 예약이 없어요")
		}
		return assign(ctx, tx, id, contentID, st.OperationMode == settings.ModeReview)
	})
}

// ── 지난 예약 ─────────────────────────────────────────────────────────

// CloseOverdue는 유예 시간이 지나도록 실행되지 않은 예약을 닫는다 (서버 중단·자동 운영 꺼짐·미준비).
// 밀린 예약을 한꺼번에 게시하지 않는다. 준비된 글은 다음 빈 예약으로 옮기고, 준비 안 된 글은 보관함으로 돌려보낸다.
// 예약 시각 생성(게시할 때 생성) 중이고 아직 허용 구간 안이면 생성이 끝날 때까지 기다린다.
func (s *Service) CloseOverdue(ctx context.Context) error {
	closed := 0
	err := store.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.lock(ctx, tx); err != nil {
			return err
		}
		c, err := s.load(ctx, tx)
		if err != nil {
			return err
		}
		now := s.clock.Now()
		review := c.st.OperationMode == settings.ModeReview
		items, err := loadPending(ctx, tx, review, now.Add(-overdueGrace))
		if err != nil {
			return err
		}
		ivs := Intervals(c.windows, c.loc, now.Add(-48*time.Hour), now.Add(48*time.Hour))
		allowedNow, _ := Allowed(now, c.rules, ivs, c.excluded, c.loc)
		for _, p := range items {
			state, reason, _ := p.readiness(review)
			if reason != nil && *reason == "생성 중" && allowedNow && !p.archived {
				_, err := tx.Exec(ctx, `UPDATE reservations SET state = 'waiting', wait_reason = '생성 중',
					delay_note = '생성이 끝나면 허용 구간 안에서 바로 게시해요' WHERE id = $1 AND state IN `+pendingStates, p.id)
				if err != nil {
					return err
				}
				continue
			}
			var note string
			switch {
			case !c.st.AutomationEnabled:
				note = "자동 운영이 꺼져 있는 동안 지나간 예약이에요"
			case c.st.PublishingPaused:
				note = "게시 일시정지 중에 지나간 예약이에요"
			case p.contentID == nil:
				note = "준비된 글이 없어 건너뛰었어요"
			case state != "assigned":
				note = fmt.Sprintf("%s 상태라 게시하지 않고 건너뛰었어요", *reason)
			case p.block != nil:
				note = fmt.Sprintf("%s — 게시하지 못하고 지나갔어요. 글은 다음 빈 예약으로 옮겨요", *p.block)
			default:
				note = "실행되지 못하고 지나간 예약이에요 (서버 중단 등). 글은 다음 빈 예약으로 옮겨요"
			}
			tag, err := tx.Exec(ctx, `UPDATE reservations SET state = 'canceled', wait_reason = $2, delay_note = $3, updated_at = now()
				WHERE id = $1 AND state IN `+pendingStates, p.id, reason, note)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				continue
			}
			closed++
			// 준비된 글은 다음 빈 예약으로 바로 옮긴다 (다음 날이면 그날 수량 안에서)
			if p.contentID != nil && state == "assigned" && !p.archived {
				id, err := nextOpen(ctx, tx, now.Add(lead), "")
				if err != nil {
					return err
				}
				if id != "" {
					if err := assign(ctx, tx, id, *p.contentID, review); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
	if err == nil && closed > 0 && s.notify != nil {
		st, _ := s.settings.Get(ctx)
		if st.AutomationEnabled && !st.PublishingPaused {
			_ = s.notify.Notify(ctx, notification.New{Severity: notification.Warning, Category: "schedule", GroupKey: "schedule:missed",
				Title: "게시하지 못하고 지나간 예약이 있어요", Body: fmt.Sprintf("%d개 예약이 실행되지 않고 지나 건너뛰었어요. 준비된 글은 다음 빈 예약으로 옮겼어요", closed),
				Link: "/schedule"})
		}
	}
	return err
}

// ── 실제 게시 뒤 간격 보장 ────────────────────────────────────────────

// AfterBodyStart는 본문 게시를 실제로 시작한 시각을 받아, 뒤의 미실행 예약이 "앞 게시 + 기본 간격 + 저장된 지연" 이상 떨어지게 늦춘다.
// 랜덤을 새로 뽑지 않고, 원래 시각(original_slot_at)과 사유를 남긴다. 고정 예약은 옮기지 않는다.
// 옮긴 결과 다른 날짜가 되고 그날 수량이 찼으면 그 예약은 건너뛴다 (글은 다음 빈 예약으로).
func (s *Service) AfterBodyStart(ctx context.Context, reservationID string, at time.Time) (int, error) {
	moved := 0
	err := store.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.lock(ctx, tx); err != nil {
			return err
		}
		c, err := s.load(ctx, tx)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id, slot_at, jitter_seconds, pinned, to_char(local_date, 'YYYY-MM-DD') FROM reservations
			WHERE state IN `+pendingStates+` AND id <> $1 AND slot_at >= (SELECT slot_at FROM reservations WHERE id = $1)
			ORDER BY slot_at, id FOR UPDATE`, reservationID)
		if err != nil {
			return err
		}
		type row struct {
			id     string
			slot   time.Time
			jitter int
			pinned bool
			date   string
		}
		list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
			var x row
			err := r.Scan(&x.id, &x.slot, &x.jitter, &x.pinned, &x.date)
			return x, err
		})
		if err != nil {
			return err
		}
		counts, err := countsByDate(ctx, tx, at.In(c.loc).AddDate(0, 0, -1).Format(dateLayout))
		if err != nil {
			return err
		}
		ivs := Intervals(c.windows, c.loc, at.Add(-24*time.Hour), c.horizonEnd(at).AddDate(0, 0, 7))
		base := c.rules.base()
		type change struct {
			id       string
			to       time.Time
			date     string
			cancel   bool
			from     time.Time
			pinnedOK bool
		}
		var changes []change
		prev := at
		for _, r := range list {
			if r.pinned {
				if r.slot.Sub(prev) < base {
					changes = append(changes, change{id: r.id, pinnedOK: true})
				}
				if r.slot.After(prev) {
					prev = r.slot
				}
				continue
			}
			need := prev.Add(base + time.Duration(r.jitter)*time.Second)
			if !r.slot.Before(need) {
				break // 이미 충분히 떨어져 있다. 뒤는 그대로
			}
			to := fit(need, ivs, c.rules)
			date := to.In(c.loc).Format(dateLayout)
			if to.IsZero() || (date != r.date && (counts[date] >= c.rules.DailyTarget || c.excluded[date])) {
				changes = append(changes, change{id: r.id, cancel: true})
				counts[r.date]--
				continue
			}
			if date != r.date {
				counts[r.date]--
				counts[date]++
			}
			changes = append(changes, change{id: r.id, to: to, date: date, from: r.slot})
			prev = to
		}
		// 뒤에서부터 옮겨 같은 시각 충돌을 피한다
		for i := len(changes) - 1; i >= 0; i-- {
			ch := changes[i]
			switch {
			case ch.pinnedOK:
				_, err = tx.Exec(ctx, `UPDATE reservations SET delay_note = '앞 게시가 늦어져 간격이 기본보다 좁아요 (시간 고정)' WHERE id = $1`, ch.id)
			case ch.cancel:
				_, err = tx.Exec(ctx, `UPDATE reservations SET state = 'canceled', delay_note = '앞 게시가 늦어져 남은 허용 시간·수량이 부족해 건너뛰었어요',
					updated_at = now() WHERE id = $1`, ch.id)
			default:
				note := fmt.Sprintf("앞 게시가 늦어져 %s → %s로 옮겼어요", ch.from.In(c.loc).Format("15:04"), ch.to.In(c.loc).Format("15:04"))
				_, err = tx.Exec(ctx, `UPDATE reservations SET slot_at = $2, local_date = $3::date, delay_note = $4, version = version + 1, updated_at = now() WHERE id = $1`,
					ch.id, ch.to, ch.date, note)
				moved++
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
	return moved, err
}

// fit은 t 이후 새 묶음을 시작할 수 있는 가장 이른 시각이다 (허용 구간 안, 끝 여유 전). 없으면 zero.
func fit(t time.Time, ivs []Interval, r Rules) time.Time {
	for _, iv := range ivs {
		last := iv.End.Add(-r.startCutoff())
		if !t.Before(last) {
			continue
		}
		if t.Before(iv.Start) {
			return iv.Start
		}
		return t
	}
	return time.Time{}
}

// ── 게시(publish)가 쓰는 도우미 ───────────────────────────────────────

// LockTx는 시간표 잠금을 잡는다 ("지금 게시"처럼 예약을 만드는 다른 패키지의 작업이 배치와 겹치지 않게).
func LockTx(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, lockID)
	return err
}

// WindowInfo는 t 시점의 허용 여부·다음 허용 구간 시작·규칙이다.
type WindowInfo struct {
	Allowed   bool // 새 묶음을 시작해도 되는지 (구간 끝 여유 포함)
	Inside    bool // 허용 구간 안인지. 이미 시작한 묶음의 남은 답글은 이것만 본다
	Reason    string
	NextStart time.Time // t 이후 처음으로 새 묶음을 시작할 수 있는 시각 (없으면 zero)
	Rules     Rules
	Loc       *time.Location
}

func (s *Service) Window(ctx context.Context, t time.Time) (WindowInfo, error) {
	c, err := s.load(ctx, s.pool)
	if err != nil {
		return WindowInfo{}, err
	}
	ivs := Intervals(c.windows, c.loc, t.Add(-48*time.Hour), t.AddDate(0, 0, 8))
	w := WindowInfo{Rules: c.rules, Loc: c.loc}
	w.Allowed, w.Reason = Allowed(t, c.rules, ivs, c.excluded, c.loc)
	w.Inside = Inside(t, ivs, c.excluded, c.loc)
	for _, iv := range ivs {
		if next := fit(t, []Interval{iv}, c.rules); !next.IsZero() && !c.excluded[next.In(c.loc).Format(dateLayout)] {
			w.NextStart = next
			break
		}
	}
	return w, nil
}

// TodayCount는 t가 속한 현지 날짜의 진행 중·완료 묶음 수다 (수동 게시 경고용).
func (s *Service) TodayCount(ctx context.Context, t time.Time) (int, int, error) {
	c, err := s.load(ctx, s.pool)
	if err != nil {
		return 0, 0, err
	}
	var n int
	err = s.pool.QueryRow(ctx, `SELECT count(*) FROM reservations WHERE state IN ('publishing', 'partial', 'unknown', 'published') AND local_date = $1::date`,
		t.In(c.loc).Format(dateLayout)).Scan(&n)
	return n, c.rules.DailyTarget, err
}
