package schedule_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yuntyu01/x-automation/internal/clock"
	"github.com/yuntyu01/x-automation/internal/content"
	"github.com/yuntyu01/x-automation/internal/notification"
	"github.com/yuntyu01/x-automation/internal/randx"
	"github.com/yuntyu01/x-automation/internal/schedule"
	"github.com/yuntyu01/x-automation/internal/settings"
	"github.com/yuntyu01/x-automation/internal/testutil/pgtest"
)

var seoul, _ = time.LoadLocation("Asia/Seoul")

func kst(m time.Month, d, h, min int) time.Time { return time.Date(2026, m, d, h, min, 0, 0, seoul) }

type env struct {
	t        *testing.T
	ctx      context.Context
	pool     *pgxpool.Pool
	clk      *clock.Fake
	svc      *schedule.Service
	contents *content.Service
	settings *settings.Repo
}

func newEnv(t *testing.T, now time.Time) *env {
	t.Helper()
	pool := pgtest.New(t)
	clk := clock.NewFake(now)
	sr := settings.NewRepo(pool)
	e := &env{t: t, ctx: context.Background(), pool: pool, clk: clk, settings: sr,
		svc:      schedule.NewService(pool, clk, randx.NewSeeded(42), sr, notification.NewService(pool)),
		contents: content.NewService(pool, clk, nil)}
	e.mode(settings.ModeFullAuto)
	return e
}

func (e *env) mode(m settings.OperationMode) {
	st, _ := e.settings.Get(e.ctx)
	if _, err := e.settings.Update(e.ctx, st.Version, m, st.Timezone, st.Policy); err != nil {
		e.t.Fatal(err)
	}
}

type res struct {
	id      string
	at      time.Time
	date    string
	state   string
	content *string
	jitter  int
	note    *string
}

func (e *env) reservations(states ...string) []res {
	e.t.Helper()
	if len(states) == 0 {
		states = []string{"open", "assigned", "waiting"}
	}
	rows, err := e.pool.Query(e.ctx, `SELECT id, slot_at, to_char(local_date, 'YYYY-MM-DD'), state, content_id, jitter_seconds, delay_note
		FROM reservations WHERE state = ANY($1) ORDER BY slot_at`, states)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []res
	for rows.Next() {
		var r res
		if err := rows.Scan(&r.id, &r.at, &r.date, &r.state, &r.content, &r.jitter, &r.note); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

const story = "그날 밤 나는 오래된 편지를 다시 꺼내 읽었다.\n\n창밖으로 비가 내리고 있었다. 나는 답장을 쓰기로 했다."

func (e *env) ready(n int) []string {
	var ids []string
	for i := 0; i < n; i++ {
		d, err := e.contents.Create(e.ctx, content.NewContent{RawText: fmt.Sprintf("%s %d", story, i)})
		if err != nil {
			e.t.Fatal(err)
		}
		ids = append(ids, d.ID)
		e.clk.Advance(time.Second) // 만든 순서가 분명하게
	}
	return ids
}

// 기본 설정(새벽 0~6시, 낮 12~17시, 30분 + 0~5분, 하루 20개)으로 미리 만들고, 재시작해도 시각이 바뀌지 않는다
func TestEnsureIsPersistentAndCapped(t *testing.T) {
	e := newEnv(t, kst(3, 2, 9, 0)) // 월요일 09:00
	n, err := e.svc.Ensure(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	all := e.reservations()
	perDay := map[string]int{}
	for i, r := range all {
		perDay[r.date]++
		if i > 0 {
			gap := r.at.Sub(all[i-1].at)
			if r.date == all[i-1].date && r.at.In(seoul).Hour() >= 12 == (all[i-1].at.In(seoul).Hour() >= 12) && (gap < 30*time.Minute || gap > 35*time.Minute) {
				t.Fatalf("gap %v between %s and %s", gap, all[i-1].at.In(seoul), r.at.In(seoul))
			}
		}
	}
	// 오늘은 09:00 이후라 낮 구간만 (최대 10개), 내일·모레는 20개
	if n != len(all) || perDay["2026-03-02"] > 10 || perDay["2026-03-03"] != 20 || perDay["2026-03-04"] != 20 {
		t.Fatalf("per day = %v (n=%d)", perDay, n)
	}
	if all[0].at.In(seoul).Format("15:04") != "12:00" {
		t.Fatalf("first slot today = %s", all[0].at.In(seoul))
	}
	// 재시작: 다른 난수로 새 서비스를 만들어 다시 실행해도 기존 시각은 그대로, 새로 만들지 않는다
	restarted := schedule.NewService(e.pool, e.clk, randx.NewSeeded(999), e.settings, nil)
	for i := 0; i < 3; i++ {
		if n, err := restarted.Ensure(e.ctx); err != nil || n != 0 {
			t.Fatalf("re-ensure created %d (%v)", n, err)
		}
	}
	again := e.reservations()
	for i := range all {
		if !again[i].at.Equal(all[i].at) || again[i].jitter != all[i].jitter {
			t.Fatal("slot times changed after restart")
		}
	}
	// 하루가 지나면 그 다음 날만 덧붙인다
	e.clk.Advance(24 * time.Hour)
	if n, _ := restarted.Ensure(e.ctx); n != 20 {
		t.Fatalf("next day appended %d", n)
	}
}

// 구간에 다 들어가지 않는 수량은 억지로 채우지 않고 부족 수를 알려 준다
func TestShortfallIsReportedNotForced(t *testing.T) {
	e := newEnv(t, kst(3, 2, 9, 0))
	set, _ := e.svc.Settings(e.ctx)
	set.Rules.DailyTarget = 20
	windows := []schedule.Window{{Label: "낮", Weekdays: []int{0, 1, 2, 3, 4, 5, 6}, StartMinute: 720, EndMinute: 960, Enabled: true}} // 12~16시
	rep, err := e.svc.SaveRules(e.ctx, schedule.RulesInput{Rules: set.Rules, Windows: windows, Scope: "now", Seed: 7}, true)
	if err != nil {
		t.Fatal(err)
	}
	var tomorrow schedule.DayPlan
	for _, d := range rep.Days {
		if d.Date == "2026-03-03" {
			tomorrow = d
		}
	}
	if tomorrow.Count != 8 || tomorrow.Shortfall != 12 || tomorrow.CapacityMax != 8 {
		t.Fatalf("tomorrow = %+v", tomorrow)
	}
	// 미리보기는 저장하지 않는다
	if s2, _ := e.svc.Settings(e.ctx); len(s2.Windows) != 2 {
		t.Fatal("dry run must not save")
	}
}

// 준비된 글을 오래된 순으로 빈 예약에 넣고, 검토 모드는 승인된 글만, 사용자가 뺀 글은 다시 넣지 않는다
func TestAutoAssignUsesReadyContentInOrder(t *testing.T) {
	e := newEnv(t, kst(3, 2, 9, 0))
	_, _ = e.svc.Ensure(e.ctx)
	ids := e.ready(3)
	if n, err := e.svc.AutoAssign(e.ctx); err != nil || n != 3 {
		t.Fatalf("assigned %d %v", n, err)
	}
	all := e.reservations()
	for i := 0; i < 3; i++ {
		if all[i].content == nil || *all[i].content != ids[i] {
			t.Fatalf("slot %d has %v want %s", i, all[i].content, ids[i])
		}
	}
	_ = e.svc.Refresh(e.ctx)
	if got := e.reservations("assigned"); len(got) != 3 {
		t.Fatalf("assigned state = %d", len(got))
	}
	// 예약 해제한 글은 다시 자동 배치하지 않는다
	if err := e.svc.Unassign(e.ctx, all[0].id); err != nil {
		t.Fatal(err)
	}
	_, _ = e.svc.AutoAssign(e.ctx)
	for _, r := range e.reservations() {
		if r.content != nil && *r.content == ids[0] {
			t.Fatal("held content was auto-assigned again")
		}
	}
	// 검토 모드: 미승인 글은 배치하지 않는다
	e.mode(settings.ModeReview)
	more := e.ready(1)
	_, _ = e.svc.AutoAssign(e.ctx)
	for _, r := range e.reservations() {
		if r.content != nil && *r.content == more[0] {
			t.Fatal("unapproved content auto-assigned in review mode")
		}
	}
	d, _ := e.contents.Get(e.ctx, more[0])
	if _, err := e.contents.Approve(e.ctx, d.ID, d.CurrentRevisionID); err != nil {
		t.Fatal(err)
	}
	if n, _ := e.svc.AutoAssign(e.ctx); n != 1 {
		t.Fatal("approved content should be assigned")
	}
}

// 서버가 멈췄다 돌아와도 밀린 예약을 한꺼번에 실행하지 않는다: 지난 예약은 닫고, 준비된 글은 다음 빈 예약으로
func TestOverdueSlotsAreClosedAndContentMovesForward(t *testing.T) {
	e := newEnv(t, kst(3, 2, 9, 0))
	_, _ = e.svc.Ensure(e.ctx)
	ids := e.ready(2)
	_, _ = e.svc.AutoAssign(e.ctx)
	on := true
	_, _ = e.settings.SetFlags(e.ctx, &on, nil, nil)
	// 13:30까지 멈춰 있었다 (12:00, 12:3x, 13:0x 지나감)
	e.clk.Set(kst(3, 2, 13, 30))
	if err := e.svc.Tick(e.ctx); err != nil {
		t.Fatal(err)
	}
	closed := e.reservations("canceled")
	if len(closed) != 3 {
		t.Fatalf("closed = %d", len(closed))
	}
	for _, r := range e.reservations() {
		if r.at.Before(e.clk.Now()) {
			t.Fatalf("past slot %s still pending", r.at.In(seoul))
		}
	}
	upcoming := e.reservations()
	if upcoming[0].content == nil || *upcoming[0].content != ids[0] || upcoming[1].content == nil || *upcoming[1].content != ids[1] {
		t.Fatal("ready contents should move to the next open slots in order")
	}
	var notes int
	_ = e.pool.QueryRow(e.ctx, `SELECT count(*) FROM notifications WHERE group_key = 'schedule:missed'`).Scan(&notes)
	if notes != 1 {
		t.Fatal("missed slots should leave one grouped notification")
	}
}

// 실제 게시가 늦어지면 다음 미실행 예약을 저장된 지연으로 늦춰 간격을 지킨다 (랜덤을 다시 뽑지 않는다)
func TestLatePostShiftsFollowingSlotsWithStoredJitter(t *testing.T) {
	e := newEnv(t, kst(3, 2, 9, 0))
	_, _ = e.svc.Ensure(e.ctx)
	before := e.reservations()
	first := before[0]
	// 12:00 예약이 12:20에 실제로 시작됐다
	if _, err := e.pool.Exec(e.ctx, `UPDATE reservations SET state = 'publishing' WHERE id = $1`, first.id); err != nil {
		t.Fatal(err)
	}
	moved, err := e.svc.AfterBodyStart(e.ctx, first.id, kst(3, 2, 12, 20))
	if err != nil || moved == 0 {
		t.Fatalf("moved %d %v", moved, err)
	}
	after := e.reservations()
	prev := kst(3, 2, 12, 20)
	for i, r := range after[:3] {
		want := prev.Add(30*time.Minute + time.Duration(before[i+1].jitter)*time.Second)
		if r.at.Before(want) {
			t.Fatalf("slot %d at %s, need >= %s", i, r.at.In(seoul), want.In(seoul))
		}
		if r.jitter != before[i+1].jitter {
			t.Fatal("jitter must not be re-drawn")
		}
		prev = r.at
	}
	if after[0].note == nil {
		t.Fatal("moved slot should record the reason")
	}
	var orig time.Time
	_ = e.pool.QueryRow(e.ctx, `SELECT original_slot_at FROM reservations WHERE id = $1`, after[0].id).Scan(&orig)
	if !orig.Equal(before[1].at) {
		t.Fatal("original time must be kept")
	}
}

// 시간 고정: 규칙 밖이면 경고 후 확인해야 하고, 다시 배치해도 고정 예약은 그대로다
func TestPinnedSlotsSurviveRebuild(t *testing.T) {
	e := newEnv(t, kst(3, 2, 9, 0))
	_, _ = e.svc.Ensure(e.ctx)
	ids := e.ready(1)
	at := kst(3, 3, 19, 0) // 허용 구간 밖
	_, err := e.svc.CreatePinned(e.ctx, schedule.PinInput{At: at, ContentID: ids[0]})
	var we *schedule.WarningError
	if !errors.As(err, &we) {
		t.Fatalf("expected warning, got %v", err)
	}
	pinID, err := e.svc.CreatePinned(e.ctx, schedule.PinInput{At: at, ContentID: ids[0], Confirm: true})
	if err != nil {
		t.Fatal(err)
	}
	set, _ := e.svc.Settings(e.ctx)
	set.Rules.BaseIntervalMin = 45
	rep, err := e.svc.SaveRules(e.ctx, schedule.RulesInput{Rules: set.Rules, Windows: set.Windows, Scope: "now", Seed: 11}, false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pinned != 1 {
		t.Fatalf("pinned kept = %d", rep.Pinned)
	}
	var state string
	var slot time.Time
	_ = e.pool.QueryRow(e.ctx, `SELECT state, slot_at FROM reservations WHERE id = $1`, pinID).Scan(&state, &slot)
	if !slot.Equal(at) || state == "canceled" {
		t.Fatalf("pinned changed: %s %s", state, slot)
	}
}

// 설정 변경 미리보기와 적용이 같은 시각을 낸다. 있던 글은 순서대로 새 예약에 다시 들어가고 자리가 모자라면 보관함으로
func TestRulesPreviewMatchesApplyAndKeepsContentOrder(t *testing.T) {
	e := newEnv(t, kst(3, 2, 9, 0))
	_, _ = e.svc.Ensure(e.ctx)
	ids := e.ready(25)
	_, _ = e.svc.AutoAssign(e.ctx)
	set, _ := e.svc.Settings(e.ctx)
	set.Rules.DailyTarget = 5 // 줄인다
	in := schedule.RulesInput{Rules: set.Rules, Windows: set.Windows, Scope: "now", Seed: 5}
	pre, err := e.svc.SaveRules(e.ctx, in, true)
	if err != nil {
		t.Fatal(err)
	}
	app, err := e.svc.SaveRules(e.ctx, in, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(pre.Slots) != len(app.Slots) {
		t.Fatalf("preview %d vs apply %d", len(pre.Slots), len(app.Slots))
	}
	for i := range pre.Slots {
		if !pre.Slots[i].At.Equal(app.Slots[i].At) {
			t.Fatal("preview and apply differ")
		}
	}
	if app.Released == 0 || app.Reassigned+app.Released != 25 {
		t.Fatalf("reassigned %d released %d", app.Reassigned, app.Released)
	}
	all := e.reservations()
	for i := 0; i < app.Reassigned; i++ {
		if all[i].content == nil || *all[i].content != ids[i] {
			t.Fatal("contents should keep their order")
		}
	}
	per := map[string]int{}
	for _, r := range all {
		per[r.date]++
	}
	for d, n := range per {
		if n > 5 {
			t.Fatalf("%s has %d > 5", d, n)
		}
	}
}

// 쉬는 날: 그날 예약을 닫고 글은 다음 빈 예약으로, 쉬는 날을 지우면 그날 예약을 다시 만든다
func TestExceptionDay(t *testing.T) {
	e := newEnv(t, kst(3, 2, 9, 0))
	_, _ = e.svc.Ensure(e.ctx)
	ids := e.ready(1)
	_, _ = e.svc.AutoAssign(e.ctx)
	n, err := e.svc.AddException(e.ctx, "2026-03-02", "오늘만 쉬기")
	if err != nil || n == 0 {
		t.Fatalf("closed %d %v", n, err)
	}
	all := e.reservations()
	if all[0].date != "2026-03-03" || all[0].content == nil || *all[0].content != ids[0] {
		t.Fatal("content should move to the next day's first slot")
	}
	if c, _ := e.svc.Ensure(e.ctx); c != 0 {
		t.Fatal("ensure must not refill the excluded day")
	}
	if err := e.svc.RemoveException(e.ctx, "2026-03-02"); err != nil {
		t.Fatal(err)
	}
	today := 0
	for _, r := range e.reservations() {
		if r.date == "2026-03-02" {
			today++
		}
	}
	if today == 0 {
		t.Fatal("removing the exception should rebuild today's slots")
	}
}

// 순서 바꾸기·다음 빈 시간·내일로
func TestSwapAndMove(t *testing.T) {
	e := newEnv(t, kst(3, 2, 9, 0))
	_, _ = e.svc.Ensure(e.ctx)
	ids := e.ready(2)
	_, _ = e.svc.AutoAssign(e.ctx)
	all := e.reservations()
	if err := e.svc.Swap(e.ctx, all[0].id, all[1].id); err != nil {
		t.Fatal(err)
	}
	all = e.reservations()
	if *all[0].content != ids[1] || *all[1].content != ids[0] {
		t.Fatal("swap failed")
	}
	target, err := e.svc.Move(e.ctx, all[0].id, "tomorrow")
	if err != nil {
		t.Fatal(err)
	}
	var date string
	_ = e.pool.QueryRow(e.ctx, `SELECT to_char(local_date, 'YYYY-MM-DD') FROM reservations WHERE id = $1`, target).Scan(&date)
	if date != "2026-03-03" {
		t.Fatalf("moved to %s", date)
	}
}
