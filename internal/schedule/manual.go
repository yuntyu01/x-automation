package schedule

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yuntyu01/x-automation/internal/settings"
	"github.com/yuntyu01/x-automation/internal/store"
)

// 사용자가 직접 하는 예약 조작. 모두 시간표 잠금 안에서 하고, 게시 중·완료된 예약은 바꾸지 않는다.

type resRow struct {
	id      string
	slotAt  time.Time
	date    string
	content *string
	pinned  bool
	state   string
}

func lockReservation(ctx context.Context, tx pgx.Tx, id string) (resRow, error) {
	var r resRow
	err := tx.QueryRow(ctx, `SELECT id, slot_at, to_char(local_date, 'YYYY-MM-DD'), content_id, pinned, state FROM reservations WHERE id = $1 FOR UPDATE`, id).
		Scan(&r.id, &r.slotAt, &r.date, &r.content, &r.pinned, &r.state)
	if store.IsNoRows(err) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	switch r.state {
	case "open", "assigned", "waiting":
	default:
		return r, ErrNotEditable
	}
	return r, nil
}

func (s *Service) withLock(ctx context.Context, fn func(tx pgx.Tx, c config) error) error {
	err := store.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.lock(ctx, tx); err != nil {
			return err
		}
		c, err := s.load(ctx, tx)
		if err != nil {
			return err
		}
		return fn(tx, c)
	})
	if err == nil {
		_ = s.Refresh(ctx) // 상태·대기 사유를 바로 맞춰 화면에 반영한다
		s.kick()
	}
	return err
}

// Assign은 예약에 고른 글을 넣는다 (준비 안 된 글도 넣을 수 있고, 준비될 때까지 대기로 보인다).
func (s *Service) Assign(ctx context.Context, reservationID, contentID string) error {
	return s.withLock(ctx, func(tx pgx.Tx, c config) error {
		if _, err := lockReservation(ctx, tx, reservationID); err != nil {
			return err
		}
		return assign(ctx, tx, reservationID, contentID, c.st.OperationMode == settings.ModeReview)
	})
}

// Unassign은 예약에서 글을 뺀다 ("예약 해제"). 뺀 글은 사용자가 다시 넣을 때까지 자동 배치하지 않는다.
func (s *Service) Unassign(ctx context.Context, reservationID string) error {
	return s.withLock(ctx, func(tx pgx.Tx, _ config) error {
		r, err := lockReservation(ctx, tx, reservationID)
		if err != nil {
			return err
		}
		if r.content == nil {
			return nil
		}
		if err := release(ctx, tx, r.id, ""); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE contents SET schedule_hold = true WHERE id = $1`, *r.content)
		return err
	})
}

// Swap은 두 예약의 글을 맞바꾼다 ("순서 바꾸기").
func (s *Service) Swap(ctx context.Context, a, b string) error {
	return s.withLock(ctx, func(tx pgx.Tx, c config) error {
		ra, err := lockReservation(ctx, tx, a)
		if err != nil {
			return err
		}
		rb, err := lockReservation(ctx, tx, b)
		if err != nil {
			return err
		}
		review := c.st.OperationMode == settings.ModeReview
		if err := release(ctx, tx, ra.id, ""); err != nil {
			return err
		}
		if err := release(ctx, tx, rb.id, ""); err != nil {
			return err
		}
		if rb.content != nil {
			if err := assign(ctx, tx, ra.id, *rb.content, review); err != nil {
				return err
			}
		}
		if ra.content != nil {
			if err := assign(ctx, tx, rb.id, *ra.content, review); err != nil {
				return err
			}
		}
		return nil
	})
}

// Move는 예약의 글을 다음 빈 예약("next") 또는 내일 첫 빈 예약("tomorrow")으로 옮긴다.
func (s *Service) Move(ctx context.Context, reservationID, to string) (string, error) {
	var target string
	err := s.withLock(ctx, func(tx pgx.Tx, c config) error {
		r, err := lockReservation(ctx, tx, reservationID)
		if err != nil {
			return err
		}
		if r.content == nil {
			return &ValidationError{Fields: map[string]string{"reservation": "옮길 글이 없는 빈 예약이에요"}}
		}
		date := ""
		after := r.slotAt
		if to == "tomorrow" {
			d, _ := time.ParseInLocation(dateLayout, r.date, c.loc)
			date = d.AddDate(0, 0, 1).Format(dateLayout)
		}
		if now := s.clock.Now().Add(lead); after.Before(now) {
			after = now
		}
		if target, err = nextOpen(ctx, tx, after, date); err != nil {
			return err
		}
		if target == "" {
			if to == "tomorrow" {
				return &ValidationError{Fields: map[string]string{"reservation": "내일 빈 예약이 없어요. 내일 예약의 순서를 바꾸거나 시간을 지정해 넣으세요"}}
			}
			return &ValidationError{Fields: map[string]string{"reservation": "뒤에 빈 예약이 없어요"}}
		}
		return assign(ctx, tx, target, *r.content, c.st.OperationMode == settings.ModeReview)
	})
	return target, err
}

// Skip은 이 시각을 건너뛴다. 준비된 글은 다음 빈 예약으로 옮긴다.
func (s *Service) Skip(ctx context.Context, reservationID string) error {
	return s.withLock(ctx, func(tx pgx.Tx, c config) error {
		r, err := lockReservation(ctx, tx, reservationID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE reservations SET state = 'canceled', delay_note = '사용자가 건너뛰었어요', updated_at = now() WHERE id = $1`, r.id); err != nil {
			return err
		}
		if r.content != nil {
			if id, err := nextOpen(ctx, tx, maxTime(r.slotAt, s.clock.Now().Add(lead)), ""); err != nil {
				return err
			} else if id != "" {
				return assign(ctx, tx, id, *r.content, c.st.OperationMode == settings.ModeReview)
			}
		}
		return nil
	})
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// PinInput은 시각 지정이다. Confirm이 false면 경고만 돌려준다.
type PinInput struct {
	At        time.Time `json:"at"`
	ContentID string    `json:"content_id"` // 새 고정 예약을 만들 때
	Confirm   bool      `json:"confirm"`
}

// pinWarnings는 고정 시각이 규칙을 벗어나는 점이다 (숨기지 않고 보여 주고, 확인하면 진행한다).
func (s *Service) pinWarnings(ctx context.Context, tx pgx.Tx, c config, at time.Time, exceptID string) ([]string, error) {
	var w []string
	ivs := Intervals(c.windows, c.loc, at.Add(-48*time.Hour), at.Add(48*time.Hour))
	if ok, reason := Allowed(at, c.rules, ivs, c.excluded, c.loc); !ok {
		w = append(w, reason)
	}
	base := c.rules.base()
	var near []time.Time
	rows, err := tx.Query(ctx, `SELECT slot_at FROM reservations WHERE state IN `+activeStates+` AND id <> $1 AND slot_at > $2 AND slot_at < $3`,
		exceptID, at.Add(-base), at.Add(base))
	if err != nil {
		return nil, err
	}
	if near, err = pgx.CollectRows(rows, pgx.RowTo[time.Time]); err != nil {
		return nil, err
	}
	for _, t := range near {
		if t.Equal(at) {
			return nil, &ValidationError{Fields: map[string]string{"at": "같은 시각에 다른 예약이 있어요"}}
		}
		w = append(w, fmt.Sprintf("%s 예약과 기본 간격(%d분)보다 가까워요", t.In(c.loc).Format("15:04"), c.rules.BaseIntervalMin))
	}
	date := at.In(c.loc).Format(dateLayout)
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM reservations WHERE state IN `+activeStates+` AND local_date = $1::date AND id <> $2`, date, exceptID).Scan(&n); err != nil {
		return nil, err
	}
	if n >= c.rules.DailyTarget {
		w = append(w, fmt.Sprintf("그날 묶음 수가 하루 목표(%d개)를 넘어요", c.rules.DailyTarget))
	}
	return w, nil
}

// Pin은 예약을 특정 시각으로 옮기고 시간 고정한다. 고정 예약은 전체 재배치에서도 유지된다.
func (s *Service) Pin(ctx context.Context, reservationID string, in PinInput) error {
	return s.withLock(ctx, func(tx pgx.Tx, c config) error {
		r, err := lockReservation(ctx, tx, reservationID)
		if err != nil {
			return err
		}
		if !in.At.After(s.clock.Now()) {
			return &ValidationError{Fields: map[string]string{"at": "지금 이후 시각을 고르세요"}}
		}
		w, err := s.pinWarnings(ctx, tx, c, in.At, r.id)
		if err != nil {
			return err
		}
		if len(w) > 0 && !in.Confirm {
			return &WarningError{Warnings: w}
		}
		note := "직접 시간을 지정했어요"
		if len(w) > 0 {
			note += " (규칙 밖: " + w[0] + ")"
		}
		_, err = tx.Exec(ctx, `UPDATE reservations SET slot_at = $2, local_date = $3::date, pinned = true, delay_note = $4, version = version + 1, updated_at = now() WHERE id = $1`,
			r.id, in.At, in.At.In(c.loc).Format(dateLayout), note)
		return err
	})
}

// CreatePinned는 고른 글로 특정 시각의 고정 예약을 새로 만든다.
func (s *Service) CreatePinned(ctx context.Context, in PinInput) (string, error) {
	var id string
	err := s.withLock(ctx, func(tx pgx.Tx, c config) error {
		if !in.At.After(s.clock.Now()) {
			return &ValidationError{Fields: map[string]string{"at": "지금 이후 시각을 고르세요"}}
		}
		w, err := s.pinWarnings(ctx, tx, c, in.At, "00000000-0000-0000-0000-000000000000")
		if err != nil {
			return err
		}
		if len(w) > 0 && !in.Confirm {
			return &WarningError{Warnings: w}
		}
		note := "직접 시간을 지정했어요"
		if len(w) > 0 {
			note += " (규칙 밖: " + w[0] + ")"
		}
		if err := tx.QueryRow(ctx, `INSERT INTO reservations (slot_at, original_slot_at, local_date, pinned, delay_note, rule_version)
			VALUES ($1, $1, $2::date, true, $3, $4) RETURNING id`, in.At, in.At.In(c.loc).Format(dateLayout), note, c.rules.PolicyVersion).Scan(&id); err != nil {
			return err
		}
		return assign(ctx, tx, id, in.ContentID, c.st.OperationMode == settings.ModeReview)
	})
	return id, err
}

// Unpin은 시간 고정을 푼다 (시각은 그대로).
func (s *Service) Unpin(ctx context.Context, reservationID string) error {
	return s.withLock(ctx, func(tx pgx.Tx, _ config) error {
		r, err := lockReservation(ctx, tx, reservationID)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE reservations SET pinned = false, version = version + 1, updated_at = now() WHERE id = $1`, r.id)
		return err
	})
}

// PlaceResult는 여러 글 배치 결과다.
type PlaceResult struct {
	Placed  []PlacedItem      `json:"placed"`
	Skipped map[string]string `json:"skipped"`
	Left    []string          `json:"left"` // 빈 예약이 모자라 넣지 못한 글
}

type PlacedItem struct {
	ContentID     string    `json:"content_id"`
	ReservationID string    `json:"reservation_id"`
	SlotAt        time.Time `json:"slot_at"`
}

// PlaceMany는 고른 글을 순서대로 다음 빈 예약에 넣는다. 이미 예약된 글은 건너뛴다.
func (s *Service) PlaceMany(ctx context.Context, contentIDs []string) (PlaceResult, error) {
	res := PlaceResult{Placed: []PlacedItem{}, Skipped: map[string]string{}, Left: []string{}}
	err := s.withLock(ctx, func(tx pgx.Tx, c config) error {
		review := c.st.OperationMode == settings.ModeReview
		after := s.clock.Now().Add(lead)
		for i, id := range contentIDs {
			var active bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM reservations WHERE content_id = $1 AND state IN `+activeStates+` AND state <> 'published')`, id).Scan(&active); err != nil {
				return err
			}
			if active {
				res.Skipped[id] = "이미 예약됐거나 게시 중이에요"
				continue
			}
			target, err := nextOpen(ctx, tx, after, "")
			if err != nil {
				return err
			}
			if target == "" {
				res.Left = append(res.Left, contentIDs[i:]...)
				return nil
			}
			if err := assign(ctx, tx, target, id, review); err != nil {
				if ve, ok := err.(*ValidationError); ok {
					for _, v := range ve.Fields {
						res.Skipped[id] = v
					}
					continue
				}
				return err
			}
			var at time.Time
			if err := tx.QueryRow(ctx, `SELECT slot_at FROM reservations WHERE id = $1`, target).Scan(&at); err != nil {
				return err
			}
			res.Placed = append(res.Placed, PlacedItem{ContentID: id, ReservationID: target, SlotAt: at})
			after = at
		}
		return nil
	})
	return res, err
}

// AddException은 날짜를 쉬는 날로 정하고 그날 실행 전 예약(고정 제외)을 닫는다. 준비된 글은 다음 빈 예약으로 흐른다.
func (s *Service) AddException(ctx context.Context, date, note string) (int, error) {
	if _, err := time.Parse(dateLayout, date); err != nil {
		return 0, &ValidationError{Fields: map[string]string{"date": "날짜 형식이 올바르지 않아요"}}
	}
	n := 0
	err := s.withLock(ctx, func(tx pgx.Tx, c config) error {
		if _, err := tx.Exec(ctx, `INSERT INTO schedule_exceptions (local_date, note) VALUES ($1::date, $2) ON CONFLICT (local_date) DO UPDATE SET note = EXCLUDED.note`, date, note); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `UPDATE reservations SET state = 'canceled', delay_note = '쉬는 날로 지정해 건너뛰었어요', updated_at = now()
			WHERE local_date = $1::date AND state IN `+pendingStates+` AND NOT pinned RETURNING content_id`, date)
		if err != nil {
			return err
		}
		contents, err := pgx.CollectRows(rows, pgx.RowTo[*string])
		if err != nil {
			return err
		}
		n = len(contents)
		review := c.st.OperationMode == settings.ModeReview
		for _, cid := range contents {
			if cid == nil {
				continue
			}
			id, err := nextOpen(ctx, tx, s.clock.Now().Add(lead), "")
			if err != nil || id == "" {
				return err
			}
			if err := assign(ctx, tx, id, *cid, review); err != nil {
				return err
			}
		}
		return nil
	})
	return n, err
}

// RemoveException은 쉬는 날을 지우고 그날 예약을 규칙대로 다시 만든다.
func (s *Service) RemoveException(ctx context.Context, date string) error {
	d, err := time.Parse(dateLayout, date)
	if err != nil {
		return &ValidationError{Fields: map[string]string{"date": "날짜 형식이 올바르지 않아요"}}
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM schedule_exceptions WHERE local_date = $1::date`, date); err != nil {
		return err
	}
	st, err := s.settings.Get(ctx)
	if err != nil {
		return err
	}
	loc := st.Location()
	from := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc)
	_, err = s.Rebuild(ctx, RebuildInput{From: from, To: from.AddDate(0, 0, 1)}, false)
	return err
}
