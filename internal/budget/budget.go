// Package budget은 앱이 추정한 사용량·비용과 월 한도 예약을 다룬다.
//
//   - 여기 금액은 요율표(설정)로 계산한 추정치다. 외부 청구서의 실제 결제액과 다르다
//   - 같은 사용은 ref_key로 한 번만 기록한다 (재시도해도 두 번 세지 않는다)
//   - 한도를 켜면 실행 전에 추정 비용을 예약하고, 끝나면 정산·해제한다
//   - 여러 워커가 동시에 한도를 통과하지 않도록 예약은 트랜잭션 잠금 안에서 계산한다
package budget

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yuntyu01/x-automation/internal/clock"
	"github.com/yuntyu01/x-automation/internal/settings"
	"github.com/yuntyu01/x-automation/internal/store"
)

const lockID = 7243100418

type Category string

const (
	AIText  Category = "ai_text"
	AIImage Category = "ai_image"
	XPost   Category = "x_post"
	XMedia  Category = "x_media"
	Storage Category = "storage"
)

func (c Category) generation() bool { return c == AIText || c == AIImage }

// ExceededError는 월 한도에 걸린 경우다.
type ExceededError struct {
	SpentMicros, HeldMicros, LimitMicros, NeedMicros int64
	Currency                                         string
}

func (e *ExceededError) Error() string {
	return fmt.Sprintf("이번 달 예산 한도에 도달했어요 (추정 사용 %s / 한도 %s)", money(e.SpentMicros+e.HeldMicros, e.Currency), money(e.LimitMicros, e.Currency))
}

func money(micros int64, cur string) string {
	return fmt.Sprintf("%s %.4f", cur, float64(micros)/1e6)
}

type Service struct {
	pool     *pgxpool.Pool
	clock    clock.Clock
	settings *settings.Repo
}

func NewService(pool *pgxpool.Pool, c clock.Clock, s *settings.Repo) *Service {
	if c == nil {
		c = clock.System{}
	}
	return &Service{pool: pool, clock: c, settings: s}
}

func (s *Service) period(st settings.Settings) time.Time {
	now := s.clock.Now().In(st.Location())
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// Reserve는 실행 전 추정 비용을 확보한다. 한도가 없으면 기록 없이 통과한다.
// 한도 도달 정책이 "생성만 중지"면 게시 비용은 막지 않는다 (묶음 중간에 답글을 끊지 않게 시작 전에 전체를 확보한다).
func (s *Service) Reserve(ctx context.Context, refKey string, cat Category, amountMicros int64) error {
	st, err := s.settings.Get(ctx)
	if err != nil {
		return err
	}
	b := st.Policy.Budget
	if b.MonthlyLimitMicros <= 0 {
		return nil
	}
	enforce := cat.generation() || b.OnLimit == "stop_all"
	period := s.period(st)
	return store.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, lockID); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM budget_reservations WHERE ref_key = $1)`, refKey).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return nil // 같은 작업의 재시도는 이미 확보한 예약을 쓴다
		}
		spent, held, err := totals(ctx, tx, period)
		if err != nil {
			return err
		}
		if enforce && spent+held+amountMicros > b.MonthlyLimitMicros {
			return &ExceededError{SpentMicros: spent, HeldMicros: held, LimitMicros: b.MonthlyLimitMicros, NeedMicros: amountMicros, Currency: b.Currency}
		}
		_, err = tx.Exec(ctx, `INSERT INTO budget_reservations (ref_key, category, amount_micros, period_month) VALUES ($1, $2, $3, $4)
			ON CONFLICT (ref_key) DO NOTHING`, refKey, string(cat), amountMicros, period)
		return err
	})
}

func totals(ctx context.Context, db store.DB, period time.Time) (spent, held int64, err error) {
	if err = db.QueryRow(ctx, `SELECT COALESCE(sum(estimated_micros), 0) FROM usage_ledger WHERE period_month = $1`, period).Scan(&spent); err != nil {
		return
	}
	err = db.QueryRow(ctx, `SELECT COALESCE(sum(amount_micros), 0) FROM budget_reservations WHERE period_month = $1 AND state = 'held'`, period).Scan(&held)
	return
}

// Entry는 사용량 기록 하나다.
type Entry struct {
	Category  Category
	RefKey    string
	Units     map[string]any
	Micros    int64
	Uncertain bool // 요청이 끊겨 과금 여부를 모르는 경우
}

// Record는 사용량을 기록하고 같은 ref의 예약을 정산한다. 같은 (category, ref_key)는 한 번만 기록된다.
func (s *Service) Record(ctx context.Context, e Entry, reservationKey string) error {
	st, err := s.settings.Get(ctx)
	if err != nil {
		return err
	}
	units, _ := json.Marshal(e.Units)
	certainty := "estimated"
	if e.Uncertain {
		certainty = "uncertain"
	}
	period := s.period(st)
	return store.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO usage_ledger (category, ref_key, units, estimated_micros, currency, rate_version, certainty, period_month)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8) ON CONFLICT (category, ref_key) DO NOTHING`,
			string(e.Category), e.RefKey, units, e.Micros, st.Policy.Pricing.Currency, st.Policy.Pricing.Version, certainty, period); err != nil {
			return err
		}
		if reservationKey != "" {
			_, err := tx.Exec(ctx, `UPDATE budget_reservations SET state = 'settled', closed_at = now() WHERE ref_key = $1 AND state = 'held'`, reservationKey)
			return err
		}
		return nil
	})
}

// Release는 실행하지 않은 예약을 푼다 (실패·취소).
func (s *Service) Release(ctx context.Context, refKey string) error {
	_, err := s.pool.Exec(ctx, `UPDATE budget_reservations SET state = 'released', closed_at = now() WHERE ref_key = $1 AND state = 'held'`, refKey)
	return err
}

// Settle은 실행이 끝난 예약을 닫는다. 실제 사용은 Record로 장부에 들어가 있다.
func (s *Service) Settle(ctx context.Context, refKey string) error {
	_, err := s.pool.Exec(ctx, `UPDATE budget_reservations SET state = 'settled', closed_at = now() WHERE ref_key = $1 AND state = 'held'`, refKey)
	return err
}

// ReleaseStale은 프로세스가 중간에 죽어 남은 오래된 예약을 푼다 (한도 계산이 영구히 막히지 않게).
func (s *Service) ReleaseStale(ctx context.Context, olderThan time.Duration) (int64, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE budget_reservations SET state = 'released', closed_at = now()
		WHERE state = 'held' AND created_at < $1`, s.clock.Now().Add(-olderThan))
	return tag.RowsAffected(), err
}

// ── 추정 ──────────────────────────────────────────────────────────────

// TextMicros는 토큰 수와 요율표로 텍스트 생성 비용을 추정한다.
func TextMicros(p settings.Pricing, in, out int64) int64 {
	return (in*p.TextInputPerMTok + out*p.TextOutputPerMTok) / 1_000_000
}

func ImageMicros(p settings.Pricing, n int) int64 { return int64(n) * p.ImagePerImage }

// PostMicros는 X 게시물 하나의 비용이다. URL이 있으면 별도 단가를 쓴다.
func PostMicros(p settings.Pricing, hasURL bool) int64 {
	if hasURL && p.XPostWithURLMicros > 0 {
		return p.XPostWithURLMicros
	}
	return p.XPostMicros
}

// PricingKnown은 요율표에 확인일이 있고 값이 하나라도 있는지다. 없으면 화면에 "단가 미확인"으로 보인다.
func PricingKnown(p settings.Pricing) bool {
	return p.VerifiedAt != "" && (p.XPostMicros > 0 || p.TextInputPerMTok > 0 || p.ImagePerImage > 0)
}

// Summary는 이번 달 추정 사용량이다.
type Summary struct {
	Period       string           `json:"period"`
	Currency     string           `json:"currency"`
	SpentMicros  int64            `json:"spent_micros"`
	HeldMicros   int64            `json:"held_micros"`
	LimitMicros  int64            `json:"limit_micros"`
	OnLimit      string           `json:"on_limit"`
	ByCategory   map[string]int64 `json:"by_category"`
	Counts       map[string]int64 `json:"counts"`
	Uncertain    int64            `json:"uncertain_count"`
	PricingKnown bool             `json:"pricing_known"`
	VerifiedAt   string           `json:"pricing_verified_at"`
	Exceeded     bool             `json:"exceeded"`
}

func (s *Service) Summary(ctx context.Context) (Summary, error) {
	st, err := s.settings.Get(ctx)
	if err != nil {
		return Summary{}, err
	}
	period := s.period(st)
	sum := Summary{Period: period.Format("2006-01"), Currency: st.Policy.Pricing.Currency, LimitMicros: st.Policy.Budget.MonthlyLimitMicros,
		OnLimit: st.Policy.Budget.OnLimit, ByCategory: map[string]int64{}, Counts: map[string]int64{},
		PricingKnown: PricingKnown(st.Policy.Pricing), VerifiedAt: st.Policy.Pricing.VerifiedAt}
	sum.SpentMicros, sum.HeldMicros, err = totals(ctx, s.pool, period)
	if err != nil {
		return sum, err
	}
	rows, err := s.pool.Query(ctx, `SELECT category, COALESCE(sum(estimated_micros), 0), count(*), count(*) FILTER (WHERE certainty = 'uncertain')
		FROM usage_ledger WHERE period_month = $1 GROUP BY category`, period)
	if err != nil {
		return sum, err
	}
	defer rows.Close()
	for rows.Next() {
		var c string
		var m, n, u int64
		if err := rows.Scan(&c, &m, &n, &u); err != nil {
			return sum, err
		}
		sum.ByCategory[c] = m
		sum.Counts[c] = n
		sum.Uncertain += u
	}
	sum.Exceeded = sum.LimitMicros > 0 && sum.SpentMicros+sum.HeldMicros >= sum.LimitMicros
	return sum, rows.Err()
}

// IsExceeded는 errors.As 도우미다.
func IsExceeded(err error) bool {
	var e *ExceededError
	return errors.As(err, &e)
}
