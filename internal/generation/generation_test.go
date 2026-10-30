package generation_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yuntyu01/x-automation/internal/budget"
	"github.com/yuntyu01/x-automation/internal/clock"
	"github.com/yuntyu01/x-automation/internal/content"
	"github.com/yuntyu01/x-automation/internal/generation"
	"github.com/yuntyu01/x-automation/internal/integration"
	"github.com/yuntyu01/x-automation/internal/integration/ai"
	"github.com/yuntyu01/x-automation/internal/integration/storage"
	"github.com/yuntyu01/x-automation/internal/jobs"
	"github.com/yuntyu01/x-automation/internal/media"
	"github.com/yuntyu01/x-automation/internal/notification"
	"github.com/yuntyu01/x-automation/internal/prompt"
	"github.com/yuntyu01/x-automation/internal/settings"
	"github.com/yuntyu01/x-automation/internal/testutil/pgtest"
)

type env struct {
	t        *testing.T
	ctx      context.Context
	pool     *pgxpool.Pool
	clk      *clock.Fake
	settings *settings.Repo
	contents *content.Service
	prompts  *prompt.Service
	queue    *jobs.Queue
	gen      *generation.Service
	planner  *generation.Planner
	mock     *ai.Mock
	promptID string
}

// 2026-03-02(월) 22:00 KST
var base = time.Date(2026, 3, 2, 13, 0, 0, 0, time.UTC)

func newEnv(t *testing.T) *env {
	t.Helper()
	pool := pgtest.New(t)
	ctx := context.Background()
	clk := clock.NewFake(base)
	sr := settings.NewRepo(pool)
	local, err := storage.NewLocal(t.TempDir(), "http://localhost/media")
	if err != nil {
		t.Fatal(err)
	}
	cs := content.NewService(pool, clk, nil)
	ps := prompt.NewService(pool)
	q := jobs.NewQueue(pool, clk)
	mock := &ai.Mock{}
	gen := generation.NewService(generation.Deps{Pool: pool, Clock: clk, Queue: q, Provider: mock, Settings: sr, Prompts: ps, Contents: cs,
		Media: media.NewService(pool, local, clk, nil), Budget: budget.NewService(pool, clk, sr), Notify: notification.NewService(pool)})
	e := &env{t: t, ctx: ctx, pool: pool, clk: clk, settings: sr, contents: cs, prompts: ps, queue: q, gen: gen, mock: mock}
	e.planner = generation.NewPlanner(pool, clk, sr, gen, notification.NewService(pool), nil)
	p, err := ps.Create(ctx, prompt.Input{Name: "단편", Category: "소설", Body: "{{topic}}에 관한 짧은 이야기",
		Variables: []prompt.Variable{{Name: "topic", Label: "주제", Type: "text", Default: "비 오는 날"}}})
	if err != nil {
		t.Fatal(err)
	}
	e.promptID = p.ID
	e.policy(func(st *settings.Settings) {
		st.OperationMode = settings.ModeFullAuto
		st.Policy.Generation.DefaultPromptID = p.ID
		st.Policy.Generation.ImageEnabled = true
	})
	on := true
	if _, err := sr.SetFlags(ctx, &on, nil, nil); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) policy(fn func(st *settings.Settings)) {
	e.t.Helper()
	st, err := e.settings.Get(e.ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	fn(&st)
	if _, err := e.settings.Update(e.ctx, st.Version, st.OperationMode, st.Timezone, st.Policy); err != nil {
		e.t.Fatal(err)
	}
}

// drain은 대기 중인 생성 작업을 워커처럼 하나씩 처리한다.
func (e *env) drain() {
	e.t.Helper()
	for i := 0; i < 500; i++ {
		j, err := e.queue.Claim(e.ctx, []string{generation.JobKind}, time.Minute)
		if err != nil {
			e.t.Fatal(err)
		}
		if j == nil {
			return
		}
		res := e.gen.HandleItem(e.ctx, j)
		switch res.Outcome {
		case jobs.Done:
			err = e.queue.Complete(e.ctx, j)
		case jobs.RetryLater:
			err = e.queue.Retry(e.ctx, j, res.Err, e.clk.Now()) // 테스트에서는 바로 다시 시도
		case jobs.Snoozed:
			if err := e.queue.Snooze(e.ctx, j, res.At, "snooze"); err != nil {
				e.t.Fatal(err)
			}
			return
		default:
			err = e.queue.Fail(e.ctx, j, res.Err, res.Class)
		}
		if err != nil {
			e.t.Fatal(err)
		}
	}
	e.t.Fatal("drain did not finish")
}

const readyText = "그날 밤 나는 오래된 편지를 다시 꺼내 읽었다. 글씨는 바래 있었지만 마음은 그대로였다.\n\n창밖으로 비가 내리고 있었다. 나는 답장을 쓰기로 했다."

func (e *env) readyContents(n int) {
	e.t.Helper()
	for i := 0; i < n; i++ {
		if _, err := e.contents.Create(e.ctx, content.NewContent{RawText: fmt.Sprintf("%s %d", readyText, i)}); err != nil {
			e.t.Fatal(err)
		}
	}
}

func (e *env) reservations(n int, first time.Time, gap time.Duration) []string {
	e.t.Helper()
	var ids []string
	for i := 0; i < n; i++ {
		at := first.Add(time.Duration(i) * gap)
		var id string
		if err := e.pool.QueryRow(e.ctx, `INSERT INTO reservations (slot_at, original_slot_at, local_date) VALUES ($1, $1, ($1 AT TIME ZONE 'Asia/Seoul')::date) RETURNING id`,
			at).Scan(&id); err != nil {
			e.t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

func (e *env) requests() []generation.Request {
	e.t.Helper()
	rs, err := e.gen.List(e.ctx, false, 100)
	if err != nil {
		e.t.Fatal(err)
	}
	return rs
}

// 완료 기준: 준비 8개·예약 20개에서 12개만 생성, 진행 중 요청 중복 제외
func TestDailyStrategyGeneratesOnlyShortfall(t *testing.T) {
	e := newEnv(t)
	e.policy(func(st *settings.Settings) {
		st.Policy.Generation.Timing = "prepare"
		st.Policy.Generation.PrepareStrategy = "daily_at"
		st.Policy.Generation.DailyAt = "21:00"
		st.Policy.Generation.HorizonDays = 1
	})
	e.readyContents(8)
	// 내일(3/3) 새벽 0시부터 30분 간격 20개 — 확보 기간 안
	e.reservations(20, time.Date(2026, 3, 2, 15, 0, 0, 0, time.UTC), 30*time.Minute)
	// 확보 기간 밖(모레 이후) 예약은 세지 않는다
	e.reservations(5, time.Date(2026, 3, 4, 15, 30, 0, 0, time.UTC), 30*time.Minute)

	if err := e.planner.Tick(e.ctx); err != nil {
		t.Fatal(err)
	}
	rs := e.requests()
	if len(rs) != 1 || rs[0].Quantity != 12 || rs[0].Trigger != "daily_at" {
		t.Fatalf("requests = %+v", rs)
	}
	// 같은 날 다시 돌아도(재시작·다른 인스턴스) 새 요청을 만들지 않는다
	for i := 0; i < 3; i++ {
		e.clk.Advance(time.Minute)
		if err := e.planner.Tick(e.ctx); err != nil {
			t.Fatal(err)
		}
	}
	// 바로 채우기도 진행 중인 12개를 부족분에서 뺀다
	r, _, err := e.planner.FillNow(e.ctx, "button")
	if err != nil {
		t.Fatal(err)
	}
	if r != nil || len(e.requests()) != 1 {
		t.Fatalf("in-flight generation must be excluded; got extra request %+v", r)
	}
	var jobsN int
	_ = e.pool.QueryRow(e.ctx, `SELECT count(*) FROM jobs WHERE kind = 'generate.item'`).Scan(&jobsN)
	if jobsN != 12 {
		t.Fatalf("jobs = %d", jobsN)
	}
	e.drain()
	got, _ := e.gen.Get(e.ctx, rs[0].ID)
	if got.State != "succeeded" || got.Produced != 12 {
		t.Fatalf("request after drain = %+v", got)
	}
	var ai int
	_ = e.pool.QueryRow(e.ctx, `SELECT count(*) FROM content_overview WHERE source = 'ai' AND stage = 'ready'`).Scan(&ai)
	if ai != 12 {
		t.Fatalf("ai ready contents = %d", ai)
	}
}

func TestDailyStrategyWaitsForTimeAndRespectsDailyLimit(t *testing.T) {
	e := newEnv(t)
	e.clk.Set(time.Date(2026, 3, 2, 11, 0, 0, 0, time.UTC)) // 20:00 KST, 지정 시각 전
	e.policy(func(st *settings.Settings) {
		st.Policy.Generation.Timing = "prepare"
		st.Policy.Generation.PrepareStrategy = "daily_at"
		st.Policy.Generation.DailyAt = "21:00"
		st.Policy.Generation.DailyLimit = 5
	})
	e.reservations(10, time.Date(2026, 3, 2, 15, 0, 0, 0, time.UTC), 30*time.Minute)
	_ = e.planner.Tick(e.ctx)
	if n := len(e.requests()); n != 0 {
		t.Fatalf("must wait until 21:00, got %d requests", n)
	}
	pl, err := e.planner.Preview(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pl.NextRunAt == nil || !pl.NextRunAt.Equal(time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)) || pl.Counts.Needed != 10 || !pl.Counts.Capped || pl.Counts.Shortfall != 5 {
		t.Fatalf("plan = %+v", pl)
	}
	e.clk.Set(time.Date(2026, 3, 2, 12, 0, 30, 0, time.UTC))
	_ = e.planner.Tick(e.ctx)
	rs := e.requests()
	if len(rs) != 1 || rs[0].Quantity != 5 {
		t.Fatalf("daily limit must cap the request: %+v", rs)
	}
	pl, _ = e.planner.Preview(e.ctx)
	if pl.NextRunAt == nil || !pl.NextRunAt.Equal(time.Date(2026, 3, 3, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("next run after today's run = %v", pl.NextRunAt)
	}
}

func TestBeforeSlotStrategyBindsUncoveredReservations(t *testing.T) {
	e := newEnv(t)
	e.policy(func(st *settings.Settings) {
		st.Policy.Generation.Timing = "prepare"
		st.Policy.Generation.PrepareStrategy = "before_slot"
		st.Policy.Generation.BeforeMinutes = 30
	})
	now := e.clk.Now()
	ids := e.reservations(2, now.Add(10*time.Minute), 15*time.Minute) // +10분, +25분: 둘 다 30분 전 시점 지남
	late := e.reservations(1, now.Add(3*time.Hour), 0)
	e.readyContents(1) // 앞쪽 하나는 준비된 재고로 채운다
	if err := e.planner.Tick(e.ctx); err != nil {
		t.Fatal(err)
	}
	rs := e.requests()
	if len(rs) != 1 || rs[0].Quantity != 1 || rs[0].ReservationID == nil || *rs[0].ReservationID != ids[1] || rs[0].Trigger != "before_slot" {
		t.Fatalf("requests = %+v (want one bound to the later reservation)", rs)
	}
	_ = e.planner.Tick(e.ctx)
	if len(e.requests()) != 1 {
		t.Fatal("same reservation must not be generated twice")
	}
	pl, _ := e.planner.Preview(e.ctx)
	var lateDue time.Time
	for _, d := range pl.Upcoming {
		if d.ReservationID == late[0] {
			lateDue = d.DueAt
		}
	}
	if !lateDue.Equal(now.Add(150 * time.Minute)) {
		t.Fatalf("due time for later reservation = %v", lateDue)
	}
}

func TestOnPublishStrategyStartsAtSlotTime(t *testing.T) {
	e := newEnv(t)
	e.policy(func(st *settings.Settings) { st.Policy.Generation.Timing = "on_publish" })
	ids := e.reservations(1, e.clk.Now().Add(5*time.Minute), 0)
	_ = e.planner.Tick(e.ctx)
	if len(e.requests()) != 0 {
		t.Fatal("must not generate before the slot time")
	}
	e.clk.Advance(5 * time.Minute)
	_ = e.planner.Tick(e.ctx)
	rs := e.requests()
	if len(rs) != 1 || *rs[0].ReservationID != ids[0] || rs[0].Trigger != "on_publish" {
		t.Fatalf("requests = %+v", rs)
	}
	var reason *string
	_ = e.pool.QueryRow(e.ctx, `SELECT wait_reason FROM reservations WHERE id = $1`, ids[0]).Scan(&reason)
	if reason == nil || *reason != "생성 중" {
		t.Fatalf("reservation should show it is generating: %v", reason)
	}
}

func TestStockStrategyRefillsToTargetOncePerHour(t *testing.T) {
	e := newEnv(t)
	e.policy(func(st *settings.Settings) {
		st.Policy.Generation.Timing = "prepare"
		st.Policy.Generation.PrepareStrategy = "stock"
		st.Policy.Generation.StockMin = 5
		st.Policy.Generation.StockTarget = 8
	})
	e.readyContents(2)
	_ = e.planner.Tick(e.ctx)
	rs := e.requests()
	if len(rs) != 1 || rs[0].Quantity != 6 || rs[0].Trigger != "stock" {
		t.Fatalf("requests = %+v", rs)
	}
	e.clk.Advance(10 * time.Minute)
	_ = e.planner.Tick(e.ctx)
	if len(e.requests()) != 1 {
		t.Fatal("stock refill must run at most once per hour")
	}
	e.drain()
	e.clk.Advance(time.Hour)
	_ = e.planner.Tick(e.ctx)
	if len(e.requests()) != 1 {
		t.Fatal("stock is at target; nothing to generate")
	}
}

func TestReviewModeCountsPendingAIAsSecured(t *testing.T) {
	e := newEnv(t)
	e.policy(func(st *settings.Settings) {
		st.OperationMode = settings.ModeReview
		st.Policy.Generation.Timing = "prepare"
		st.Policy.Generation.PrepareStrategy = "stock"
		st.Policy.Generation.StockMin = 3
		st.Policy.Generation.StockTarget = 3
	})
	_ = e.planner.Tick(e.ctx)
	e.drain()
	var pending int
	_ = e.pool.QueryRow(e.ctx, `SELECT count(*) FROM content_overview WHERE stage = 'review'`).Scan(&pending)
	if pending != 3 {
		t.Fatalf("generated contents must wait for review: %d", pending)
	}
	e.clk.Advance(time.Hour)
	_ = e.planner.Tick(e.ctx)
	if len(e.requests()) != 1 {
		t.Fatal("contents awaiting review must not be regenerated")
	}
}

func TestBlockersStopAutomaticGeneration(t *testing.T) {
	e := newEnv(t)
	e.policy(func(st *settings.Settings) {
		st.Policy.Generation.Timing = "prepare"
		st.Policy.Generation.PrepareStrategy = "stock"
		st.OperationMode = settings.ModePublishOnly
	})
	_ = e.planner.Tick(e.ctx)
	if len(e.requests()) != 0 {
		t.Fatal("publish-only mode must not generate")
	}
	if _, _, err := e.planner.FillNow(e.ctx, "button"); err == nil {
		t.Fatal("fill now must explain why it cannot run")
	}
	e.policy(func(st *settings.Settings) { st.OperationMode = settings.ModeFullAuto })
	off := false
	_, _ = e.settings.SetFlags(e.ctx, &off, nil, nil)
	_ = e.planner.Tick(e.ctx)
	if len(e.requests()) != 0 {
		t.Fatal("automation off must not generate automatically")
	}
}

func TestManualSubmitIsIdempotentAndCancelable(t *testing.T) {
	e := newEnv(t)
	in := generation.SubmitInput{IdempotencyKey: "tap-1", PromptID: e.promptID, Quantity: 5, Options: generation.Options{Text: true},
		Variables: map[string]string{"topic": "등대"}}
	r1, created, err := e.gen.Submit(e.ctx, in)
	if err != nil || !created {
		t.Fatal(err, created)
	}
	r2, created, err := e.gen.Submit(e.ctx, in)
	if err != nil || created || r2.ID != r1.ID {
		t.Fatalf("double submit = %v %v %v", r2.ID, created, err)
	}
	// 하나 처리 후 나머지 취소
	j, _ := e.queue.Claim(e.ctx, []string{generation.JobKind}, time.Minute)
	if res := e.gen.HandleItem(e.ctx, j); res.Outcome != jobs.Done {
		t.Fatalf("handle = %+v", res)
	}
	_ = e.queue.Complete(e.ctx, j)
	got, err := e.gen.Cancel(e.ctx, r1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "partial" || got.Produced != 1 || got.Failed != 4 || got.Queued != 0 {
		t.Fatalf("after cancel = %+v", got)
	}
	// 글/이미지 생성 독립 설정과 게시만 자동 모드
	e.policy(func(st *settings.Settings) { st.Policy.Generation.ImageEnabled = false })
	_, _, err = e.gen.Submit(e.ctx, generation.SubmitInput{IdempotencyKey: "img-off", Quantity: 1, Options: generation.Options{Image: true, ImagePrompt: "바다"}})
	var ve0 *generation.ValidationError
	if !errors.As(err, &ve0) || ve0.Fields["options.image"] == "" {
		t.Fatalf("image disabled = %v", err)
	}
	e.policy(func(st *settings.Settings) {
		st.OperationMode = settings.ModePublishOnly
		st.Policy.Generation.ImageEnabled = true
	})
	if _, _, err = e.gen.Submit(e.ctx, generation.SubmitInput{IdempotencyKey: "po-text", PromptID: e.promptID, Quantity: 1, Options: generation.Options{Text: true}}); err == nil {
		t.Fatal("publish-only mode must refuse text generation")
	}
	if _, _, err = e.gen.Submit(e.ctx, generation.SubmitInput{IdempotencyKey: "po-img", Quantity: 1, Options: generation.Options{Image: true, ImagePrompt: "바다"}}); err != nil {
		t.Fatalf("publish-only mode still allows image-only generation: %v", err)
	}
	e.policy(func(st *settings.Settings) { st.OperationMode = settings.ModeFullAuto })
	// 변수 검증
	_, _, err = e.gen.Submit(e.ctx, generation.SubmitInput{IdempotencyKey: "tap-2", Quantity: 0, Options: generation.Options{Text: true}})
	var ve *generation.ValidationError
	if !errors.As(err, &ve) || ve.Fields["quantity"] == "" || ve.Fields["prompt_id"] == "" {
		t.Fatalf("validation = %v", err)
	}
}

func TestFailuresDoNotProduceIncompleteContent(t *testing.T) {
	e := newEnv(t)
	// 이미지 실패 + 텍스트만 게시 비허용 → 글은 저장되지만 게시 불가
	e.mock.Fail = func(op string) error {
		if op == "image" {
			return &integration.Error{Class: integration.Review, Provider: "mock", Op: "image", Message: "이미지 정책 거절"}
		}
		return nil
	}
	r, _, err := e.gen.Submit(e.ctx, generation.SubmitInput{IdempotencyKey: "img-1", PromptID: e.promptID, Quantity: 1,
		Options: generation.Options{Text: true, Image: true}})
	if err != nil {
		t.Fatal(err)
	}
	e.drain()
	var stage string
	var publishable bool
	_ = e.pool.QueryRow(e.ctx, `SELECT stage, publishable FROM content_overview o JOIN contents c USING (id) WHERE c.generation_request_id = $1`, r.ID).Scan(&stage, &publishable)
	if publishable || stage != "draft" {
		t.Fatalf("image failure must keep content unpublishable: stage=%s publishable=%v", stage, publishable)
	}
	// 같은 실패라도 텍스트만 게시를 고르면 준비 완료
	r2, _, _ := e.gen.Submit(e.ctx, generation.SubmitInput{IdempotencyKey: "img-2", PromptID: e.promptID, Quantity: 1,
		Options: generation.Options{Text: true, Image: true, TextOnlyFallback: true}})
	e.drain()
	_ = e.pool.QueryRow(e.ctx, `SELECT stage, publishable FROM content_overview o JOIN contents c USING (id) WHERE c.generation_request_id = $1`, r2.ID).Scan(&stage, &publishable)
	if !publishable || stage != "ready" {
		t.Fatalf("text-only fallback = stage %s publishable %v", stage, publishable)
	}

	// 일시 오류는 재시도 후 성공, 설정 오류는 재시도 없이 실패 + 알림
	calls := 0
	e.mock.Fail = func(op string) error {
		calls++
		if calls == 1 {
			return &integration.Error{Class: integration.Retryable, Provider: "mock", Op: "text", Message: "잠시 후 다시"}
		}
		return nil
	}
	r3, _, _ := e.gen.Submit(e.ctx, generation.SubmitInput{IdempotencyKey: "retry-1", PromptID: e.promptID, Quantity: 1, Options: generation.Options{Text: true}})
	e.drain()
	if got, _ := e.gen.Get(e.ctx, r3.ID); got.State != "succeeded" || calls != 2 {
		t.Fatalf("retryable = %+v calls %d", got, calls)
	}
	e.mock.Fail = func(op string) error {
		return &integration.Error{Class: integration.Config, Provider: "mock", Op: "text", Message: "API 키가 올바르지 않아요"}
	}
	r4, _, _ := e.gen.Submit(e.ctx, generation.SubmitInput{IdempotencyKey: "cfg-1", PromptID: e.promptID, Quantity: 2, Options: generation.Options{Text: true}})
	e.drain()
	got, _ := e.gen.Get(e.ctx, r4.ID)
	if got.State != "failed" || got.Failed != 2 || got.Error == nil {
		t.Fatalf("config failure = %+v", got)
	}
	var crit int
	_ = e.pool.QueryRow(e.ctx, `SELECT count(*) FROM notifications WHERE group_key = 'generation:config' AND resolved_at IS NULL`).Scan(&crit)
	if crit != 1 {
		t.Fatalf("config failure must leave one grouped notification, got %d", crit)
	}
}

func TestBudgetLimitBlocksGenerationBeforeCall(t *testing.T) {
	e := newEnv(t)
	e.policy(func(st *settings.Settings) {
		st.Policy.Budget.MonthlyLimitMicros = 10_000
		st.Policy.Pricing = settings.Pricing{Currency: "USD", Version: "test", VerifiedAt: "2026-03-01", TextInputPerMTok: 5_000_000, TextOutputPerMTok: 5_000_000}
	})
	calls := 0
	e.mock.Fail = func(string) error { calls++; return nil }
	r, _, _ := e.gen.Submit(e.ctx, generation.SubmitInput{IdempotencyKey: "b-1", PromptID: e.promptID, Quantity: 3, Options: generation.Options{Text: true}})
	e.drain()
	got, _ := e.gen.Get(e.ctx, r.ID)
	// 추정 27,500 µ$ (입력 3,000 + 출력 상한 2,500 토큰) > 한도 10,000 µ$ → 외부 호출 없이 실패
	if calls != 0 || got.Failed != 3 || got.State != "failed" {
		t.Fatalf("budget = %+v calls %d", got, calls)
	}
}

func TestRegenerateKeepsCurrentTextUntilApplied(t *testing.T) {
	e := newEnv(t)
	d, _ := e.contents.Create(e.ctx, content.NewContent{RawText: readyText})
	r, _, err := e.gen.Submit(e.ctx, generation.SubmitInput{IdempotencyKey: "re-1", Quantity: 1, Trigger: "regenerate", ContentID: &d.ID,
		Options: generation.Options{Text: true, Target: "part", PartIndex: 1, Instruction: "짧게"}})
	if err != nil {
		t.Fatal(err)
	}
	e.drain()
	if got, _ := e.gen.Get(e.ctx, r.ID); got.State != "succeeded" {
		t.Fatalf("regenerate = %+v", got)
	}
	after, _ := e.contents.Get(e.ctx, d.ID)
	if after.Revision.RawText != readyText {
		t.Fatal("regeneration must not overwrite the current text")
	}
	res, err := e.gen.Results(e.ctx, d.ID)
	if err != nil || len(res) != 1 || res[0].Target != "part" || res[0].PartIndex == nil || *res[0].PartIndex != 1 {
		t.Fatalf("results = %+v %v", res, err)
	}
	applied, err := e.gen.ApplyResult(e.ctx, d.ID, res[0].ID, after.EditVersion)
	if err != nil {
		t.Fatal(err)
	}
	if applied.Revision.RawText == readyText || len(applied.Revision.Parts) != 2 || applied.Revision.Parts[0].Text != after.Revision.Parts[0].Text {
		t.Fatalf("applied = %q", applied.Revision.RawText)
	}
	if _, err := e.gen.ApplyResult(e.ctx, d.ID, res[0].ID, applied.EditVersion); err == nil {
		t.Fatal("a result can be applied only once")
	}
}

func TestPromptWithoutDefaultsBlocksAutomaticGeneration(t *testing.T) {
	e := newEnv(t)
	p, err := e.prompts.Create(e.ctx, prompt.Input{Name: "필수", Body: "{{topic}} 이야기", Variables: []prompt.Variable{{Name: "topic", Label: "주제", Type: "text", Required: true}}})
	if err != nil {
		t.Fatal(err)
	}
	e.policy(func(st *settings.Settings) { st.Policy.Generation.DefaultPromptID = p.ID })
	pl, _ := e.planner.Preview(e.ctx)
	if pl.Active || len(pl.Blockers) == 0 {
		t.Fatalf("plan must explain the missing default: %+v", pl.Blockers)
	}
	_, _, err = e.planner.FillNow(e.ctx, "button")
	var ve *generation.ValidationError
	if !errors.As(err, &ve) || ve.Fields["generation"] == "" {
		t.Fatalf("fill now = %v", err)
	}
}
