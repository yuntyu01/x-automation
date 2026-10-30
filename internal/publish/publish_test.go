package publish_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yuntyu01/x-automation/internal/budget"
	"github.com/yuntyu01/x-automation/internal/clock"
	"github.com/yuntyu01/x-automation/internal/connection"
	"github.com/yuntyu01/x-automation/internal/content"
	"github.com/yuntyu01/x-automation/internal/integration/storage"
	"github.com/yuntyu01/x-automation/internal/integration/xapi"
	"github.com/yuntyu01/x-automation/internal/integration/xapi/xfake"
	"github.com/yuntyu01/x-automation/internal/jobs"
	"github.com/yuntyu01/x-automation/internal/media"
	"github.com/yuntyu01/x-automation/internal/notification"
	"github.com/yuntyu01/x-automation/internal/publish"
	"github.com/yuntyu01/x-automation/internal/randx"
	"github.com/yuntyu01/x-automation/internal/schedule"
	"github.com/yuntyu01/x-automation/internal/secretbox"
	"github.com/yuntyu01/x-automation/internal/settings"
	"github.com/yuntyu01/x-automation/internal/testutil/pgtest"
	"github.com/yuntyu01/x-automation/internal/thread"
)

var seoul, _ = time.LoadLocation("Asia/Seoul")

func kst(d, h, m int) time.Time { return time.Date(2026, 10, d, h, m, 0, 0, seoul) }

var session = []byte("session-hash-a")

type env struct {
	t        *testing.T
	ctx      context.Context
	pool     *pgxpool.Pool
	clk      *clock.Fake
	fake     *xfake.Server
	client   *xapi.HTTP
	box      *secretbox.Box
	settings *settings.Repo
	notify   *notification.Service
	queue    *jobs.Queue
	sched    *schedule.Service
	contents *content.Service
	conn     *connection.Service
	pub      *publish.Service
	gates    publish.Gates
}

func newEnv(t *testing.T, timeout time.Duration) *env {
	t.Helper()
	pool := pgtest.New(t)
	clk := clock.NewFake(kst(27, 12, 5)) // 화요일, 기본 허용 구간(12:00~17:00) 안
	fake := xfake.New()
	t.Cleanup(fake.Close)
	client := xapi.NewHTTP(xapi.HTTPConfig{ClientID: "cid", ClientSecret: "secret", RedirectURI: "http://app.test/api/x/oauth/callback",
		APIBase: fake.URL, AuthorizeURL: fake.URL + "/i/oauth2/authorize", TokenURL: fake.URL + "/2/oauth2/token",
		HTTP: &http.Client{Timeout: timeout}})
	box, err := secretbox.New([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	sr := settings.NewRepo(pool)
	n := notification.NewService(pool)
	e := &env{t: t, ctx: context.Background(), pool: pool, clk: clk, fake: fake, client: client, box: box, settings: sr, notify: n,
		queue: jobs.NewQueue(pool, clk), gates: publish.Gates{Live: true, PublishingEnabled: true}}
	e.sched = schedule.NewService(pool, clk, randx.NewSeeded(1), sr, n)
	e.contents = content.NewService(pool, clk, nil)
	e.conn = connection.NewService(pool, client, box, clk, n)
	e.restart()
	st, _ := sr.Get(e.ctx)
	if _, err := sr.Update(e.ctx, st.Version, settings.ModeFullAuto, st.Timezone, st.Policy); err != nil {
		t.Fatal(err)
	}
	return e
}

// restart는 서버 재시작처럼 서비스를 새로 만든다 (메모리 상태 없음, DB만 이어 받음).
func (e *env) restart() {
	local, err := storage.NewLocal(e.t.TempDir(), "http://app.test/media")
	if err != nil {
		e.t.Fatal(err)
	}
	m := media.NewService(e.pool, local, e.clk, func(context.Context) int { return 14 })
	e.queue = jobs.NewQueue(e.pool, e.clk)
	e.pub = publish.NewService(publish.Deps{Pool: e.pool, Clock: e.clk, Queue: e.queue, Conn: e.conn, Settings: e.settings,
		Schedule: e.sched, Media: m, Budget: budget.NewService(e.pool, e.clk, e.settings), Notify: e.notify, Gates: e.gates})
}

func (e *env) connect() {
	e.t.Helper()
	u, err := e.conn.Begin(e.ctx, session)
	if err != nil {
		e.t.Fatal(err)
	}
	pu, _ := url.Parse(u)
	if pu.Query().Get("code_challenge_method") != "S256" || pu.Query().Get("code_challenge") == "" {
		e.t.Fatalf("authorize url lacks PKCE: %s", u)
	}
	v, err := e.conn.Complete(e.ctx, session, pu.Query().Get("state"), "good-code", "")
	if err != nil {
		e.t.Fatal(err)
	}
	if v.Status != connection.Connected || v.Username != "tester" {
		e.t.Fatalf("connection = %+v", v)
	}
}

func (e *env) newContent(raw string) string {
	e.t.Helper()
	d, err := e.contents.Create(e.ctx, content.NewContent{RawText: raw, SplitMode: thread.ModeManual})
	if err != nil {
		e.t.Fatal(err)
	}
	return d.ID
}

const threeParts = "본문입니다. 첫 줄.\n---\n첫 번째 답글입니다.\n---\n두 번째 답글입니다."

// drain은 워커처럼 작업을 꺼내 실행한다. 미뤄진 작업은 시계를 그 시각으로 옮겨 이어 간다 (최대 maxWait).
func (e *env) drain(maxWait time.Duration) []jobs.Result {
	e.t.Helper()
	var out []jobs.Result
	limit := e.clk.Now().Add(maxWait)
	for i := 0; i < 50; i++ {
		j, err := e.queue.Claim(e.ctx, []string{publish.JobKind}, 2*time.Minute)
		if err != nil {
			e.t.Fatal(err)
		}
		if j == nil {
			var next *time.Time
			if err := e.pool.QueryRow(e.ctx, `SELECT min(run_at) FROM jobs WHERE kind = $1 AND state = 'queued'`, publish.JobKind).Scan(&next); err != nil {
				e.t.Fatal(err)
			}
			if next == nil || next.After(limit) {
				return out
			}
			if next.After(e.clk.Now()) {
				e.clk.Set(next.Add(time.Second))
			}
			continue
		}
		res := e.pub.HandleRun(e.ctx, j)
		if err := e.queue.Finish(e.ctx, j, res); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, res)
	}
	e.t.Fatal("drain did not settle")
	return out
}

func (e *env) run(id string) publish.RunView {
	e.t.Helper()
	v, err := e.pub.Run(e.ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return v
}

func (e *env) partStates(v publish.RunView) string {
	var s []string
	for _, p := range v.Parts {
		s = append(s, p.State)
	}
	return strings.Join(s, ",")
}

func (e *env) reservationState(id string) string {
	var st string
	if err := e.pool.QueryRow(e.ctx, `SELECT state FROM reservations WHERE id = $1`, id).Scan(&st); err != nil {
		e.t.Fatal(err)
	}
	return st
}

func TestBodySucceedsReplyFailsRestartResumesOnlyReply(t *testing.T) {
	e := newEnv(t, 5*time.Second)
	e.connect()
	cid := e.newContent(threeParts)
	e.fake.Configure(func(f *xfake.Server) { f.RejectReplies = 403 }) // 답글 거부 (대화 제한 등)

	runID, err := e.pub.PostNow(e.ctx, publish.PostNowInput{ContentID: cid})
	if err != nil {
		t.Fatal(err)
	}
	e.drain(time.Hour)
	v := e.run(runID)
	if v.State != "partial" || e.partStates(v) != "succeeded,failed,pending" {
		t.Fatalf("after reply failure: %s %s", v.State, e.partStates(v))
	}
	if len(e.fake.Posts()) != 1 {
		t.Fatalf("posts = %d", len(e.fake.Posts()))
	}
	body := e.fake.Posts()[0]
	if *v.Parts[0].PostID != body.ID || !strings.HasSuffix(*v.Parts[0].URL, "/tester/status/"+body.ID) {
		t.Fatalf("stored body id/url = %v %v", *v.Parts[0].PostID, *v.Parts[0].URL)
	}
	if e.reservationState(v.ReservationID) != "partial" {
		t.Fatalf("reservation = %s", e.reservationState(v.ReservationID))
	}

	// 서버 재시작 → 답글 거부가 풀림 → 실패한 답글부터 다시
	e.restart()
	e.fake.Configure(func(f *xfake.Server) { f.RejectReplies = 0 })
	if err := e.pub.Retry(e.ctx, runID, false); err != nil {
		t.Fatal(err)
	}
	e.drain(time.Hour)
	v = e.run(runID)
	if v.State != "completed" || e.partStates(v) != "succeeded,succeeded,succeeded" {
		t.Fatalf("after resume: %s %s", v.State, e.partStates(v))
	}
	posts := e.fake.Posts()
	if len(posts) != 3 {
		t.Fatalf("posts = %d (본문이 다시 올라가면 안 됨)", len(posts))
	}
	if posts[1].ReplyTo != body.ID || posts[2].ReplyTo != posts[1].ID || posts[0].ID != body.ID {
		t.Fatalf("thread chain broken: %+v", posts)
	}
	if e.reservationState(v.ReservationID) != "published" {
		t.Fatalf("reservation = %s", e.reservationState(v.ReservationID))
	}
}

func TestTimeoutAfterSendIsUnknownAndNeverResent(t *testing.T) {
	e := newEnv(t, 300*time.Millisecond)
	e.connect()
	cid := e.newContent("본문만 있는 글입니다.\n---\n답글 하나.")
	e.fake.Configure(func(f *xfake.Server) { f.HangAfterPost = time.Second }) // X는 게시했지만 응답이 늦어 클라이언트가 시간 초과

	runID, err := e.pub.PostNow(e.ctx, publish.PostNowInput{ContentID: cid})
	if err != nil {
		t.Fatal(err)
	}
	res := e.drain(time.Hour)
	if len(res) != 1 || res[0].Outcome != jobs.Unknown {
		t.Fatalf("results = %+v", res)
	}
	time.Sleep(800 * time.Millisecond) // 가짜 서버 응답 대기 정리
	e.fake.Configure(func(f *xfake.Server) { f.HangAfterPost = 0 })
	v := e.run(runID)
	if v.State != "unknown" || e.partStates(v) != "unknown,pending" {
		t.Fatalf("state = %s %s", v.State, e.partStates(v))
	}
	if n := len(e.fake.Posts()); n != 1 {
		t.Fatalf("posts = %d", n)
	}
	// 자동으로 다시 보내지 않는다: 재시작·디스패치·작업 정리 뒤에도 그대로
	e.restart()
	e.clk.Advance(time.Hour)
	if _, _, err := e.queue.RecoverExpired(e.ctx, func(string) jobs.RecoverPolicy { return jobs.RecoverRequeue }); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pub.Reconcile(e.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pub.Dispatch(e.ctx); err != nil {
		t.Fatal(err)
	}
	e.drain(time.Hour)
	if n := len(e.fake.Posts()); n != 1 {
		t.Fatalf("resent automatically: posts = %d", n)
	}
	// 알림: 결과 확인 필요 (중대)
	list, _ := e.notify.List(e.ctx, notification.ListOptions{})
	found := false
	for _, n := range list {
		if n.Severity == notification.Critical && strings.Contains(n.Title, "확인") {
			found = true
		}
	}
	if !found {
		t.Fatalf("critical notification missing: %+v", list)
	}
	// 확인 없이 재시도하면 중복 위험 경고
	var we *publish.WarningError
	if err := e.pub.Retry(e.ctx, runID, false); !errors.As(err, &we) {
		t.Fatalf("retry without ack = %v", err)
	}
	// X에서 확인한 주소로 정리 → 남은 답글만 이어서
	body := e.fake.Posts()[0]
	if err := e.pub.Resolve(e.ctx, runID, publish.ResolveInput{Position: 0, Posted: true, PostRef: "https://x.com/tester/status/" + body.ID + "?s=20"}); err != nil {
		t.Fatal(err)
	}
	e.drain(time.Hour)
	v = e.run(runID)
	posts := e.fake.Posts()
	if v.State != "completed" || len(posts) != 2 || posts[1].ReplyTo != body.ID {
		t.Fatalf("after resolve: %s posts=%+v", v.State, posts)
	}
}

func TestCrashAfterIntentIsUnknown(t *testing.T) {
	e := newEnv(t, 5*time.Second)
	e.connect()
	cid := e.newContent("본문.\n---\n답글.")
	runID, err := e.pub.PostNow(e.ctx, publish.PostNowInput{ContentID: cid})
	if err != nil {
		t.Fatal(err)
	}
	// 워커가 전송 의도를 남긴 직후 죽은 상황
	j, err := e.queue.Claim(e.ctx, []string{publish.JobKind}, 2*time.Minute)
	if err != nil || j == nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(e.ctx, `UPDATE publish_parts SET state = 'sending', intent_at = now() WHERE run_id = $1 AND position = 0`, runID); err != nil {
		t.Fatal(err)
	}
	e.restart()
	e.clk.Advance(3 * time.Minute)
	if req, _, err := e.queue.RecoverExpired(e.ctx, func(string) jobs.RecoverPolicy { return jobs.RecoverRequeue }); err != nil || req != 1 {
		t.Fatalf("recover = %d %v", req, err)
	}
	e.drain(time.Hour)
	v := e.run(runID)
	if v.State != "unknown" || len(e.fake.Posts()) != 0 {
		t.Fatalf("state = %s posts = %d", v.State, len(e.fake.Posts()))
	}
	// 확인 결과 안 올라갔음 → 그 부분부터 보낸다
	if err := e.pub.Resolve(e.ctx, runID, publish.ResolveInput{Position: 0, Posted: false}); err != nil {
		t.Fatal(err)
	}
	e.drain(time.Hour)
	if v := e.run(runID); v.State != "completed" || len(e.fake.Posts()) != 2 {
		t.Fatalf("after resolve not-posted: %s %d", v.State, len(e.fake.Posts()))
	}
}

// assignedReservation은 시각이 된 준비 완료 예약을 만든다.
func (e *env) assignedReservation(cid string, at time.Time) string {
	e.t.Helper()
	var id string
	err := e.pool.QueryRow(e.ctx, `INSERT INTO reservations (slot_at, original_slot_at, local_date, content_id, revision_id, state)
		SELECT $1, $1, $2::date, c.id, c.current_revision_id, 'assigned' FROM contents c WHERE c.id = $3 RETURNING id`,
		at, at.In(seoul).Format("2006-01-02"), cid).Scan(&id)
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) automation(on bool) {
	if _, err := e.settings.SetFlags(e.ctx, &on, nil, nil); err != nil {
		e.t.Fatal(err)
	}
}

func TestWorkersAndDispatchRacesStartOneRun(t *testing.T) {
	e := newEnv(t, 5*time.Second)
	e.connect()
	e.automation(true)
	if _, err := e.pool.Exec(e.ctx, `UPDATE schedule_rules SET base_interval_min = 1`); err != nil {
		t.Fatal(err)
	}
	a := e.newContent("A 본문.\n---\nA 답글.")
	b := e.newContent("B 본문.\n---\nB 답글.")
	ra := e.assignedReservation(a, e.clk.Now().Add(-time.Minute))
	rb := e.assignedReservation(b, e.clk.Now().Add(-30*time.Second))

	// 디스패치 여러 개가 동시에 돌아도 활성 실행은 하나
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = e.pub.Dispatch(e.ctx)
		}()
	}
	wg.Wait()
	var active int
	_ = e.pool.QueryRow(e.ctx, `SELECT count(*) FROM publish_runs WHERE state IN ('pending', 'running')`).Scan(&active)
	if active != 1 {
		t.Fatalf("active runs = %d", active)
	}
	// 같은 글을 "지금 게시"해도 이미 게시 중이면 거부
	if _, err := e.pub.PostNow(e.ctx, publish.PostNowInput{ContentID: a, Confirm: true}); err == nil {
		t.Fatal("post-now of a publishing content should fail")
	}
	// 워커 두 개가 같은 큐를 동시에 처리
	var mu sync.Mutex
	worker := func() {
		defer wg.Done()
		q := jobs.NewQueue(e.pool, e.clk)
		for i := 0; i < 10; i++ {
			j, err := q.Claim(e.ctx, []string{publish.JobKind}, 2*time.Minute)
			if err != nil || j == nil {
				return
			}
			res := e.pub.HandleRun(e.ctx, j)
			mu.Lock()
			_ = q.Finish(e.ctx, j, res)
			mu.Unlock()
		}
	}
	wg.Add(2)
	go worker()
	go worker()
	wg.Wait()
	e.drain(time.Hour)
	// 첫 묶음이 끝나야 다음 묶음 (기본 간격 1분 뒤)
	e.clk.Advance(2 * time.Minute)
	if _, err := e.pub.Dispatch(e.ctx); err != nil {
		t.Fatal(err)
	}
	e.drain(time.Hour)
	posts := e.fake.Posts()
	bodies := map[string]int{}
	for _, p := range posts {
		if p.ReplyTo == "" {
			bodies[p.Text]++
		}
	}
	if len(posts) != 4 || bodies["A 본문."] != 1 || bodies["B 본문."] != 1 {
		t.Fatalf("posts = %+v", posts)
	}
	if e.reservationState(ra) != "published" || e.reservationState(rb) != "published" {
		t.Fatalf("ra = %s rb = %s", e.reservationState(ra), e.reservationState(rb))
	}
}

func TestSkipRacesDispatch(t *testing.T) {
	for i := 0; i < 5; i++ {
		e := newEnv(t, 5*time.Second)
		e.connect()
		e.automation(true)
		cid := e.newContent("경쟁 본문.\n---\n답글.")
		rid := e.assignedReservation(cid, e.clk.Now().Add(-time.Minute))
		var wg sync.WaitGroup
		var skipErr error
		var runID string
		wg.Add(2)
		go func() { defer wg.Done(); skipErr = e.sched.Skip(e.ctx, rid) }()
		go func() { defer wg.Done(); runID, _ = e.pub.Dispatch(e.ctx) }()
		wg.Wait()
		st := e.reservationState(rid)
		switch {
		case runID != "" && skipErr == nil:
			t.Fatalf("both skip and dispatch won (state %s)", st)
		case runID == "" && skipErr != nil:
			t.Fatalf("neither won: %v", skipErr)
		case runID != "" && st != "publishing":
			t.Fatalf("dispatch won but state = %s", st)
		case skipErr == nil && st != "canceled":
			t.Fatalf("skip won but state = %s", st)
		}
	}
}

func TestGatesBlockPublishing(t *testing.T) {
	e := newEnv(t, 5*time.Second)
	cid := e.newContent("막힌 본문.\n---\n답글.")
	var ve *publish.ValidationError
	// 연결 전
	if _, err := e.pub.PostNow(e.ctx, publish.PostNowInput{ContentID: cid}); !errors.As(err, &ve) || !strings.Contains(ve.Fields["content_id"], "연결") {
		t.Fatalf("not connected = %v", err)
	}
	e.connect()
	// 서버 실게시 스위치 꺼짐
	e.gates.PublishingEnabled = false
	e.restart()
	if _, err := e.pub.PostNow(e.ctx, publish.PostNowInput{ContentID: cid}); !errors.As(err, &ve) || !strings.Contains(ve.Fields["content_id"], "실제 게시가 꺼져") {
		t.Fatalf("publishing disabled = %v", err)
	}
	e.automation(true)
	rid := e.assignedReservation(cid, e.clk.Now().Add(-time.Minute))
	if id, err := e.pub.Dispatch(e.ctx); err != nil || id != "" {
		t.Fatalf("dispatch with gate off = %q %v", id, err)
	}
	var reason *string
	_ = e.pool.QueryRow(e.ctx, `SELECT block_reason FROM reservations WHERE id = $1`, rid).Scan(&reason)
	if reason == nil || !strings.Contains(*reason, "실제 게시가 꺼져") {
		t.Fatalf("block reason = %v", reason)
	}
	// 자동 운영 꺼짐: 예약 게시는 시작하지 않는다
	e.gates.PublishingEnabled = true
	e.restart()
	e.automation(false)
	if id, _ := e.pub.Dispatch(e.ctx); id != "" {
		t.Fatal("dispatch with automation off")
	}
	// 실행을 만든 뒤 일시정지 → 아무것도 보내지 않고 취소, 예약은 다시 준비 완료로
	e.automation(true)
	runID, err := e.pub.Dispatch(e.ctx)
	if err != nil || runID == "" {
		t.Fatalf("dispatch = %q %v", runID, err)
	}
	on := true
	if _, err := e.settings.SetFlags(e.ctx, nil, nil, &on); err != nil {
		t.Fatal(err)
	}
	e.drain(time.Minute)
	if v := e.run(runID); v.State != "canceled" || len(e.fake.Posts()) != 0 {
		t.Fatalf("paused run = %s posts %d", v.State, len(e.fake.Posts()))
	}
	if st := e.reservationState(rid); st != "assigned" {
		t.Fatalf("reservation after pause = %s", st)
	}
	// 목업 모드(Live=false)는 PUBLISHING_ENABLED와 무관하게 대역에만 게시한다
	e.gates = publish.Gates{Live: false, PublishingEnabled: false}
	e.restart()
	off := false
	_, _ = e.settings.SetFlags(e.ctx, nil, nil, &off)
	if _, err := e.pub.PostNow(e.ctx, publish.PostNowInput{ContentID: cid, Confirm: true}); err != nil {
		t.Fatalf("mock gate = %v", err)
	}
}

func TestReplyOutsideWindowWaitsForNextWindow(t *testing.T) {
	e := newEnv(t, 5*time.Second)
	e.connect()
	e.automation(true)
	// 구간 끝 여유(끝 여유 + 답글 간격) 안에서는 새 본문을 시작하지 않는다. 그래서 본문은 16:30에 올리고,
	// 답글 차례(16:32)에 서버가 내려가 있다가 17:05에 돌아온 상황을 만든다 → 답글은 구간 밖
	if _, err := e.pool.Exec(e.ctx, `UPDATE schedule_rules SET reply_gap_sec = 120`); err != nil {
		t.Fatal(err)
	}
	e.clk.Set(kst(27, 16, 30))
	cid := e.newContent(threeParts)
	e.assignedReservation(cid, e.clk.Now())
	runID, err := e.pub.Dispatch(e.ctx)
	if err != nil || runID == "" {
		var reason *string
		_ = e.pool.QueryRow(e.ctx, `SELECT block_reason FROM reservations WHERE content_id = $1`, cid).Scan(&reason)
		t.Fatalf("dispatch = %q %v block=%v", runID, err, *reason)
	}
	e.drain(time.Second) // 본문만
	if v := e.run(runID); e.partStates(v) != "succeeded,pending,pending" {
		t.Fatalf("after body: %s", e.partStates(v))
	}
	e.restart()
	e.clk.Set(kst(27, 17, 5))
	e.drain(time.Minute)
	v := e.run(runID)
	if e.partStates(v) != "succeeded,pending,pending" || v.Note == nil || !strings.Contains(*v.Note, "남은 답글") {
		t.Fatalf("after window end: %s note=%v", e.partStates(v), v.Note)
	}
	var runAt time.Time
	_ = e.pool.QueryRow(e.ctx, `SELECT run_at FROM jobs WHERE kind = $1 AND state = 'queued'`, publish.JobKind).Scan(&runAt)
	if !runAt.Equal(kst(28, 0, 0)) {
		t.Fatalf("next try at %v, want next window start", runAt.In(seoul))
	}
	e.drain(12 * time.Hour)
	v = e.run(runID)
	if v.State != "completed" || len(e.fake.Posts()) != 3 {
		t.Fatalf("final = %s posts %d", v.State, len(e.fake.Posts()))
	}
}

func TestTokenRefreshAndExpiry(t *testing.T) {
	e := newEnv(t, 5*time.Second)
	e.connect()
	cid := e.newContent("토큰 본문.\n---\n답글.")
	e.fake.Expire() // 접근 토큰 만료 → 401 → 갱신 후 한 번 다시
	runID, err := e.pub.PostNow(e.ctx, publish.PostNowInput{ContentID: cid})
	if err != nil {
		t.Fatal(err)
	}
	e.drain(time.Hour)
	if v := e.run(runID); v.State != "completed" || e.fake.RefreshCount() != 1 || len(e.fake.Posts()) != 2 {
		t.Fatalf("refresh path: %s refreshes=%d posts=%d", v.State, e.fake.RefreshCount(), len(e.fake.Posts()))
	}
	// 갱신 토큰도 무효 → 연결 만료 + 알림, 게시는 막힘
	bad, _ := e.box.Seal([]byte("revoked-token"), "x:refresh")
	if _, err := e.pool.Exec(e.ctx, `UPDATE connections SET refresh_token_enc = $1, token_expires_at = $2`, bad, e.clk.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.conn.Token(e.ctx, false); err == nil {
		t.Fatal("expected refresh failure")
	}
	v, _ := connection.NewRepo(e.pool).View(e.ctx)
	if v.Status != connection.Expired {
		t.Fatalf("status = %s", v.Status)
	}
	var ve *publish.ValidationError
	cid2 := e.newContent("다음 글.\n---\n답글.")
	if _, err := e.pub.PostNow(e.ctx, publish.PostNowInput{ContentID: cid2}); !errors.As(err, &ve) || !strings.Contains(ve.Fields["content_id"], "만료") {
		t.Fatalf("expired connection = %v", err)
	}
}

func TestOAuthStateBoundToSessionAndSingleUse(t *testing.T) {
	e := newEnv(t, 5*time.Second)
	u, err := e.conn.Begin(e.ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	pu, _ := url.Parse(u)
	state := pu.Query().Get("state")
	var fe *connection.FlowError
	if _, err := e.conn.Complete(e.ctx, []byte("other-session"), state, "good-code", ""); !errors.As(err, &fe) {
		t.Fatalf("other session = %v", err)
	}
	// 한 번 쓴 state는 다시 못 쓴다
	if _, err := e.conn.Complete(e.ctx, session, state, "good-code", ""); !errors.As(err, &fe) {
		t.Fatalf("reused state = %v", err)
	}
	u2, _ := e.conn.Begin(e.ctx, session)
	pu2, _ := url.Parse(u2)
	e.clk.Advance(11 * time.Minute) // 10분 만료
	if _, err := e.conn.Complete(e.ctx, session, pu2.Query().Get("state"), "good-code", ""); !errors.As(err, &fe) {
		t.Fatalf("expired state = %v", err)
	}
	if _, err := e.conn.Complete(e.ctx, session, "", "", "access_denied"); !errors.As(err, &fe) {
		t.Fatalf("denied = %v", err)
	}
	// 토큰은 암호화되어 저장된다
	e.connect()
	var acc []byte
	_ = e.pool.QueryRow(e.ctx, `SELECT access_token_enc FROM connections`).Scan(&acc)
	if strings.Contains(string(acc), "acc-") {
		t.Fatal("access token stored in plain text")
	}
}

func TestAbandonHoldsContent(t *testing.T) {
	e := newEnv(t, 5*time.Second)
	e.connect()
	cid := e.newContent(threeParts)
	e.fake.Configure(func(f *xfake.Server) { f.RejectReplies = 403 })
	runID, _ := e.pub.PostNow(e.ctx, publish.PostNowInput{ContentID: cid})
	e.drain(time.Hour)
	if err := e.pub.Abandon(e.ctx, runID); err != nil {
		t.Fatal(err)
	}
	var hold bool
	var stage string
	_ = e.pool.QueryRow(e.ctx, `SELECT c.schedule_hold, o.stage FROM contents c JOIN content_overview o ON o.id = c.id WHERE c.id = $1`, cid).Scan(&hold, &stage)
	if !hold || stage != "published" {
		t.Fatalf("hold=%v stage=%s", hold, stage)
	}
	list, _ := e.pub.ListRuns(e.ctx)
	if len(list.Attention) != 0 || len(list.Recent) != 1 {
		t.Fatalf("lists = %d attention, %d recent", len(list.Attention), len(list.Recent))
	}
}

func TestParsePostRef(t *testing.T) {
	cases := map[string]string{
		"https://x.com/tester/status/1849000000000000001":           "1849000000000000001",
		"https://twitter.com/a/status/1849000000000000002?s=20&t=x": "1849000000000000002",
		"1849000000000000003": "1849000000000000003",
		"https://x.com/i/web/status/1849000000000000004/photo/1": "1849000000000000004",
	}
	for in, want := range cases {
		if got, ok := publish.ParsePostRef(in); !ok || got != want {
			t.Errorf("%s → %q %v", in, got, ok)
		}
	}
	for _, bad := range []string{"", "hello", "https://x.com/tester", "12"} {
		if _, ok := publish.ParsePostRef(bad); ok {
			t.Errorf("%q should not parse", bad)
		}
	}
}

func (e *env) budgetPolicy(limit int64, onLimit string) {
	e.t.Helper()
	st, _ := e.settings.Get(e.ctx)
	p := st.Policy
	p.Budget.MonthlyLimitMicros, p.Budget.OnLimit = limit, onLimit
	p.Pricing.XPostMicros, p.Pricing.XPostWithURLMicros, p.Pricing.VerifiedAt, p.Pricing.Source = 15000, 200000, "2026-10-27", "test"
	if _, err := e.settings.Update(e.ctx, st.Version, st.OperationMode, st.Timezone, p); err != nil {
		e.t.Fatal(err)
	}
}

func TestBudgetPolicyStopAllBlocksNewPosts(t *testing.T) {
	e := newEnv(t, 5*time.Second)
	e.connect()
	e.automation(true)
	e.budgetPolicy(100000, "stop_all") // 0.1 USD
	b := budget.NewService(e.pool, e.clk, e.settings)
	if err := b.Record(e.ctx, budget.Entry{Category: budget.AIText, RefKey: "spent", Micros: 90000}, ""); err != nil {
		t.Fatal(err)
	}
	cid := e.newContent("예산 본문.\n---\n답글.") // 2개 × 0.015 = 0.03 → 한도 초과
	if _, err := e.pub.PostNow(e.ctx, publish.PostNowInput{ContentID: cid, Confirm: true}); !budget.IsExceeded(err) {
		t.Fatalf("post-now over budget = %v", err)
	}
	rid := e.assignedReservation(cid, e.clk.Now().Add(-time.Minute))
	if id, err := e.pub.Dispatch(e.ctx); err != nil || id != "" {
		t.Fatalf("dispatch over budget = %q %v", id, err)
	}
	var reason *string
	_ = e.pool.QueryRow(e.ctx, `SELECT block_reason FROM reservations WHERE id = $1`, rid).Scan(&reason)
	if reason == nil || !strings.Contains(*reason, "예산") {
		t.Fatalf("block reason = %v", reason)
	}
	list, _ := e.notify.List(e.ctx, notification.ListOptions{})
	// 남은 예산(0.01)이 이번 게시(0.03)에 모자란 경우: 한도 도달과 구분해 따로 알린다
	if len(list) == 0 || list[0].GroupKey != "budget:insufficient" {
		t.Fatalf("budget notification = %+v", list)
	}
	// "생성만 중지" 정책이면 게시는 계속된다 (한도를 넘어도 게시 비용은 기록)
	e.budgetPolicy(100000, "stop_generation")
	runID, err := e.pub.Dispatch(e.ctx)
	if err != nil || runID == "" {
		t.Fatalf("dispatch with stop_generation = %q %v", runID, err)
	}
	e.drain(time.Hour)
	if v := e.run(runID); v.State != "completed" {
		t.Fatalf("run = %s", v.State)
	}
	sum, _ := b.Summary(e.ctx)
	if sum.Counts["x_post"] != 2 || sum.ByCategory["x_post"] != 30000 {
		t.Fatalf("x_post usage = %v %v", sum.Counts, sum.ByCategory)
	}
}
