// Package dashboard는 운영 홈 화면용 요약과, 계속되는 상태(콘텐츠 부족·예산 한도)의 알림을 맡는다.
// 모든 수치는 DB의 실제 상태에서 계산한다 (하드코딩한 운영 수치 없음).
package dashboard

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yuntyu01/x-automation/internal/budget"
	"github.com/yuntyu01/x-automation/internal/clock"
	"github.com/yuntyu01/x-automation/internal/connection"
	"github.com/yuntyu01/x-automation/internal/generation"
	"github.com/yuntyu01/x-automation/internal/notification"
	"github.com/yuntyu01/x-automation/internal/settings"
)

const (
	shortageHorizon = 3 * time.Hour // 이 안의 예약에 넣을 글이 없으면 알린다
	actionHorizon   = 24 * time.Hour
	dueGrace        = 10 * time.Minute
	budgetWarnRatio = 0.8
	fillLead        = 5 * time.Minute // schedule의 배치 여유와 같다
)

type Deps struct {
	Pool     *pgxpool.Pool
	Clock    clock.Clock
	Settings *settings.Repo
	Budget   *budget.Service
	Planner  *generation.Planner
	Notify   *notification.Service
	Live     bool // 실제 X 연동 (목업이면 false)
	// PublishingEnabled는 서버의 PUBLISHING_ENABLED다
	PublishingEnabled bool
}

type Service struct {
	Deps
	conns *connection.Repo
}

func NewService(d Deps) *Service {
	if d.Clock == nil {
		d.Clock = clock.System{}
	}
	return &Service{Deps: d, conns: connection.NewRepo(d.Pool)}
}

type Slot struct {
	ID          string     `json:"id"`
	SlotAt      time.Time  `json:"slot_at"`
	State       string     `json:"state"`
	ContentID   *string    `json:"content_id"`
	Title       *string    `json:"title"`
	Preview     *string    `json:"preview"`
	WaitReason  *string    `json:"wait_reason"`
	BlockReason *string    `json:"block_reason"`
	PartCount   int        `json:"part_count"`
	Generating  bool       `json:"generating"`
	GenerateAt  *time.Time `json:"generation_due_at"`
}

// Action은 홈의 "필요한 조치" 한 줄이다.
type Action struct {
	Key      string `json:"key"`
	Severity string `json:"severity"` // critical · warning · info
	Title    string `json:"title"`
	Body     string `json:"body,omitempty"`
	Link     string `json:"link"`
	Count    int    `json:"count"`
}

type Today struct {
	Date       string `json:"date"`
	Target     int    `json:"target"`      // 하루 묶음 목표
	Published  int    `json:"published"`   // 게시 완료 묶음
	InProgress int    `json:"in_progress"` // 게시 중·확인 필요
	Remaining  int    `json:"remaining"`   // 오늘 남은 예약
	Posts      int    `json:"posts"`       // 오늘 실제로 올라간 X 게시물 수 (본문 + 답글)
	Skipped    int    `json:"skipped"`
}

type Automation struct {
	Enabled            bool       `json:"enabled"`
	ChangedAt          *time.Time `json:"changed_at"`
	GenerationPaused   bool       `json:"generation_paused"`
	PublishingPaused   bool       `json:"publishing_paused"`
	Mock               bool       `json:"mock"`
	PublishingEnabled  bool       `json:"publishing_enabled"`
	PublishBlockers    []string   `json:"publish_blockers"`    // 예약 게시가 일어나지 않는 이유
	GenerationBlockers []string   `json:"generation_blockers"` // 자동 생성이 일어나지 않는 이유
}

type Stock struct {
	Ready   int `json:"ready"`
	Review  int `json:"review"`
	Draft   int `json:"draft"`
	Problem int `json:"problem"`
}

type Home struct {
	Now        time.Time      `json:"now"`
	Timezone   string         `json:"timezone"`
	Mode       string         `json:"operation_mode"`
	Next       *Slot          `json:"next"`
	Upcoming   []Slot         `json:"upcoming"`
	Today      Today          `json:"today"`
	Actions    []Action       `json:"actions"`
	Automation Automation     `json:"automation"`
	Stock      Stock          `json:"stock"`
	Usage      budget.Summary `json:"usage"`
}

// Home은 홈 화면 요약이다.
func (s *Service) Home(ctx context.Context) (Home, error) {
	st, err := s.Settings.Get(ctx)
	if err != nil {
		return Home{}, err
	}
	now := s.Clock.Now()
	loc := st.Location()
	h := Home{Now: now, Timezone: st.Timezone, Mode: string(st.OperationMode), Upcoming: []Slot{}, Actions: []Action{}}
	if h.Usage, err = s.Budget.Summary(ctx); err != nil {
		return h, err
	}
	conn, err := s.conns.View(ctx)
	if err != nil {
		return h, err
	}
	h.Automation = s.automation(ctx, st, conn, h.Usage)

	// 다음 게시 = 글이 들어 있는 가장 가까운 예약 (게시 중 포함). 가까운 예약 목록은 빈 예약까지 보여 준다
	next, err := s.slots(ctx, `r.slot_at > $1 AND (r.state = 'publishing' OR (r.state IN ('assigned', 'waiting') AND r.content_id IS NOT NULL))
		ORDER BY r.slot_at LIMIT 1`, now.Add(-dueGrace))
	if err != nil {
		return h, err
	}
	if len(next) > 0 {
		h.Next = &next[0]
	}
	nextID := ""
	if h.Next != nil {
		nextID = h.Next.ID
	}
	if h.Upcoming, err = s.slots(ctx, `r.slot_at > $1 AND r.state IN ('open', 'assigned', 'waiting', 'publishing') AND r.id::text <> $2
		ORDER BY r.slot_at LIMIT 5`, now.Add(-dueGrace), nextID); err != nil {
		return h, err
	}
	if h.Today, err = s.today(ctx, now, loc); err != nil {
		return h, err
	}
	if err := s.Pool.QueryRow(ctx, `SELECT daily_target FROM schedule_rules WHERE id = 1`).Scan(&h.Today.Target); err != nil {
		return h, err
	}
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE stage = 'ready' AND NOT is_demo), count(*) FILTER (WHERE stage = 'review'),
		count(*) FILTER (WHERE stage = 'draft'), count(*) FILTER (WHERE stage = 'problem') FROM content_overview`).
		Scan(&h.Stock.Ready, &h.Stock.Review, &h.Stock.Draft, &h.Stock.Problem); err != nil {
		return h, err
	}
	h.Actions, err = s.actions(ctx, st, conn, h, now)
	return h, err
}

func (s *Service) slots(ctx context.Context, where string, args ...any) ([]Slot, error) {
	rows, err := s.Pool.Query(ctx, `SELECT r.id, r.slot_at, r.state, r.content_id, c.title, left(rv.raw_text, 120), r.wait_reason, r.block_reason,
		(SELECT count(*) FROM thread_parts tp WHERE tp.revision_id = COALESCE(r.revision_id, c.current_revision_id)),
		EXISTS (SELECT 1 FROM generation_requests g WHERE g.reservation_id = r.id AND g.state IN ('queued', 'running')), r.generation_due_at
		FROM reservations r
		LEFT JOIN contents c ON c.id = r.content_id
		LEFT JOIN content_revisions rv ON rv.id = COALESCE(r.revision_id, c.current_revision_id)
		WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Slot, error) {
		var x Slot
		err := r.Scan(&x.ID, &x.SlotAt, &x.State, &x.ContentID, &x.Title, &x.Preview, &x.WaitReason, &x.BlockReason, &x.PartCount, &x.Generating, &x.GenerateAt)
		return x, err
	})
}

func (s *Service) today(ctx context.Context, now time.Time, loc *time.Location) (Today, error) {
	t := Today{Date: now.In(loc).Format("2006-01-02")}
	y, m, d := now.In(loc).Date()
	start := time.Date(y, m, d, 0, 0, 0, 0, loc)
	end := start.AddDate(0, 0, 1)
	err := s.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE state = 'published'),
		count(*) FILTER (WHERE state IN ('publishing', 'partial', 'unknown')),
		count(*) FILTER (WHERE state IN ('open', 'assigned', 'waiting') AND slot_at > $2),
		count(*) FILTER (WHERE state IN ('canceled', 'failed') AND content_id IS NOT NULL)
		FROM reservations WHERE local_date = $1::date`, t.Date, now.Add(-dueGrace)).Scan(&t.Published, &t.InProgress, &t.Remaining, &t.Skipped)
	if err != nil {
		return t, err
	}
	err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM publish_parts WHERE state = 'succeeded' AND succeeded_at >= $1 AND succeeded_at < $2`, start, end).Scan(&t.Posts)
	return t, err
}

func (s *Service) automation(ctx context.Context, st settings.Settings, conn connection.View, usage budget.Summary) Automation {
	a := Automation{Enabled: st.AutomationEnabled, ChangedAt: st.AutomationChangedAt, GenerationPaused: st.GenerationPaused,
		PublishingPaused: st.PublishingPaused, Mock: !s.Live, PublishingEnabled: s.PublishingEnabled, PublishBlockers: []string{}, GenerationBlockers: []string{}}
	if s.Live && !s.PublishingEnabled {
		a.PublishBlockers = append(a.PublishBlockers, "실제 게시가 꺼져 있어요 (서버 설정)")
	}
	if !st.AutomationEnabled {
		a.PublishBlockers = append(a.PublishBlockers, "자동 운영을 시작하지 않았어요")
	}
	if st.PublishingPaused {
		a.PublishBlockers = append(a.PublishBlockers, "게시가 일시정지되어 있어요")
	}
	switch conn.Status {
	case connection.Connected:
	case connection.Expired:
		a.PublishBlockers = append(a.PublishBlockers, "X 연결이 만료됐어요. 다시 연결해 주세요")
	case connection.Errored:
		a.PublishBlockers = append(a.PublishBlockers, "X 연결에 문제가 있어요")
	default:
		a.PublishBlockers = append(a.PublishBlockers, "X 계정이 연결되지 않았어요")
	}
	if usage.Exceeded && usage.OnLimit == "stop_all" {
		a.PublishBlockers = append(a.PublishBlockers, "월 예산 한도에 도달해 새 게시를 멈췄어요")
	}
	if s.Planner != nil {
		for _, b := range s.Planner.Blockers(ctx, st, true) {
			if b != "자동 운영이 꺼져 있어요" {
				a.GenerationBlockers = append(a.GenerationBlockers, b)
			}
		}
	}
	if usage.Exceeded {
		a.GenerationBlockers = append(a.GenerationBlockers, "월 예산 한도에 도달해 생성을 멈췄어요")
	}
	return a
}

func (s *Service) count(ctx context.Context, q string, args ...any) int {
	var n int
	if err := s.Pool.QueryRow(ctx, q, args...).Scan(&n); err != nil {
		return 0
	}
	return n
}

// emptySoon은 within 안에 시작하는데 넣을 글도, 예정된 생성도 없는 예약 수다.
// 배치 여유(5분)보다 가까운 예약은 이제 채울 수 없으므로 세지 않는다.
func (s *Service) emptySoon(ctx context.Context, now time.Time, within time.Duration, generationWorks bool) int {
	return s.count(ctx, `SELECT count(*) FROM reservations r WHERE r.state = 'open' AND r.slot_at > $1 AND r.slot_at <= $2
		AND NOT EXISTS (SELECT 1 FROM generation_requests g WHERE g.reservation_id = r.id AND g.state IN ('queued', 'running'))
		AND NOT ($3 AND r.generation_due_at IS NOT NULL)`, now.Add(fillLead), now.Add(within), generationWorks)
}

func (s *Service) actions(ctx context.Context, st settings.Settings, conn connection.View, h Home, now time.Time) ([]Action, error) {
	out := []Action{}
	add := func(a Action) {
		if a.Count > 0 {
			out = append(out, a)
		}
	}
	add(Action{Key: "publish_unknown", Severity: "critical", Title: "결과 확인이 필요한 게시", Link: "/publish",
		Body:  "보냈지만 결과를 몰라 다시 보내지 않았어요. X에서 확인해 주세요",
		Count: s.count(ctx, `SELECT count(*) FROM publish_runs WHERE state = 'unknown' AND dismissed_at IS NULL`)})
	add(Action{Key: "publish_partial", Severity: "warning", Title: "답글 게시에 실패한 글", Link: "/publish",
		Body:  "본문은 올라갔어요. 실패한 답글부터 다시 시도할 수 있어요",
		Count: s.count(ctx, `SELECT count(*) FROM publish_runs WHERE state = 'partial' AND dismissed_at IS NULL`)})
	add(Action{Key: "publish_failed", Severity: "warning", Title: "게시에 실패한 글", Link: "/publish",
		Count: s.count(ctx, `SELECT count(*) FROM publish_runs WHERE state = 'failed' AND dismissed_at IS NULL`)})
	switch conn.Status {
	case connection.Expired, connection.Errored:
		add(Action{Key: "x_connection", Severity: "critical", Title: "X 연결을 다시 해야 해요", Body: conn.StatusDetail, Link: "/settings/connections", Count: 1})
	case connection.Disconnected:
		if st.AutomationEnabled {
			add(Action{Key: "x_connection", Severity: "critical", Title: "X 계정이 연결되지 않아 예약 게시를 못 해요", Link: "/settings/connections", Count: 1})
		}
	}
	// 지금 시작했어야 하는데 막힌 예약
	if n := s.count(ctx, `SELECT count(*) FROM reservations WHERE state = 'assigned' AND block_reason IS NOT NULL AND slot_at <= $1 AND slot_at > $2`,
		now, now.Add(-dueGrace)); n > 0 {
		var reason string
		_ = s.Pool.QueryRow(ctx, `SELECT block_reason FROM reservations WHERE state = 'assigned' AND block_reason IS NOT NULL AND slot_at <= $1
			ORDER BY slot_at DESC LIMIT 1`, now).Scan(&reason)
		add(Action{Key: "blocked", Severity: "warning", Title: "시각이 됐지만 게시를 시작하지 못한 예약", Body: reason, Link: "/schedule", Count: n})
	}
	generationWorks := len(h.Automation.GenerationBlockers) == 0 && !st.GenerationPaused
	add(Action{Key: "empty_soon", Severity: "warning", Title: "24시간 안에 넣을 글이 없는 예약", Link: "/contents",
		Body:  "글을 만들거나 직접 등록하면 빈 예약에 자동으로 들어가요",
		Count: s.emptySoon(ctx, now, actionHorizon, generationWorks)})
	add(Action{Key: "waiting_soon", Severity: "info", Title: "준비가 안 된 글이 들어간 예약 (24시간 안)", Link: "/schedule",
		Count: s.count(ctx, `SELECT count(*) FROM reservations WHERE state = 'waiting' AND COALESCE(wait_reason, '') <> '생성 중'
			AND slot_at > $1 AND slot_at <= $2`, now, now.Add(actionHorizon))})
	if st.OperationMode == settings.ModeReview {
		add(Action{Key: "review", Severity: "info", Title: "검토를 기다리는 글", Link: "/contents?stage=review", Count: h.Stock.Review})
	}
	add(Action{Key: "generation_failed", Severity: "warning", Title: "생성에 실패한 글", Link: "/contents?stage=problem",
		Count: s.count(ctx, `SELECT count(*) FROM content_overview WHERE generation_state = 'failed' AND archived_at IS NULL`)})
	u := h.Usage
	if u.LimitMicros > 0 {
		switch {
		case u.Exceeded:
			title := "월 예산 한도에 도달해 생성을 멈췄어요"
			if u.OnLimit == "stop_all" {
				title = "월 예산 한도에 도달해 생성과 새 게시를 멈췄어요"
			}
			add(Action{Key: "budget", Severity: "critical", Title: title, Link: "/settings/budget", Count: 1})
		case float64(u.SpentMicros+u.HeldMicros) >= budgetWarnRatio*float64(u.LimitMicros):
			add(Action{Key: "budget", Severity: "warning", Title: "월 예산의 80%를 넘었어요", Link: "/settings/budget", Count: 1})
		}
		if !u.PricingKnown {
			add(Action{Key: "pricing", Severity: "warning", Title: "요율표가 비어 있어 예산 한도가 제대로 작동하지 않아요", Link: "/settings/budget", Count: 1})
		}
	}
	return out, nil
}

// Watch는 1분마다 불려 계속되는 상태를 알림으로 남기거나 닫는다 (브라우저를 닫아도 서버에 쌓인다).
func (s *Service) Watch(ctx context.Context) error {
	if s.Notify == nil {
		return nil
	}
	st, err := s.Settings.Get(ctx)
	if err != nil {
		return err
	}
	now := s.Clock.Now()
	// 콘텐츠 부족: 자동 운영 중이고 곧 시작할 예약에 넣을 글도 예정된 생성도 없을 때
	generationWorks := false
	if s.Planner != nil {
		generationWorks = len(s.Planner.Blockers(ctx, st, true)) == 0 && !st.GenerationPaused
	}
	if n := s.emptySoon(ctx, now, shortageHorizon, generationWorks); st.AutomationEnabled && n > 0 {
		body := fmt.Sprintf("앞으로 %d시간 안의 예약 %d개에 넣을 글이 없어요. 글을 만들거나 직접 등록해 주세요", int(shortageHorizon.Hours()), n)
		if !generationWorks {
			body += " (자동 생성이 멈춰 있어요)"
		}
		_ = s.Notify.Upsert(ctx, notification.New{Severity: notification.Warning, Category: "stock", GroupKey: "stock:shortage",
			Title: "곧 게시할 글이 부족해요", Body: body, Link: "/contents"})
	} else {
		_ = s.Notify.Resolve(ctx, "stock:shortage")
	}
	// 예산
	u, err := s.Budget.Summary(ctx)
	if err != nil {
		return err
	}
	switch {
	case u.LimitMicros > 0 && u.Exceeded:
		title := "월 예산 한도에 도달해 생성을 멈췄어요"
		if u.OnLimit == "stop_all" {
			title = "월 예산 한도에 도달해 생성과 새 게시를 멈췄어요"
		}
		_ = s.Notify.Upsert(ctx, notification.New{Severity: notification.Critical, Category: "budget", GroupKey: "budget:exceeded",
			Title: title, Body: "추정 사용량 기준이에요. 한도를 올리거나 다음 달까지 기다리면 다시 시작해요", Link: "/settings/budget"})
		_ = s.Notify.Resolve(ctx, "budget:warn")
		_ = s.Notify.Resolve(ctx, "budget:insufficient") // 한도 도달 알림 하나로 충분하다
	case u.LimitMicros > 0 && float64(u.SpentMicros+u.HeldMicros) >= budgetWarnRatio*float64(u.LimitMicros):
		_ = s.Notify.Resolve(ctx, "budget:exceeded")
		_ = s.Notify.Upsert(ctx, notification.New{Severity: notification.Warning, Category: "budget", GroupKey: "budget:warn",
			Title: "월 예산의 80%를 넘었어요", Body: "추정 사용량 기준이에요. 실제 청구액은 각 공급자 콘솔에서 확인하세요", Link: "/settings/budget"})
	default:
		_ = s.Notify.Resolve(ctx, "budget:exceeded")
		_ = s.Notify.Resolve(ctx, "budget:warn")
		if u.LimitMicros <= 0 {
			_ = s.Notify.Resolve(ctx, "budget:insufficient") // 한도를 없앴다
		}
	}
	return nil
}
