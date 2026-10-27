package dashboard_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yuntyu01/x-automation/internal/budget"
	"github.com/yuntyu01/x-automation/internal/clock"
	"github.com/yuntyu01/x-automation/internal/content"
	"github.com/yuntyu01/x-automation/internal/dashboard"
	"github.com/yuntyu01/x-automation/internal/notification"
	"github.com/yuntyu01/x-automation/internal/randx"
	"github.com/yuntyu01/x-automation/internal/schedule"
	"github.com/yuntyu01/x-automation/internal/settings"
	"github.com/yuntyu01/x-automation/internal/testutil/pgtest"
)

var seoul, _ = time.LoadLocation("Asia/Seoul")

type env struct {
	t        *testing.T
	ctx      context.Context
	pool     *pgxpool.Pool
	clk      *clock.Fake
	settings *settings.Repo
	notify   *notification.Service
	budget   *budget.Service
	sched    *schedule.Service
	contents *content.Service
	dash     *dashboard.Service
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool := pgtest.New(t)
	clk := clock.NewFake(time.Date(2026, 10, 27, 12, 5, 0, 0, seoul))
	sr := settings.NewRepo(pool)
	n := notification.NewService(pool)
	b := budget.NewService(pool, clk, sr)
	e := &env{t: t, ctx: context.Background(), pool: pool, clk: clk, settings: sr, notify: n, budget: b,
		sched: schedule.NewService(pool, clk, randx.NewSeeded(7), sr, n), contents: content.NewService(pool, clk, nil),
		dash: dashboard.NewService(dashboard.Deps{Pool: pool, Clock: clk, Settings: sr, Budget: b, Notify: n, Live: true, PublishingEnabled: false})}
	st, _ := sr.Get(e.ctx)
	if _, err := sr.Update(e.ctx, st.Version, settings.ModeFullAuto, st.Timezone, st.Policy); err != nil {
		t.Fatal(err)
	}
	if _, err := e.sched.Ensure(e.ctx); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) content(raw string) string {
	e.t.Helper()
	d, err := e.contents.Create(e.ctx, content.NewContent{RawText: raw, SplitMode: "manual"})
	if err != nil {
		e.t.Fatal(err)
	}
	return d.ID
}

func (e *env) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.pool.Exec(e.ctx, q, args...); err != nil {
		e.t.Fatal(err)
	}
}

// pastRun은 오늘 이미 끝난(또는 일부 끝난) 게시를 만든다.
func (e *env) pastRun(state, resState string, ago time.Duration, parts []string) {
	e.t.Helper()
	cid := e.content("지난 글.\n---\n답글.")
	at := e.clk.Now().Add(-ago)
	var rid, runID string
	if err := e.pool.QueryRow(e.ctx, `INSERT INTO reservations (slot_at, original_slot_at, local_date, content_id, revision_id, state)
		SELECT $1, $1, $2::date, id, current_revision_id, $4 FROM contents WHERE id = $3 RETURNING id`, at, at.In(seoul).Format("2006-01-02"), cid, resState).Scan(&rid); err != nil {
		e.t.Fatal(err)
	}
	if err := e.pool.QueryRow(e.ctx, `INSERT INTO publish_runs (reservation_id, content_id, revision_id, trigger, state)
		SELECT $1, id, current_revision_id, 'schedule', $3 FROM contents WHERE id = $2 RETURNING id`, rid, cid, state).Scan(&runID); err != nil {
		e.t.Fatal(err)
	}
	for i, ps := range parts {
		e.exec(`INSERT INTO publish_parts (run_id, position, text, state, succeeded_at) VALUES ($1, $2, 't', $3, CASE WHEN $3 = 'succeeded' THEN $4::timestamptz END)`,
			runID, i, ps, at)
	}
}

func TestHomeReflectsDatabaseState(t *testing.T) {
	e := newEnv(t)
	a := e.content("다음에 올라갈 글.\n---\n답글.")
	if err := e.sched.Tick(e.ctx); err != nil { // 배치·준비 상태 갱신
		t.Fatal(err)
	}
	// 생성을 멈춰 두면 생성 예정인 빈 예약도 "넣을 글이 없는 예약"으로 센다
	paused := true
	if _, err := e.settings.SetFlags(e.ctx, nil, &paused, nil); err != nil {
		t.Fatal(err)
	}
	e.pastRun("completed", "published", 3*time.Hour, []string{"succeeded", "succeeded"})
	e.pastRun("partial", "partial", 2*time.Hour, []string{"succeeded", "failed", "pending"})

	h, err := e.dash.Home(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if h.Next == nil || h.Next.ContentID == nil || *h.Next.ContentID != a || h.Next.State != "assigned" {
		t.Fatalf("next = %+v", h.Next)
	}
	// 가까운 예약은 빈 예약도 보여 준다 (다음 게시보다 이른 빈 자리 포함, 다음 게시는 빼고)
	if len(h.Upcoming) != 5 || h.Upcoming[0].State != "open" || !h.Upcoming[0].SlotAt.Before(h.Next.SlotAt) {
		t.Fatalf("upcoming = %+v", h.Upcoming)
	}
	for _, u := range h.Upcoming {
		if u.ID == h.Next.ID {
			t.Fatal("next duplicated in upcoming")
		}
	}
	if h.Today.Date != "2026-10-27" || h.Today.Target != 20 || h.Today.Published != 1 || h.Today.InProgress != 1 || h.Today.Posts != 3 || h.Today.Remaining == 0 {
		t.Fatalf("today = %+v", h.Today)
	}
	keys := map[string]dashboard.Action{}
	for _, x := range h.Actions {
		keys[x.Key] = x
	}
	if keys["publish_partial"].Count != 1 || keys["empty_soon"].Count == 0 {
		t.Fatalf("actions = %+v", h.Actions)
	}
	if _, ok := keys["x_connection"]; ok {
		t.Fatal("자동 운영 전에는 미연결을 조치로 띄우지 않는다")
	}
	joined := strings.Join(h.Automation.PublishBlockers, "|")
	for _, want := range []string{"실제 게시가 꺼져", "자동 운영", "X 계정"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("publish blockers missing %q: %v", want, h.Automation.PublishBlockers)
		}
	}
	// 자동 운영을 켜면 미연결이 중대 조치가 된다
	on := true
	_, _ = e.settings.SetFlags(e.ctx, &on, nil, nil)
	h, _ = e.dash.Home(e.ctx)
	found := false
	for _, x := range h.Actions {
		if x.Key == "x_connection" && x.Severity == "critical" {
			found = true
		}
	}
	if !found {
		t.Fatalf("x connection action missing: %+v", h.Actions)
	}
}

func (e *env) unread(group string) []notification.Notification {
	list, _ := e.notify.List(e.ctx, notification.ListOptions{Limit: 100})
	var out []notification.Notification
	for _, n := range list {
		if n.GroupKey == group && n.ResolvedAt == nil {
			out = append(out, n)
		}
	}
	return out
}

func TestWatchShortageNotificationIsStateful(t *testing.T) {
	e := newEnv(t)
	if err := e.dash.Watch(e.ctx); err != nil {
		t.Fatal(err)
	}
	if n := e.unread("stock:shortage"); len(n) != 0 {
		t.Fatal("자동 운영 전에는 부족 알림을 만들지 않는다")
	}
	on := true
	_, _ = e.settings.SetFlags(e.ctx, &on, nil, nil)
	for i := 0; i < 5; i++ { // 매분 점검을 여러 번
		if err := e.dash.Watch(e.ctx); err != nil {
			t.Fatal(err)
		}
	}
	n := e.unread("stock:shortage")
	if len(n) != 1 || n[0].Count != 1 || !strings.Contains(n[0].Body, "자동 생성이 멈춰") {
		t.Fatalf("shortage = %+v", n)
	}
	// 읽어도 상태가 계속되는 동안 새로 쌓이지 않는다
	_ = e.notify.MarkRead(e.ctx, n[0].ID)
	_ = e.dash.Watch(e.ctx)
	if c, _ := e.notify.Counts(e.ctx); c.Unread != 0 {
		t.Fatalf("unread after read = %d", c.Unread)
	}
	// 글을 채우면 풀린다
	for i := 0; i < 12; i++ {
		e.content("채우는 글입니다.\n---\n답글.")
	}
	if _, err := e.sched.AutoAssign(e.ctx); err != nil {
		t.Fatal(err)
	}
	_ = e.dash.Watch(e.ctx)
	if n := e.unread("stock:shortage"); len(n) != 0 {
		rows, _ := e.pool.Query(e.ctx, `SELECT slot_at, state, COALESCE(wait_reason, ''), generation_due_at IS NOT NULL FROM reservations WHERE slot_at < $1 ORDER BY slot_at`, e.clk.Now().Add(4*time.Hour))
		for rows.Next() {
			var at time.Time
			var st, wr string
			var g bool
			_ = rows.Scan(&at, &st, &wr, &g)
			t.Logf("%s %s %s %v", at.In(seoul).Format("15:04"), st, wr, g)
		}
		rows.Close()
		t.Fatalf("still open after filling: %+v", n)
	}
}

func TestBudgetWarnAndExceeded(t *testing.T) {
	e := newEnv(t)
	st, _ := e.settings.Get(e.ctx)
	p := st.Policy
	p.Budget.MonthlyLimitMicros, p.Budget.OnLimit = 1_000_000, "stop_all"
	if _, err := e.settings.Update(e.ctx, st.Version, st.OperationMode, st.Timezone, p); err != nil {
		t.Fatal(err)
	}
	// 요율표가 비어 있으면 한도가 제대로 작동하지 않는다고 알린다
	h, _ := e.dash.Home(e.ctx)
	if !hasAction(h, "pricing") {
		t.Fatalf("pricing action missing: %+v", h.Actions)
	}
	_ = e.budget.Record(e.ctx, budget.Entry{Category: budget.AIText, RefKey: "a", Micros: 850_000}, "")
	_ = e.dash.Watch(e.ctx)
	if len(e.unread("budget:warn")) != 1 || len(e.unread("budget:exceeded")) != 0 {
		t.Fatal("80% warning expected")
	}
	_ = e.budget.Record(e.ctx, budget.Entry{Category: budget.XPost, RefKey: "b", Micros: 200_000}, "")
	_ = e.dash.Watch(e.ctx)
	ex := e.unread("budget:exceeded")
	if len(ex) != 1 || ex[0].Severity != notification.Critical || len(e.unread("budget:warn")) != 0 {
		t.Fatalf("exceeded = %+v", ex)
	}
	h, _ = e.dash.Home(e.ctx)
	if !strings.Contains(strings.Join(h.Automation.PublishBlockers, "|"), "예산") || !strings.Contains(strings.Join(h.Automation.GenerationBlockers, "|"), "예산") {
		t.Fatalf("blockers = %+v", h.Automation)
	}
	if h.Usage.Counts["x_post"] != 1 || h.Usage.ByCategory["ai_text"] != 850_000 {
		t.Fatalf("usage split = %+v", h.Usage)
	}
	// 한도를 올리면 풀린다
	st, _ = e.settings.Get(e.ctx)
	p = st.Policy
	p.Budget.MonthlyLimitMicros = 10_000_000
	_, _ = e.settings.Update(e.ctx, st.Version, st.OperationMode, st.Timezone, p)
	_ = e.dash.Watch(e.ctx)
	if len(e.unread("budget:exceeded")) != 0 || len(e.unread("budget:warn")) != 0 {
		t.Fatal("budget alerts should resolve")
	}
}

func hasAction(h dashboard.Home, key string) bool {
	for _, a := range h.Actions {
		if a.Key == key {
			return true
		}
	}
	return false
}
