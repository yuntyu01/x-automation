package publish_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yuntyu01/x-automation/internal/integration/xapi/xfake"
	"github.com/yuntyu01/x-automation/internal/jobs"
	"github.com/yuntyu01/x-automation/internal/publish"
)

// 구간 끝 여유는 새 묶음 시작에만 쓴다. 본문이 여유 직전에 올라가면 답글은 같은 구간 안에서 바로 이어 올린다
func TestRepliesNearWindowEndAreNotDeferred(t *testing.T) {
	e := newEnv(t, 5*time.Second)
	e.connect()
	e.automation(true)
	if _, err := e.pool.Exec(e.ctx, `UPDATE schedule_rules SET reply_gap_sec = 60, end_margin_min = 5`); err != nil {
		t.Fatal(err)
	}
	e.clk.Set(kst(27, 16, 53)) // 새 묶음 시작 한도(16:54) 직전
	cid := e.newContent(threeParts)
	e.assignedReservation(cid, e.clk.Now())
	runID, err := e.pub.Dispatch(e.ctx)
	if err != nil || runID == "" {
		t.Fatalf("dispatch = %q %v", runID, err)
	}
	e.drain(10 * time.Minute)
	if v := e.run(runID); v.State != "completed" || len(e.fake.Posts()) != 3 {
		t.Fatalf("state = %s parts = %s note = %v", v.State, e.partStates(v), v.Note)
	}
}

// 보내던 중에 끊긴 실행은 게시가 막혀 있어도 '결과 확인 필요'로 드러난다 (막힌 채 계속 미뤄지지 않는다)
func TestInterruptedSendSurfacesWhileBlocked(t *testing.T) {
	e := newEnv(t, 5*time.Second)
	e.connect()
	runID, err := e.pub.PostNow(e.ctx, publish.PostNowInput{ContentID: e.newContent("본문.\n---\n답글."), Confirm: true})
	if err != nil {
		t.Fatal(err)
	}
	if j, err := e.queue.Claim(e.ctx, []string{publish.JobKind}, 2*time.Minute); err != nil || j == nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(e.ctx, `UPDATE publish_parts SET state = 'sending', intent_at = now() WHERE run_id = $1 AND position = 0`, runID); err != nil {
		t.Fatal(err)
	}
	on := true
	if _, err := e.settings.SetFlags(e.ctx, nil, nil, &on); err != nil { // 게시 일시정지
		t.Fatal(err)
	}
	e.restart()
	e.clk.Advance(3 * time.Minute)
	if _, _, err := e.queue.RecoverExpired(e.ctx, func(string) jobs.RecoverPolicy { return jobs.RecoverRequeue }); err != nil {
		t.Fatal(err)
	}
	e.drain(10 * time.Minute)
	if v := e.run(runID); v.State != "unknown" || len(e.fake.Posts()) != 0 {
		t.Fatalf("state = %s posts = %d note = %v", v.State, len(e.fake.Posts()), v.Note)
	}
}

// 바로 거절돼 아무것도 올라가지 않은 실행은 다음 게시의 간격 계산에 들어가지 않는다
func TestRejectedRunDoesNotDelayNextSlot(t *testing.T) {
	e := newEnv(t, 5*time.Second)
	e.connect()
	e.automation(true)
	e.fake.Configure(func(f *xfake.Server) { f.FailNextPosts = []int{403} })
	runID, err := e.pub.PostNow(e.ctx, publish.PostNowInput{ContentID: e.newContent("거절될 본문."), Confirm: true})
	if err != nil {
		t.Fatal(err)
	}
	e.drain(time.Minute)
	if v := e.run(runID); v.State != "failed" {
		t.Fatalf("rejected run = %s", v.State)
	}
	e.clk.Advance(5 * time.Minute)
	e.assignedReservation(e.newContent("다음 본문."), e.clk.Now())
	if id, err := e.pub.Dispatch(e.ctx); err != nil || id == "" {
		var reason *string
		_ = e.pool.QueryRow(e.ctx, `SELECT block_reason FROM reservations WHERE state = 'assigned'`).Scan(&reason)
		t.Fatalf("dispatch = %q %v block=%v", id, err, deref(reason))
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}

// 게시 직전 검사에 걸린 글은 예약에서 빠지고 알림이 남는다 (대기↔준비 완료를 오가며 예약을 태우지 않는다)
func TestPrePublishValidationFailureReleasesContent(t *testing.T) {
	e := newEnv(t, 5*time.Second)
	e.connect()
	e.automation(true)
	cid := e.newContent("본문.\n---\n답글.")
	rid := e.assignedReservation(cid, e.clk.Now())
	// 승인 뒤 한도가 줄어든 상황: 저장된 부분 글이 지금 한도를 넘는다
	if _, err := e.pool.Exec(e.ctx, `UPDATE thread_parts SET text = repeat('가', 200) WHERE position = 0
		AND revision_id = (SELECT current_revision_id FROM contents WHERE id = $1)`, cid); err != nil {
		t.Fatal(err)
	}
	if id, err := e.pub.Dispatch(e.ctx); err != nil || id != "" {
		t.Fatalf("dispatch = %q %v", id, err)
	}
	var content *string
	var hold bool
	_ = e.pool.QueryRow(e.ctx, `SELECT r.content_id, c.schedule_hold FROM reservations r, contents c WHERE r.id = $1 AND c.id = $2`, rid, cid).Scan(&content, &hold)
	if content != nil || !hold {
		t.Fatalf("reservation content = %v hold = %v", content, hold)
	}
	var n int
	_ = e.pool.QueryRow(e.ctx, `SELECT count(*) FROM notifications WHERE group_key = $1`, "publish:invalid:"+cid).Scan(&n)
	if n != 1 {
		t.Fatalf("notifications = %d", n)
	}
	if err := e.sched.Tick(e.ctx); err != nil {
		t.Fatal(err)
	}
	var placed int
	_ = e.pool.QueryRow(e.ctx, `SELECT count(*) FROM reservations WHERE content_id = $1 AND state IN ('assigned', 'waiting')`, cid).Scan(&placed)
	if placed != 0 {
		t.Fatalf("held content was placed again in %d reservations", placed)
	}
}

// 예약 게시 실행을 사용자가 다시 시도할 때도 워커와 같은 조건으로 미리 막는다 (받아 놓고 계속 미루지 않는다)
func TestRetryOfScheduledRunUsesWorkerGates(t *testing.T) {
	e := newEnv(t, 5*time.Second)
	e.connect()
	e.automation(true)
	e.fake.Configure(func(f *xfake.Server) { f.RejectReplies = 403 })
	e.assignedReservation(e.newContent(threeParts), e.clk.Now())
	runID, err := e.pub.Dispatch(e.ctx)
	if err != nil || runID == "" {
		t.Fatalf("dispatch = %q %v", runID, err)
	}
	e.drain(10 * time.Minute)
	if v := e.run(runID); v.State != "partial" {
		t.Fatalf("setup: run = %s %s", v.State, e.partStates(v))
	}
	e.fake.Configure(func(f *xfake.Server) { f.RejectReplies = 0 })
	e.automation(false)
	var ve *publish.ValidationError
	if err := e.pub.Retry(e.ctx, runID, false); !errors.As(err, &ve) || !strings.Contains(ve.Fields["run"], "자동 운영") {
		t.Fatalf("retry with automation off = %v", err)
	}
	e.automation(true)
	if err := e.pub.Retry(e.ctx, runID, false); err != nil {
		t.Fatal(err)
	}
	e.drain(10 * time.Minute)
	if v := e.run(runID); v.State != "completed" || len(e.fake.Posts()) != 3 {
		t.Fatalf("after retry = %s posts %d", v.State, len(e.fake.Posts()))
	}
}
