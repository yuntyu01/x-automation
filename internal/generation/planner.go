package generation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yuntyu01/x-automation/internal/clock"
	"github.com/yuntyu01/x-automation/internal/notification"
	"github.com/yuntyu01/x-automation/internal/prompt"
	"github.com/yuntyu01/x-automation/internal/settings"
	"github.com/yuntyu01/x-automation/internal/store"
)

const plannerLock = 7243100419

// 부족분 계산 규칙 (docs/ARCHITECTURE.md "부족분")
//
//		부족분 = 창 안의 콘텐츠 없는 예약 − 준비 완료·미배정 − 진행 중 생성(예약 미지정) − 검토 대기 AI 생성분
//
//	  - 준비 완료는 content_overview의 ready 단계만 센다 (미완성 초안·미승인 글 제외, 예시 콘텐츠 제외)
//	  - 검토 대기 AI 생성분은 "확보된 생성 자리"로 본다. 승인만 남은 글을 다시 생성하지 않기 위해서다
//	  - 특정 예약에 묶인 진행 중 생성이 있는 예약은 이미 채워지는 중이라 창에서 뺀다
//	  - 결과는 일일 생성 상한의 남은 양과 한 요청 상한(100)으로 자른다
type Counts struct {
	Needed         int  `json:"needed"`
	Bound          int  `json:"bound"`
	Ready          int  `json:"ready"`
	InFlight       int  `json:"in_flight"`
	ReviewPending  int  `json:"review_pending"`
	RemainingToday int  `json:"remaining_today"`
	Shortfall      int  `json:"shortfall"`
	Capped         bool `json:"capped"`
}

type Planner struct {
	pool     *pgxpool.Pool
	clock    clock.Clock
	settings *settings.Repo
	gen      *Service
	notify   *notification.Service
	log      *slog.Logger
}

func NewPlanner(pool *pgxpool.Pool, c clock.Clock, s *settings.Repo, gen *Service, n *notification.Service, log *slog.Logger) *Planner {
	if c == nil {
		c = clock.System{}
	}
	if log == nil {
		log = slog.Default()
	}
	return &Planner{pool: pool, clock: c, settings: s, gen: gen, notify: n, log: log}
}

// Blockers는 자동 생성이 돌지 않는 이유다 (화면 표시용). 비어 있으면 자동 생성이 동작한다.
func (p *Planner) Blockers(ctx context.Context, st settings.Settings, automatic bool) []string {
	var out []string
	g := st.Policy.Generation
	if automatic && !st.AutomationEnabled {
		out = append(out, "자동 운영이 꺼져 있어요")
	}
	if st.GenerationPaused {
		out = append(out, "생성이 일시정지되어 있어요")
	}
	if st.OperationMode == settings.ModePublishOnly {
		out = append(out, "게시만 자동 모드에서는 글을 생성하지 않아요")
	}
	if !g.TextEnabled {
		out = append(out, "글 생성이 꺼져 있어요")
	}
	if automatic && g.Timing == "manual" {
		out = append(out, "생성 시점이 '버튼을 눌렀을 때'라 자동으로 생성하지 않아요")
	}
	if g.DefaultPromptID == "" {
		out = append(out, "자동 생성에 쓸 기본 프롬프트가 없어요")
	} else if pr, err := p.gen.prompts.Get(ctx, g.DefaultPromptID, 0); err != nil || !pr.Active {
		out = append(out, "기본 프롬프트를 찾을 수 없거나 비활성화되어 있어요")
	} else if _, errs := prompt.Render(pr.Version, nil); len(errs) > 0 {
		// 자동 생성은 변수를 입력받지 않으므로 필수 변수에는 기본값이 있어야 한다
		out = append(out, "기본 프롬프트의 필수 변수에 기본값이 없어요. 프롬프트 화면에서 기본값을 넣어 주세요")
	}
	return out
}

// autoError는 자동 생성 요청을 만들 수 없을 때의 설명이다.
func autoError(err error) *ValidationError {
	return &ValidationError{Fields: map[string]string{"generation": "자동 생성 요청을 만들 수 없어요: " + userMessage(err)}}
}

// Tick은 1분마다 불린다. 설정된 생성 시점·전략에 맞는 생성 요청을 만든다.
func (p *Planner) Tick(ctx context.Context) error {
	st, err := p.settings.Get(ctx)
	if err != nil {
		return err
	}
	if b := p.Blockers(ctx, st, true); len(b) > 0 {
		// 자동 운영 중인데 프롬프트 문제로 못 도는 경우만 알린다 (나머지는 사용자가 고른 상태)
		if st.AutomationEnabled && st.OperationMode != settings.ModePublishOnly && st.Policy.Generation.TextEnabled && st.Policy.Generation.Timing != "manual" &&
			!st.GenerationPaused {
			p.gen.notifyState(ctx, notification.Warning, "generation:blocked", b[0]+". 생성 설정에서 정해 주세요", "/settings/generation")
		}
		return nil
	}
	if p.notify != nil {
		_ = p.notify.Resolve(ctx, "generation:blocked")
	}
	g := st.Policy.Generation
	switch {
	case g.Timing == "on_publish":
		return p.tickDue(ctx, st, 0, "on_publish", "onpub")
	case g.Timing == "prepare" && g.PrepareStrategy == "daily_at":
		return p.tickDaily(ctx, st)
	case g.Timing == "prepare" && g.PrepareStrategy == "before_slot":
		return p.tickDue(ctx, st, g.BeforeMinutes, "before_slot", "before")
	case g.Timing == "prepare" && g.PrepareStrategy == "stock":
		return p.tickStock(ctx, st)
	}
	return nil
}

func dailyTime(st settings.Settings, day time.Time) time.Time {
	var h, m int
	_, _ = fmt.Sscanf(st.Policy.Generation.DailyAt, "%d:%d", &h, &m)
	return time.Date(day.Year(), day.Month(), day.Day(), h, m, 0, 0, st.Location())
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// horizonEnd는 매일 지정 시각 전략의 확보 기간 끝이다: 오늘 남은 시간 + 다음 날부터 확보 기간(일).
func horizonEnd(st settings.Settings, now time.Time) time.Time {
	return startOfDay(now.In(st.Location())).AddDate(0, 0, 1+st.Policy.Generation.HorizonDays)
}

func (p *Planner) tickDaily(ctx context.Context, st settings.Settings) error {
	now := p.clock.Now().In(st.Location())
	if now.Before(dailyTime(st, now)) {
		return nil
	}
	key := fmt.Sprintf("daily:%s:v%d", now.Format("2006-01-02"), st.Policy.Generation.PolicyVersion)
	_, err := p.fill(ctx, st, key, "daily_at", now, horizonEnd(st, now))
	return ignoreValidation(err)
}

// ignoreValidation: 주기 실행에서는 설정 문제를 알림으로 남겼으니 오류로 기록하지 않는다.
func ignoreValidation(err error) error {
	var ve *ValidationError
	if errors.As(err, &ve) {
		return nil
	}
	return err
}

func (p *Planner) tickStock(ctx context.Context, st settings.Settings) error {
	c, err := p.count(ctx, p.pool, st, nil, nil)
	if err != nil {
		return err
	}
	if c.Ready >= st.Policy.Generation.StockMin {
		return nil
	}
	now := p.clock.Now().In(st.Location())
	// 같은 시간대(1시간)에는 한 번만 보충한다
	key := fmt.Sprintf("stock:v%d:%s", st.Policy.Generation.PolicyVersion, now.Format("2006010215"))
	_, err = p.fill(ctx, st, key, "stock", time.Time{}, time.Time{})
	return ignoreValidation(err)
}

// fill은 잠금 안에서 판단 기록을 남기고 부족분만큼 요청을 만든다. 같은 키는 다시 판단하지 않는다.
func (p *Planner) fill(ctx context.Context, st settings.Settings, key, trigger string, from, to time.Time) (*Request, error) {
	var reqID string
	var created bool
	var counts Counts
	err := store.InTx(ctx, p.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, plannerLock); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO generation_marks (key, trigger) VALUES ($1, $2) ON CONFLICT (key) DO NOTHING`, key, trigger)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		var fp, tp *time.Time
		if !from.IsZero() {
			fp, tp = &from, &to
		}
		counts, err = p.count(ctx, tx, st, fp, tp)
		if err != nil {
			return err
		}
		if trigger == "stock" {
			counts = stockCounts(st, counts)
		}
		note := "부족분이 없어요"
		if counts.Shortfall > 0 {
			reqID, created, err = p.gen.SubmitTx(ctx, tx, p.autoInput(st, key, trigger, counts.Shortfall, nil))
			if err != nil {
				return err
			}
			note = fmt.Sprintf("%d개 생성 요청", counts.Shortfall)
		} else if counts.Capped {
			note = "오늘 생성 상한에 도달해 생성하지 않았어요"
		}
		_, err = tx.Exec(ctx, `UPDATE generation_marks SET shortfall = $2, request_id = NULLIF($3, '')::uuid, note = $4 WHERE key = $1`,
			key, counts.Shortfall, reqID, note)
		return err
	})
	if err != nil {
		var ve *ValidationError
		if errors.As(err, &ve) {
			ae := autoError(err)
			p.gen.notifyState(ctx, notification.Warning, "generation:blocked", ae.Fields["generation"], "/settings/generation")
			return nil, ae
		}
		return nil, err
	}
	if counts.Capped {
		p.gen.notifyOnce(ctx, notification.Warning, "generation:daily-limit",
			fmt.Sprintf("오늘 생성 상한(%d개) 때문에 부족분을 다 채우지 못했어요", st.Policy.Generation.DailyLimit), "/settings/generation")
	}
	if reqID == "" {
		return nil, nil
	}
	if created {
		p.gen.kick()
	}
	r, err := p.gen.Get(ctx, reqID)
	return &r, err
}

func stockCounts(st settings.Settings, c Counts) Counts {
	g := st.Policy.Generation
	c.Needed = 0
	want := g.StockTarget - c.Ready - c.InFlight - c.ReviewPending
	c.Shortfall, c.Capped = capShortfall(want, c.RemainingToday)
	return c
}

func capShortfall(want, remaining int) (int, bool) {
	if want <= 0 {
		return 0, false
	}
	capped := false
	if want > remaining {
		want, capped = max(remaining, 0), true
	}
	if want > 100 {
		want = 100
	}
	return want, capped
}

func (p *Planner) autoInput(st settings.Settings, key, trigger string, qty int, reservationID *string) SubmitInput {
	g, im := st.Policy.Generation, st.Policy.Image
	pv := g.PolicyVersion
	return SubmitInput{
		IdempotencyKey: key,
		Trigger:        trigger,
		PolicyVersion:  &pv,
		PromptID:       g.DefaultPromptID,
		Quantity:       qty,
		ReservationID:  reservationID,
		Options: Options{
			Text:             true,
			Image:            g.ImageEnabled && im.DefaultMode == "ai",
			ImageCount:       im.Count,
			ImagePlacement:   im.Placement,
			TextOnlyFallback: g.TextOnlyFallback,
		},
	}
}

// tickDue는 예약별 전략이다: 예약 시각 N분 전(게시 시각 생성은 0분)이 된 빈 예약을 채운다.
// 준비된 재고로 덮을 수 있는 앞쪽 예약은 남기고, 덮지 못하는 뒤쪽 예약에만 생성 요청을 묶는다.
func (p *Planner) tickDue(ctx context.Context, st settings.Settings, minutes int, trigger, prefix string) error {
	now := p.clock.Now()
	var reqIDs []string
	var capped bool
	err := store.InTx(ctx, p.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, plannerLock); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT r.id FROM reservations r
			WHERE r.content_id IS NULL AND r.state IN ('open', 'waiting')
			  AND r.slot_at - make_interval(mins => $1) <= $2 AND r.slot_at > $2 - interval '6 hours'
			  AND NOT EXISTS (SELECT 1 FROM generation_requests g WHERE g.reservation_id = r.id AND g.state IN ('queued', 'running'))
			  AND NOT EXISTS (SELECT 1 FROM generation_marks m WHERE m.key = $3 || ':' || r.id::text || ':v' || $4::int)
			ORDER BY r.slot_at, r.id`, minutes, now, prefix, st.Policy.Generation.PolicyVersion)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		c, err := p.count(ctx, tx, st, nil, nil)
		if err != nil {
			return err
		}
		uncovered := len(ids) - c.Ready - c.InFlight - c.ReviewPending
		n, isCapped := capShortfall(uncovered, c.RemainingToday)
		capped = isCapped
		for _, id := range ids[len(ids)-n:] {
			key := fmt.Sprintf("%s:%s:v%d", prefix, id, st.Policy.Generation.PolicyVersion)
			resID := id
			reqID, _, err := p.gen.SubmitTx(ctx, tx, p.autoInput(st, key, trigger, 1, &resID))
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO generation_marks (key, trigger, shortfall, request_id, note) VALUES ($1, $2, 1, $3, '예약에 맞춰 생성 요청')
				ON CONFLICT (key) DO NOTHING`, key, trigger, reqID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE reservations SET wait_reason = '생성 중', updated_at = now() WHERE id = $1`, id); err != nil {
				return err
			}
			reqIDs = append(reqIDs, reqID)
		}
		return nil
	})
	if err != nil {
		var ve *ValidationError
		if errors.As(err, &ve) {
			p.gen.notifyState(ctx, notification.Warning, "generation:blocked", autoError(err).Fields["generation"], "/settings/generation")
			return nil
		}
		return err
	}
	if capped {
		p.gen.notifyOnce(ctx, notification.Warning, "generation:daily-limit",
			fmt.Sprintf("오늘 생성 상한(%d개)에 도달해 빈 예약을 채우지 못했어요", st.Policy.Generation.DailyLimit), "/settings/generation")
	}
	if len(reqIDs) > 0 {
		p.gen.kick()
	}
	return nil
}

// FillNow는 정기 생성을 기다리지 않고 확보 기간의 부족분을 바로 채운다 (첫 운영 시작·가까운 예약 추가·버튼).
// 같은 분에 두 번 눌러도 요청은 하나다.
func (p *Planner) FillNow(ctx context.Context, reason string) (*Request, Counts, error) {
	st, err := p.settings.Get(ctx)
	if err != nil {
		return nil, Counts{}, err
	}
	if b := p.Blockers(ctx, st, false); len(b) > 0 {
		return nil, Counts{}, &ValidationError{Fields: map[string]string{"generation": b[0]}}
	}
	now := p.clock.Now().In(st.Location())
	key := fmt.Sprintf("fill:%s:%s:v%d", reason, now.Format("200601021504"), st.Policy.Generation.PolicyVersion)
	trigger := "fill_now"
	var r *Request
	if st.Policy.Generation.Timing == "prepare" && st.Policy.Generation.PrepareStrategy == "stock" {
		r, err = p.fillStockNow(ctx, st, key)
	} else {
		r, err = p.fill(ctx, st, key, trigger, now, horizonEnd(st, now))
	}
	if err != nil {
		return nil, Counts{}, err
	}
	var c Counts
	_ = p.pool.QueryRow(ctx, `SELECT shortfall FROM generation_marks WHERE key = $1`, key).Scan(&c.Shortfall)
	return r, c, nil
}

func (p *Planner) fillStockNow(ctx context.Context, st settings.Settings, key string) (*Request, error) {
	// fill의 stock 계산을 쓰되 trigger는 fill_now로 남긴다
	var reqID string
	var created bool
	err := store.InTx(ctx, p.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, plannerLock); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO generation_marks (key, trigger) VALUES ($1, 'fill_now') ON CONFLICT (key) DO NOTHING`, key)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		c, err := p.count(ctx, tx, st, nil, nil)
		if err != nil {
			return err
		}
		c = stockCounts(st, c)
		if c.Shortfall > 0 {
			if reqID, created, err = p.gen.SubmitTx(ctx, tx, p.autoInput(st, key, "fill_now", c.Shortfall, nil)); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `UPDATE generation_marks SET shortfall = $2, request_id = NULLIF($3, '')::uuid, note = $4 WHERE key = $1`,
			key, c.Shortfall, reqID, fmt.Sprintf("재고 보충 %d개", c.Shortfall))
		return err
	})
	var ve *ValidationError
	if errors.As(err, &ve) {
		return nil, autoError(err)
	}
	if err != nil || reqID == "" {
		return nil, err
	}
	if created {
		p.gen.kick()
	}
	r, err := p.gen.Get(ctx, reqID)
	return &r, err
}

// count는 부족분 계산 재료를 센다. from/to가 nil이면 예약 창 없이 재고만 센다.
func (p *Planner) count(ctx context.Context, db store.DB, st settings.Settings, from, to *time.Time) (Counts, error) {
	var c Counts
	var inflightAll int
	if from != nil {
		if err := db.QueryRow(ctx, `SELECT
			count(*) FILTER (WHERE NOT bound), count(*) FILTER (WHERE bound)
			FROM (SELECT EXISTS (SELECT 1 FROM generation_requests g WHERE g.reservation_id = r.id AND g.state IN ('queued', 'running')) AS bound
			      FROM reservations r WHERE r.content_id IS NULL AND r.state IN ('open', 'waiting') AND r.slot_at >= $1 AND r.slot_at < $2) x`,
			*from, *to).Scan(&c.Needed, &c.Bound); err != nil {
			return c, err
		}
	}
	if err := db.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM content_overview o JOIN contents c ON c.id = o.id WHERE o.stage = 'ready' AND NOT o.is_demo AND NOT c.schedule_hold),
		(SELECT COALESCE(sum(quantity - produced - failed), 0) FROM generation_requests
		  WHERE state IN ('queued', 'running') AND reservation_id IS NULL AND content_id IS NULL),
		(SELECT count(*) FROM content_overview o JOIN contents c ON c.id = o.id
		  WHERE o.stage = 'review' AND o.review_state = 'pending' AND c.generation_request_id IS NOT NULL),
		(SELECT COALESCE(sum(quantity - produced - failed), 0) FROM generation_requests WHERE state IN ('queued', 'running'))`).
		Scan(&c.Ready, &c.InFlight, &c.ReviewPending, &inflightAll); err != nil {
		return c, err
	}
	used, err := p.gen.usedTodayDB(ctx, db, st)
	if err != nil {
		return c, err
	}
	c.RemainingToday = max(st.Policy.Generation.DailyLimit-used-inflightAll, 0)
	c.Shortfall, c.Capped = capShortfall(c.Needed-c.Ready-c.InFlight-c.ReviewPending, c.RemainingToday)
	return c, nil
}

// ── 계획 미리보기 (화면) ─────────────────────────────────────────────

type DueSlot struct {
	ReservationID string    `json:"reservation_id"`
	SlotAt        time.Time `json:"slot_at"`
	DueAt         time.Time `json:"due_at"`
	Generating    bool      `json:"generating"`
}

type Mark struct {
	Key       string    `json:"key"`
	Trigger   string    `json:"trigger"`
	Shortfall int       `json:"shortfall"`
	RequestID *string   `json:"request_id"`
	Note      *string   `json:"note"`
	CreatedAt time.Time `json:"created_at"`
}

type Plan struct {
	Active      bool       `json:"active"`
	Blockers    []string   `json:"blockers"`
	Timing      string     `json:"timing"`
	Strategy    string     `json:"strategy"`
	PolicyVer   int        `json:"policy_version"`
	NextRunAt   *time.Time `json:"next_run_at"`
	NextRunNote string     `json:"next_run_note"`
	WindowStart *time.Time `json:"window_start"`
	WindowEnd   *time.Time `json:"window_end"`
	Counts      Counts     `json:"counts"`
	StockMin    int        `json:"stock_min"`
	StockTarget int        `json:"stock_target"`
	Upcoming    []DueSlot  `json:"upcoming"`
	Recent      []Mark     `json:"recent"`
}

// DueAt는 예약 하나의 생성 예정 시각이다 (예약 카드 표시용). 재고 보충·버튼 생성은 예약별 시각이 없다.
func DueAt(st settings.Settings, slot time.Time) *time.Time {
	g := st.Policy.Generation
	var t time.Time
	switch {
	case g.Timing == "on_publish":
		t = slot
	case g.Timing == "prepare" && g.PrepareStrategy == "before_slot":
		t = slot.Add(-time.Duration(g.BeforeMinutes) * time.Minute)
	case g.Timing == "prepare" && g.PrepareStrategy == "daily_at":
		// D일 예약은 D−확보기간 일의 지정 시각 실행부터 확보 창에 들어온다
		t = dailyTime(st, startOfDay(slot.In(st.Location())).AddDate(0, 0, -g.HorizonDays))
	default:
		return nil
	}
	return &t
}

func (p *Planner) Preview(ctx context.Context) (Plan, error) {
	st, err := p.settings.Get(ctx)
	if err != nil {
		return Plan{}, err
	}
	g := st.Policy.Generation
	pl := Plan{Timing: g.Timing, Strategy: g.PrepareStrategy, PolicyVer: g.PolicyVersion, StockMin: g.StockMin, StockTarget: g.StockTarget,
		Upcoming: []DueSlot{}, Recent: []Mark{}}
	pl.Blockers = p.Blockers(ctx, st, true)
	if pl.Blockers == nil {
		pl.Blockers = []string{}
	}
	pl.Active = len(pl.Blockers) == 0
	now := p.clock.Now().In(st.Location())

	var from, to time.Time
	switch {
	case g.Timing == "prepare" && g.PrepareStrategy == "daily_at":
		today := dailyTime(st, now)
		var done bool
		_ = p.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM generation_marks WHERE key = $1)`,
			fmt.Sprintf("daily:%s:v%d", now.Format("2006-01-02"), g.PolicyVersion)).Scan(&done)
		next := today
		switch {
		case done:
			next = dailyTime(st, now.AddDate(0, 0, 1))
		case now.After(today):
			next = now // 오늘 시각이 지났는데 아직 판단 전이면 다음 확인(1분 안)에 실행한다
		}
		pl.NextRunAt = &next
		pl.NextRunNote = fmt.Sprintf("매일 %s에 확보 기간 %d일의 부족분을 준비해요", g.DailyAt, g.HorizonDays)
		from, to = now, horizonEnd(st, now)
	case g.Timing == "prepare" && g.PrepareStrategy == "stock":
		pl.NextRunNote = fmt.Sprintf("준비 완료가 %d개 미만이면 %d개까지 보충해요 (한 시간에 한 번)", g.StockMin, g.StockTarget)
	case g.Timing == "prepare" && g.PrepareStrategy == "before_slot":
		pl.NextRunNote = fmt.Sprintf("각 예약 %d분 전에 빈 예약만 생성해요", g.BeforeMinutes)
		from, to = now, now.Add(24*time.Hour)
	case g.Timing == "on_publish":
		pl.NextRunNote = "예약 시각에 생성을 시작해요. 생성 시간만큼 실제 게시가 늦어질 수 있어요"
		from, to = now, now.Add(24*time.Hour)
	default:
		pl.NextRunNote = "버튼을 눌렀을 때만 생성해요"
	}
	var fp, tp *time.Time
	if !from.IsZero() {
		fp, tp = &from, &to
		pl.WindowStart, pl.WindowEnd = fp, tp
	}
	pl.Counts, err = p.count(ctx, p.pool, st, fp, tp)
	if err != nil {
		return pl, err
	}
	if g.Timing == "prepare" && g.PrepareStrategy == "stock" {
		pl.Counts = stockCounts(st, pl.Counts)
		if pl.Counts.Ready < g.StockMin {
			n := now
			pl.NextRunAt = &n
		}
	}

	// 다가오는 빈 예약과 각 생성 예정 시각
	rows, err := p.pool.Query(ctx, `SELECT r.id, r.slot_at,
		EXISTS (SELECT 1 FROM generation_requests g WHERE g.reservation_id = r.id AND g.state IN ('queued', 'running'))
		FROM reservations r WHERE r.content_id IS NULL AND r.state IN ('open', 'waiting') AND r.slot_at > $1
		ORDER BY r.slot_at LIMIT 10`, now.Add(-6*time.Hour))
	if err != nil {
		return pl, err
	}
	for rows.Next() {
		var d DueSlot
		if err := rows.Scan(&d.ReservationID, &d.SlotAt, &d.Generating); err != nil {
			rows.Close()
			return pl, err
		}
		if due := DueAt(st, d.SlotAt); due != nil {
			d.DueAt = *due
		}
		pl.Upcoming = append(pl.Upcoming, d)
	}
	rows.Close()
	if (g.Timing == "on_publish" || g.PrepareStrategy == "before_slot" && g.Timing == "prepare") && len(pl.Upcoming) > 0 {
		for _, d := range pl.Upcoming {
			if !d.Generating && !d.DueAt.IsZero() {
				t := d.DueAt
				if t.Before(now) {
					t = now
				}
				pl.NextRunAt = &t
				break
			}
		}
	}

	mrows, err := p.pool.Query(ctx, `SELECT key, trigger, shortfall, request_id, note, created_at FROM generation_marks ORDER BY created_at DESC LIMIT 10`)
	if err != nil {
		return pl, err
	}
	defer mrows.Close()
	for mrows.Next() {
		var m Mark
		if err := mrows.Scan(&m.Key, &m.Trigger, &m.Shortfall, &m.RequestID, &m.Note, &m.CreatedAt); err != nil {
			return pl, err
		}
		pl.Recent = append(pl.Recent, m)
	}
	return pl, mrows.Err()
}
