package retention_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yuntyu01/x-automation/internal/budget"
	"github.com/yuntyu01/x-automation/internal/clock"
	"github.com/yuntyu01/x-automation/internal/connection"
	"github.com/yuntyu01/x-automation/internal/content"
	"github.com/yuntyu01/x-automation/internal/integration"
	"github.com/yuntyu01/x-automation/internal/integration/storage"
	"github.com/yuntyu01/x-automation/internal/integration/xapi"
	"github.com/yuntyu01/x-automation/internal/jobs"
	"github.com/yuntyu01/x-automation/internal/media"
	"github.com/yuntyu01/x-automation/internal/notification"
	"github.com/yuntyu01/x-automation/internal/publish"
	"github.com/yuntyu01/x-automation/internal/randx"
	"github.com/yuntyu01/x-automation/internal/retention"
	"github.com/yuntyu01/x-automation/internal/schedule"
	"github.com/yuntyu01/x-automation/internal/secretbox"
	"github.com/yuntyu01/x-automation/internal/settings"
	"github.com/yuntyu01/x-automation/internal/testutil/pgtest"
	"github.com/yuntyu01/x-automation/internal/thread"
)

var seoul, _ = time.LoadLocation("Asia/Seoul")

// flakyStore는 삭제 실패를 흉내 낸다.
type flakyStore struct {
	storage.Store
	failDelete atomic.Bool
}

func (f *flakyStore) Delete(ctx context.Context, key string) error {
	if f.failDelete.Load() {
		return errors.New("r2 unavailable")
	}
	return f.Store.Delete(ctx, key)
}

type env struct {
	t        *testing.T
	ctx      context.Context
	pool     *pgxpool.Pool
	clk      *clock.Fake
	store    *flakyStore
	media    *media.Service
	contents *content.Service
	notify   *notification.Service
	queue    *jobs.Queue
	pub      *publish.Service
	x        *xapi.Memory
	ret      *retention.Service
	seq      int
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool := pgtest.New(t)
	clk := clock.NewFake(time.Date(2026, 10, 27, 12, 5, 0, 0, seoul))
	local, err := storage.NewLocal(t.TempDir(), "http://app.test/media")
	if err != nil {
		t.Fatal(err)
	}
	fs := &flakyStore{Store: local}
	sr := settings.NewRepo(pool)
	n := notification.NewService(pool)
	m := media.NewService(pool, fs, clk, func(context.Context) int { return 14 })
	cs := content.NewService(pool, clk, nil)
	cs.SetMediaLoader(m.ContentLoader())
	box, _ := secretbox.New(bytes.Repeat([]byte{7}, 32))
	x := &xapi.Memory{RedirectURI: "http://app.test/api/x/oauth/callback"}
	conn := connection.NewService(pool, x, box, clk, n)
	q := jobs.NewQueue(pool, clk)
	e := &env{t: t, ctx: context.Background(), pool: pool, clk: clk, store: fs, media: m, contents: cs, notify: n, queue: q, x: x,
		ret: retention.NewService(pool, m, sr, clk, n, slog.New(slog.NewTextHandler(io.Discard, nil)))}
	e.pub = publish.NewService(publish.Deps{Pool: pool, Clock: clk, Queue: q, Conn: conn, Settings: sr,
		Schedule: schedule.NewService(pool, clk, randx.NewSeeded(1), sr, n), Media: m, Budget: budget.NewService(pool, clk, sr), Notify: n,
		Gates: publish.Gates{Live: false}})
	st, _ := sr.Get(e.ctx)
	if _, err := sr.Update(e.ctx, st.Version, settings.ModeFullAuto, st.Timezone, st.Policy); err != nil {
		t.Fatal(err)
	}
	u, err := conn.Begin(e.ctx, []byte("s"))
	if err != nil {
		t.Fatal(err)
	}
	pu, _ := url.Parse(u)
	if _, err := conn.Complete(e.ctx, []byte("s"), pu.Query().Get("state"), "mock-code", ""); err != nil {
		t.Fatal(err)
	}
	return e
}

// image는 매번 다른 내용의 이미지를 올린다 (같은 내용은 재사용되므로).
func (e *env) image() string {
	e.t.Helper()
	e.seq++
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for i := range img.Pix {
		img.Pix[i] = byte((i * e.seq) % 251)
	}
	img.Set(e.seq%32, 0, color.RGBA{R: byte(e.seq), A: 255})
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	a, err := e.media.Save(e.ctx, media.SaveInput{Data: b.Bytes(), Source: "upload", AltText: "보관 확인용"})
	if err != nil {
		e.t.Fatal(err)
	}
	return a.ID
}

func (e *env) contentWith(mediaIDs ...string) string {
	e.t.Helper()
	d, err := e.contents.Create(e.ctx, content.NewContent{RawText: "이미지가 있는 본문.\n---\n답글.", SplitMode: thread.ModeManual})
	if err != nil {
		e.t.Fatal(err)
	}
	if len(mediaIDs) > 0 {
		if _, err := e.contents.SetPartMedia(e.ctx, d.ID, d.EditVersion, 0, mediaIDs, "upload"); err != nil {
			e.t.Fatal(err)
		}
	}
	return d.ID
}

func (e *env) publish(cid string) string {
	e.t.Helper()
	id, err := e.pub.PostNow(e.ctx, publish.PostNowInput{ContentID: cid, Confirm: true})
	if err != nil {
		e.t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		j, err := e.queue.Claim(e.ctx, []string{publish.JobKind}, time.Minute)
		if err != nil {
			e.t.Fatal(err)
		}
		if j == nil {
			break
		}
		_ = e.queue.Finish(e.ctx, j, e.pub.HandleRun(e.ctx, j))
	}
	return id
}

func (e *env) state(id string) (string, *time.Time) {
	e.t.Helper()
	var st string
	var anchor *time.Time
	if err := e.pool.QueryRow(e.ctx, `SELECT state, retention_anchor_at FROM media_assets WHERE id = $1`, id).Scan(&st, &anchor); err != nil {
		e.t.Fatal(err)
	}
	return st, anchor
}

func (e *env) sweep() retention.Report {
	e.t.Helper()
	r, err := e.ret.Sweep(e.ctx)
	if err != nil {
		e.t.Fatal(err)
	}
	return r
}

func (e *env) days(n int) { e.clk.Advance(time.Duration(n) * 24 * time.Hour) }

func TestPublishedImagesAreCleanedAfterRetentionAndRecordsKept(t *testing.T) {
	e := newEnv(t)
	unpublished := e.image()
	used := e.image()
	cid := e.contentWith(used)
	runID := e.publish(cid)
	completedAt := e.clk.Now()
	if st, anchor := e.state(used); st != "active" || anchor == nil || !anchor.Equal(completedAt) {
		t.Fatalf("anchor after completion = %s %v", st, anchor)
	}
	if _, anchor := e.state(unpublished); anchor != nil {
		t.Fatal("미게시 이미지에는 보관 기준이 없어야 한다")
	}
	e.days(13)
	if r := e.sweep(); r.Claimed != 0 {
		t.Fatalf("too early: %+v", r)
	}
	e.days(2) // 완료 후 15일
	if r := e.sweep(); r.Deleted != 1 || r.Failed != 0 {
		t.Fatalf("sweep = %+v", r)
	}
	a, err := e.media.Get(e.ctx, used)
	if err != nil || a.State != "deleted" || a.DeletedAt == nil || a.AltText != "보관 확인용" {
		t.Fatalf("deleted asset = %+v %v", a, err)
	}
	if _, _, err := e.media.File(e.ctx, used, false); err == nil {
		t.Fatal("object should be gone from storage")
	}
	if st, _ := e.state(unpublished); st != "active" {
		t.Fatalf("한 번도 게시하지 않은 이미지는 지우지 않는다: %s", st)
	}
	// 게시 기록·X 링크·원문은 그대로
	v, _ := e.pub.Run(e.ctx, runID)
	if v.State != "completed" || v.Parts[0].URL == nil || v.Parts[0].Text == "" || v.Parts[0].MediaCount != 1 {
		t.Fatalf("run record = %+v", v)
	}
	d, _ := e.contents.Get(e.ctx, cid)
	if !strings.Contains(d.Revision.RawText, "이미지가 있는 본문") || len(d.Revision.Media) != 1 || d.Revision.Media[0].State != "deleted" {
		t.Fatalf("content after cleanup = %+v", d.Revision.Media)
	}
	list, _ := e.notify.List(e.ctx, notification.ListOptions{})
	if len(list) == 0 || list[0].GroupKey != "retention:done" || list[0].Severity != notification.Info {
		t.Fatalf("notification = %+v", list)
	}
	// 정리된 이미지가 붙은 글은 다시 게시하지 않는다
	if _, err := e.pub.PostNow(e.ctx, publish.PostNowInput{ContentID: cid, Confirm: true}); err == nil || !strings.Contains(err.Error()+errFields(err), "정리된 이미지") {
		t.Fatalf("republish with cleaned image = %v", err)
	}
	if r := e.sweep(); r.Claimed != 0 || r.Deleted != 0 {
		t.Fatalf("second sweep should be idle: %+v", r)
	}
}

func errFields(err error) string {
	var ve *publish.ValidationError
	if errors.As(err, &ve) {
		return ve.Fields["content"]
	}
	return ""
}

func TestReuseExtendsRetentionAndActiveReferencesProtect(t *testing.T) {
	e := newEnv(t)
	img := e.image()
	a := e.contentWith(img)
	e.publish(a)
	b := e.contentWith(img) // 같은 이미지를 다음 글 초안에도 쓴다
	e.days(15)
	if r := e.sweep(); r.Claimed != 0 {
		t.Fatalf("초안이 쓰는 이미지는 보호: %+v", r)
	}
	e.days(-5) // 10일째에 두 번째 글 게시
	e.publish(b)
	_, anchor := e.state(img)
	if anchor == nil || !anchor.Equal(e.clk.Now()) {
		t.Fatalf("재사용하면 보관 기준이 마지막 완료 시각으로 바뀐다: %v", anchor)
	}
	e.days(5) // 첫 게시 15일, 두 번째 게시 5일
	if r := e.sweep(); r.Claimed != 0 {
		t.Fatalf("최근 완료 기준이라 아직 보관: %+v", r)
	}
	e.days(10) // 두 번째 게시 15일
	if r := e.sweep(); r.Deleted != 1 {
		t.Fatalf("expired = %+v", r)
	}

	// 일부 완료(답글 실패)인 묶음의 이미지는 기준을 정하지 않고 계속 보호한다
	q := e.image()
	c := e.contentWith(q)
	e.x.Fail = func(op string, req xapi.PostRequest) error {
		if req.ReplyToID != "" {
			return &integration.Error{Class: integration.Review, Provider: "x", Op: "post", HTTPStatus: 403, Message: "답글이 거부됐어요"}
		}
		return nil
	}
	runID := e.publish(c)
	e.x.Fail = nil
	if v, _ := e.pub.Run(e.ctx, runID); v.State != "partial" {
		t.Fatalf("run = %s", v.State)
	}
	st, anchor := e.state(q)
	if st != "active" || anchor != nil {
		t.Fatalf("partial run image = %s %v", st, anchor)
	}
	// 예전 완료로 기준이 오래됐어도, 끝나지 않은 실행이 쓰면 지우지 않는다
	if _, err := e.pool.Exec(e.ctx, `UPDATE media_assets SET retention_anchor_at = $2 WHERE id = $1`, q, e.clk.Now().AddDate(0, 0, -40)); err != nil {
		t.Fatal(err)
	}
	if r := e.sweep(); r.Claimed != 0 {
		t.Fatalf("partial run reference must protect: %+v", r)
	}
	// 그만두면 그때부터 보관 기간을 센다
	if err := e.pub.Abandon(e.ctx, runID); err != nil {
		t.Fatal(err)
	}
	if _, anchor := e.state(q); anchor == nil || !anchor.Equal(e.clk.Now()) {
		t.Fatalf("abandon anchors = %v", anchor)
	}
}

func TestAttachRacingSweepNeverLeavesDeletedReference(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 15; i++ {
		img := e.image()
		e.publish(e.contentWith(img))
		draft := e.contentWith()
		d, _ := e.contents.Get(e.ctx, draft)
		e.days(15)
		var wg sync.WaitGroup
		var attachErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, attachErr = e.contents.SetPartMedia(e.ctx, draft, d.EditVersion, 0, []string{img}, "upload")
		}()
		go func() {
			defer wg.Done()
			_, _ = e.ret.Sweep(e.ctx)
		}()
		wg.Wait()
		_, _ = e.ret.Sweep(e.ctx) // 앞선 정리가 건너뛴 경우까지 한 번 더
		st, _ := e.state(img)
		var refs int
		_ = e.pool.QueryRow(e.ctx, `SELECT count(*) FROM part_media pm JOIN contents c ON c.current_revision_id = pm.revision_id WHERE c.id = $1 AND pm.media_id = $2`, draft, img).Scan(&refs)
		switch {
		case attachErr == nil && (refs != 1 || st != "active"):
			t.Fatalf("iteration %d: attached but image %s (refs %d)", i, st, refs)
		case attachErr != nil && (refs != 0 || st != "deleted"):
			t.Fatalf("iteration %d: attach failed (%v) but image %s refs %d", i, attachErr, st, refs)
		}
	}
}

func TestDeleteFailureIsRetriedAndStaleClaimsRecover(t *testing.T) {
	e := newEnv(t)
	img := e.image()
	e.publish(e.contentWith(img))
	e.days(15)
	e.store.failDelete.Store(true)
	if r := e.sweep(); r.Failed != 1 || r.Deleted != 0 {
		t.Fatalf("failing sweep = %+v", r)
	}
	if st, _ := e.state(img); st != "delete_failed" {
		t.Fatalf("state = %s", st)
	}
	list, _ := e.notify.List(e.ctx, notification.ListOptions{})
	if len(list) == 0 || list[0].GroupKey != "retention:failed" || list[0].Link != "/images?filter=failed" {
		t.Fatalf("failure notification = %+v", list)
	}
	// 바로 다시 돌리면 간격 전이라 건너뛰고, 1시간 뒤에는 다시 시도한다
	e.store.failDelete.Store(false)
	if r := e.sweep(); r.Retried != 0 {
		t.Fatalf("retry too soon: %+v", r)
	}
	e.clk.Advance(61 * time.Minute)
	if r := e.sweep(); r.Retried != 1 || r.Deleted != 1 {
		t.Fatalf("retry = %+v", r)
	}
	for _, n := range func() []notification.Notification { l, _ := e.notify.List(e.ctx, notification.ListOptions{}); return l }() {
		if n.GroupKey == "retention:failed" && n.ResolvedAt == nil {
			t.Fatal("failure notification should resolve after retry succeeds")
		}
	}

	// 선점 직후 프로세스가 죽어 'deleting'으로 남은 경우: 10분 뒤 다시 지운다
	img2 := e.image()
	e.publish(e.contentWith(img2))
	e.days(15)
	if _, err := e.pool.Exec(e.ctx, `UPDATE media_assets SET state = 'deleting', delete_claimed_at = $2 WHERE id = $1`, img2, e.clk.Now()); err != nil {
		t.Fatal(err)
	}
	if r := e.sweep(); r.Retried != 0 {
		t.Fatalf("fresh claim must not be stolen: %+v", r)
	}
	e.clk.Advance(11 * time.Minute)
	if r := e.sweep(); r.Retried != 1 || r.Deleted != 1 {
		t.Fatalf("stale claim = %+v", r)
	}
	// 저장 도중 끊겨 'pending'으로 남은 행은 하루 뒤 지운다
	if _, err := e.pool.Exec(e.ctx, `INSERT INTO media_assets (storage_key, source, content_type, bytes, width, height, sha256, state, created_at)
		VALUES ('pending:x', 'upload', 'image/png', 1, 1, 1, 'x', 'pending', $1)`, e.clk.Now().Add(-25*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if r := e.sweep(); r.PendingRemoved != 1 {
		t.Fatalf("pending = %+v", r)
	}
}

func TestOrphansOnlyInsidePrefixAfterGrace(t *testing.T) {
	e := newEnv(t)
	keep := e.image()
	if err := e.store.Put(e.ctx, "media/2026/10/orphan.png", "image/png", []byte("x")); err != nil {
		t.Fatal(err)
	}
	// 방금 올라간 고아는 유예 기간 동안 둔다 (DB 기록이 늦게 끝나는 업로드일 수 있다). 파일 시각은 실제 시계라 시계를 맞춘다
	e.clk.Set(time.Now())
	if n, err := e.media.CleanOrphans(e.ctx, time.Hour, 100); err != nil || n != 0 {
		t.Fatalf("fresh orphan = %d %v", n, err)
	}
	e.clk.Set(time.Now().Add(2 * time.Hour))
	if n, err := e.media.CleanOrphans(e.ctx, time.Hour, 100); err != nil || n != 1 {
		t.Fatalf("old orphan = %d %v", n, err)
	}
	if _, _, err := e.media.File(e.ctx, keep, false); err != nil {
		t.Fatalf("referenced object must stay: %v", err)
	}
}
