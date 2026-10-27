package schedule

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yuntyu01/x-automation/internal/clock"
	"github.com/yuntyu01/x-automation/internal/notification"
	"github.com/yuntyu01/x-automation/internal/randx"
	"github.com/yuntyu01/x-automation/internal/settings"
	"github.com/yuntyu01/x-automation/internal/store"
)

const (
	lockID       = 7243100420
	lead         = 5 * time.Minute  // 지금부터 이 시간 안에는 새 예약을 만들지 않는다 (생성·준비 여유)
	overdueGrace = 10 * time.Minute // 이만큼 지나도록 실행되지 않은 예약은 닫는다
)

var (
	ErrNotFound    = errors.New("예약을 찾을 수 없어요")
	ErrConflict    = errors.New("다른 곳에서 먼저 바뀌었어요. 새로고침 후 다시 해 주세요")
	ErrNotEditable = errors.New("이미 게시 중이거나 끝난 예약은 바꿀 수 없어요")
)

type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string { return "입력값을 확인해 주세요" }

// WarningError는 확인하면 진행할 수 있는 경고다 (허용 구간 밖 고정 시각 등).
type WarningError struct{ Warnings []string }

func (e *WarningError) Error() string { return strings.Join(e.Warnings, " / ") }

type Exception struct {
	Date string `json:"date"`
	Note string `json:"note"`
}

type Service struct {
	pool     *pgxpool.Pool
	clock    clock.Clock
	rand     randx.Source
	settings *settings.Repo
	notify   *notification.Service
	kick     func()
}

func NewService(pool *pgxpool.Pool, c clock.Clock, r randx.Source, s *settings.Repo, n *notification.Service) *Service {
	if c == nil {
		c = clock.System{}
	}
	if r == nil {
		r = randx.Default{}
	}
	return &Service{pool: pool, clock: c, rand: r, settings: s, notify: n, kick: func() {}}
}

// SetKick은 예약이 바뀌었을 때 깨울 작업(게시 워커 등)을 정한다.
func (s *Service) SetKick(fn func()) { s.kick = fn }

func (s *Service) lock(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, lockID)
	return err
}

// ── 규칙·구간·쉬는 날 ─────────────────────────────────────────────────

func loadRules(ctx context.Context, db store.DB) (Rules, error) {
	var r Rules
	err := db.QueryRow(ctx, `SELECT daily_target, base_interval_min, jitter_min_min, jitter_max_min, reply_gap_sec, end_margin_min,
		substitute_unready, policy_version, version FROM schedule_rules WHERE id = 1`).
		Scan(&r.DailyTarget, &r.BaseIntervalMin, &r.JitterMinMin, &r.JitterMaxMin, &r.ReplyGapSec, &r.EndMarginMin,
			&r.SubstituteUnready, &r.PolicyVersion, &r.Version)
	return r, err
}

func loadWindows(ctx context.Context, db store.DB) ([]Window, error) {
	rows, err := db.Query(ctx, `SELECT id, label, weekdays, start_minute, end_minute, enabled, sort_order FROM schedule_windows ORDER BY sort_order, start_minute, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Window{}
	for rows.Next() {
		var w Window
		var wd []int16
		if err := rows.Scan(&w.ID, &w.Label, &wd, &w.StartMinute, &w.EndMinute, &w.Enabled, &w.SortOrder); err != nil {
			return nil, err
		}
		for _, d := range wd {
			w.Weekdays = append(w.Weekdays, int(d))
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func loadExceptions(ctx context.Context, db store.DB, from string) ([]Exception, error) {
	rows, err := db.Query(ctx, `SELECT to_char(local_date, 'YYYY-MM-DD'), note FROM schedule_exceptions WHERE local_date >= $1::date ORDER BY local_date`, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Exception{}
	for rows.Next() {
		var e Exception
		if err := rows.Scan(&e.Date, &e.Note); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// config는 계산에 필요한 설정 묶음이다.
type config struct {
	rules    Rules
	windows  []Window
	excluded map[string]bool
	st       settings.Settings
	loc      *time.Location
}

func (s *Service) load(ctx context.Context, db store.DB) (config, error) {
	var c config
	var err error
	if c.st, err = s.settings.Get(ctx); err != nil {
		return c, err
	}
	c.loc = c.st.Location()
	if c.rules, err = loadRules(ctx, db); err != nil {
		return c, err
	}
	if c.windows, err = loadWindows(ctx, db); err != nil {
		return c, err
	}
	ex, err := loadExceptions(ctx, db, s.clock.Now().In(c.loc).AddDate(0, 0, -2).Format(dateLayout))
	if err != nil {
		return c, err
	}
	c.excluded = map[string]bool{}
	for _, e := range ex {
		c.excluded[e.Date] = true
	}
	return c, nil
}

// horizonEnd는 미리 만들어 두는 예약의 끝이다: 오늘 + 생성 확보 기간 + 하루 (화면에서 모레까지 보이게).
func (c config) horizonEnd(now time.Time) time.Time {
	return startOfDay(now.In(c.loc)).AddDate(0, 0, 2+max(1, c.st.Policy.Generation.HorizonDays))
}

// Settings는 시간표 설정 화면용 묶음이다.
type Settings struct {
	Rules      Rules       `json:"rules"`
	Windows    []Window    `json:"windows"`
	Exceptions []Exception `json:"exceptions"`
	Timezone   string      `json:"timezone"`
	Capacity   [2]int      `json:"capacity"` // 오늘 기준 하루 최대 묶음 수 범위 (지연 최대~최소)
}

func (s *Service) Settings(ctx context.Context) (Settings, error) {
	c, err := s.load(ctx, s.pool)
	if err != nil {
		return Settings{}, err
	}
	ex, err := loadExceptions(ctx, s.pool, s.clock.Now().In(c.loc).Format(dateLayout))
	if err != nil {
		return Settings{}, err
	}
	out := Settings{Rules: c.rules, Windows: c.windows, Exceptions: ex, Timezone: c.st.Timezone}
	out.Capacity[0], out.Capacity[1] = Capacity(c.rules, c.windows, c.loc, s.clock.Now())
	return out, nil
}

// ValidateRules는 필드별 오류다.
func ValidateRules(r Rules, ws []Window) map[string]string {
	f := map[string]string{}
	if r.DailyTarget < 0 || r.DailyTarget > 200 {
		f["rules.daily_target"] = "하루 묶음 수는 0~200이에요"
	}
	if r.BaseIntervalMin < 1 || r.BaseIntervalMin > 1440 {
		f["rules.base_interval_min"] = "기본 간격은 1분~24시간이에요"
	}
	if r.JitterMinMin < 0 || r.JitterMaxMin < r.JitterMinMin || r.JitterMaxMin > 240 {
		f["rules.jitter_max_min"] = "추가 지연은 0 이상이고 최대가 최소보다 크거나 같아야 해요 (최대 240분)"
	}
	if r.ReplyGapSec < 0 || r.ReplyGapSec > 3600 {
		f["rules.reply_gap_sec"] = "답글 간격은 0~3600초예요"
	}
	if r.EndMarginMin < 0 || r.EndMarginMin > 240 {
		f["rules.end_margin_min"] = "구간 끝 여유는 0~240분이에요"
	}
	if len(ws) > 20 {
		f["windows"] = "구간은 20개까지예요"
	}
	for i, w := range ws {
		key := fmt.Sprintf("windows.%d", i)
		switch {
		case strings.TrimSpace(w.Label) == "" || len([]rune(w.Label)) > 20:
			f[key+".label"] = "이름을 20자 이내로 써 주세요"
		case w.StartMinute < 0 || w.StartMinute > 1439 || w.EndMinute < 0 || w.EndMinute > 1440:
			f[key+".start_minute"] = "시각이 올바르지 않아요"
		case w.StartMinute == w.EndMinute:
			f[key+".end_minute"] = "시작과 끝이 같아요"
		case len(w.Weekdays) == 0:
			f[key+".weekdays"] = "요일을 하나 이상 고르세요"
		}
		for _, d := range w.Weekdays {
			if d < 0 || d > 6 {
				f[key+".weekdays"] = "요일이 올바르지 않아요"
			}
		}
	}
	return f
}

// saveRules는 규칙과 구간을 저장한다 (version이 다르면 충돌).
func saveRules(ctx context.Context, tx pgx.Tx, r Rules, ws []Window) error {
	tag, err := tx.Exec(ctx, `UPDATE schedule_rules SET daily_target = $1, base_interval_min = $2, jitter_min_min = $3, jitter_max_min = $4,
		reply_gap_sec = $5, end_margin_min = $6, substitute_unready = $7, policy_version = policy_version + 1, version = version + 1, updated_at = now()
		WHERE id = 1 AND version = $8`, r.DailyTarget, r.BaseIntervalMin, r.JitterMinMin, r.JitterMaxMin, r.ReplyGapSec, r.EndMarginMin,
		r.SubstituteUnready, r.Version)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrConflict
	}
	if _, err := tx.Exec(ctx, `DELETE FROM schedule_windows`); err != nil {
		return err
	}
	for i, w := range ws {
		days := append([]int(nil), w.Weekdays...)
		sort.Ints(days)
		if _, err := tx.Exec(ctx, `INSERT INTO schedule_windows (label, weekdays, start_minute, end_minute, enabled, sort_order) VALUES ($1, $2, $3, $4, $5, $6)`,
			strings.TrimSpace(w.Label), days, w.StartMinute, w.EndMinute, w.Enabled, i); err != nil {
			return err
		}
	}
	return nil
}
