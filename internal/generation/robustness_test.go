package generation_test

import (
	"strings"
	"testing"
	"time"

	"github.com/yuntyu01/x-automation/internal/content"
	"github.com/yuntyu01/x-automation/internal/generation"
	"github.com/yuntyu01/x-automation/internal/integration"
	"github.com/yuntyu01/x-automation/internal/jobs"
	"github.com/yuntyu01/x-automation/internal/settings"
)

func (e *env) stage(contentID string) string {
	e.t.Helper()
	var s string
	if err := e.pool.QueryRow(e.ctx, `SELECT stage FROM content_overview WHERE id = $1`, contentID).Scan(&s); err != nil {
		e.t.Fatal(err)
	}
	return s
}

// 재생성은 제안만 만든다: 진행 중이거나 실패해도 지금 글은 그대로 게시 준비 상태다
func TestRegenerationNeverChangesContentStage(t *testing.T) {
	e := newEnv(t)
	d, _ := e.contents.Create(e.ctx, content.NewContent{RawText: readyText})
	if got := e.stage(d.ID); got != "ready" {
		t.Fatalf("before = %s", got)
	}
	e.mock.Fail = func(op string) error {
		return &integration.Error{Class: integration.Review, Provider: "mock", Op: op, Message: "거절"}
	}
	r, _, err := e.gen.Submit(e.ctx, generation.SubmitInput{IdempotencyKey: "re-fail", Quantity: 1, Trigger: "regenerate", ContentID: &d.ID,
		Options: generation.Options{Text: true, Instruction: "짧게"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := e.stage(d.ID); got != "ready" {
		t.Fatalf("queued regeneration moved content to %s", got)
	}
	e.drain()
	if got, _ := e.gen.Get(e.ctx, r.ID); got.State != "failed" {
		t.Fatalf("request = %s", got.State)
	}
	if got := e.stage(d.ID); got != "ready" {
		t.Fatalf("failed regeneration moved content to %s", got)
	}
}

// 기본 나누기가 '직접 나누기'여도 AI 글은 구분선이 없으니 두 부분으로 나눠 게시할 수 있게 한다
func TestManualSplitPolicyStillSplitsAIOutput(t *testing.T) {
	e := newEnv(t)
	e.policy(func(st *settings.Settings) { st.Policy.Thread.SplitMode = "manual" })
	r, _, err := e.gen.Submit(e.ctx, generation.SubmitInput{IdempotencyKey: "manual-1", PromptID: e.promptID, Quantity: 1, Options: generation.Options{Text: true}})
	if err != nil {
		t.Fatal(err)
	}
	e.drain()
	var mode string
	var parts int
	var publishable bool
	if err := e.pool.QueryRow(e.ctx, `SELECT o.split_mode, o.part_count, o.publishable FROM content_overview o JOIN contents c USING (id)
		WHERE c.generation_request_id = $1`, r.ID).Scan(&mode, &parts, &publishable); err != nil {
		t.Fatal(err)
	}
	if mode != "two_parts" || parts < 2 || !publishable {
		t.Fatalf("mode=%s parts=%d publishable=%v", mode, parts, publishable)
	}
}

func (e *env) heldMicros() int64 {
	e.t.Helper()
	var n int64
	if err := e.pool.QueryRow(e.ctx, `SELECT COALESCE(sum(amount_micros), 0) FROM budget_reservations WHERE state = 'held'`).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// 글 비용을 기록해도 이미지 호출이 끝날 때까지 예약을 붙잡아 다른 작업이 한도를 넘지 못하게 한다
func TestBudgetStaysHeldUntilImagesFinish(t *testing.T) {
	e := newEnv(t)
	e.policy(func(st *settings.Settings) {
		st.Policy.Budget.MonthlyLimitMicros = 10_000_000
		st.Policy.Pricing = settings.Pricing{Currency: "USD", Version: "test", VerifiedAt: "2026-03-01",
			TextInputPerMTok: 1_000_000, TextOutputPerMTok: 1_000_000, ImagePerImage: 40_000}
	})
	var heldDuringImage int64 = -1
	e.mock.Fail = func(op string) error {
		if op == "image" {
			heldDuringImage = e.heldMicros()
		}
		return nil
	}
	if _, _, err := e.gen.Submit(e.ctx, generation.SubmitInput{IdempotencyKey: "hold-1", PromptID: e.promptID, Quantity: 1,
		Options: generation.Options{Text: true, Image: true}}); err != nil {
		t.Fatal(err)
	}
	e.drain()
	if heldDuringImage <= 0 {
		t.Fatalf("held during image call = %d, want the reservation still held", heldDuringImage)
	}
	if left := e.heldMicros(); left != 0 {
		t.Fatalf("held after job = %d", left)
	}
}

// 끊긴 요청은 과금됐을 수 있다. 사용량을 못 받아도 0이 아니라 추정치를 '불명확'으로 남긴다
func TestInterruptedCallRecordsEstimatedUncertainCost(t *testing.T) {
	e := newEnv(t)
	e.policy(func(st *settings.Settings) {
		st.Policy.Generation.MaxAttempts = 2
		st.Policy.Pricing = settings.Pricing{Currency: "USD", Version: "test", VerifiedAt: "2026-03-01", TextInputPerMTok: 1_000_000, TextOutputPerMTok: 1_000_000}
	})
	e.mock.Fail = func(op string) error {
		return &integration.Error{Class: integration.Retryable, Provider: "mock", Op: op, Message: "시간 초과"}
	}
	if _, _, err := e.gen.Submit(e.ctx, generation.SubmitInput{IdempotencyKey: "timeout-1", PromptID: e.promptID, Quantity: 1, Options: generation.Options{Text: true}}); err != nil {
		t.Fatal(err)
	}
	e.drain()
	var rows int
	var zero int
	if err := e.pool.QueryRow(e.ctx, `SELECT count(*), count(*) FILTER (WHERE estimated_micros = 0) FROM usage_ledger WHERE certainty = 'uncertain'`).Scan(&rows, &zero); err != nil {
		t.Fatal(err)
	}
	if rows != 2 || zero != 0 {
		t.Fatalf("uncertain rows = %d (zero-cost %d), want 2 with estimates", rows, zero)
	}
}

// 작업이 실행 중에 끊겨 시도 횟수를 다 쓰면 요청도 실패로 닫힌다 ('진행 중'에 남아 부족분 계산을 막지 않게)
func TestOrphanedRequestIsClosed(t *testing.T) {
	e := newEnv(t)
	e.policy(func(st *settings.Settings) { st.Policy.Generation.MaxAttempts = 1 })
	r, _, err := e.gen.Submit(e.ctx, generation.SubmitInput{IdempotencyKey: "orphan-1", PromptID: e.promptID, Quantity: 1, Options: generation.Options{Text: true}})
	if err != nil {
		t.Fatal(err)
	}
	// 워커가 작업을 잡은 채 죽었다
	if j, err := e.queue.Claim(e.ctx, []string{generation.JobKind}, time.Minute); err != nil || j == nil {
		t.Fatalf("claim = %v %v", j, err)
	}
	if n, _ := e.gen.CloseOrphaned(e.ctx); n != 0 {
		t.Fatalf("running job must not be closed, closed %d", n)
	}
	e.clk.Advance(2 * time.Minute)
	if _, _, err := e.queue.RecoverExpired(e.ctx, func(string) jobs.RecoverPolicy { return jobs.RecoverRequeue }); err != nil {
		t.Fatal(err)
	}
	if n, err := e.gen.CloseOrphaned(e.ctx); err != nil || n != 1 {
		t.Fatalf("closed = %d %v", n, err)
	}
	if got, _ := e.gen.Get(e.ctx, r.ID); got.State != "failed" || got.Failed != 1 {
		t.Fatalf("request = %s failed=%d", got.State, got.Failed)
	}
}

// 번호 표기를 켠 글에 부분 재생성을 적용해도 "(1/2)"가 두 번 붙지 않는다
func TestApplyPartResultKeepsSingleNumbering(t *testing.T) {
	e := newEnv(t)
	d, err := e.contents.Create(e.ctx, content.NewContent{RawText: readyText, SplitOptions: content.SplitOptions{Numbering: true}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(d.Revision.Parts[0].Text, "\n(1/2)") {
		t.Fatalf("setup: part 0 = %q", d.Revision.Parts[0].Text)
	}
	if _, _, err := e.gen.Submit(e.ctx, generation.SubmitInput{IdempotencyKey: "num-1", Quantity: 1, Trigger: "regenerate", ContentID: &d.ID,
		Options: generation.Options{Text: true, Target: "part", PartIndex: 1, Instruction: "짧게"}}); err != nil {
		t.Fatal(err)
	}
	e.drain()
	res, err := e.gen.Results(e.ctx, d.ID)
	if err != nil || len(res) != 1 {
		t.Fatalf("results = %v %v", res, err)
	}
	applied, err := e.gen.ApplyResult(e.ctx, d.ID, res[0].ID, d.EditVersion)
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range applied.Revision.Parts {
		if n := strings.Count(p.Text, "/2)"); n != 1 {
			t.Fatalf("part %d has %d number marks: %q", i, n, p.Text)
		}
	}
}
