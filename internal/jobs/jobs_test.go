package jobs

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuntyu01/x-automation/internal/clock"
	"github.com/yuntyu01/x-automation/internal/testutil/pgtest"
)

func TestEnqueueIsIdempotentAndClaimIsExclusive(t *testing.T) {
	pool := pgtest.New(t)
	ctx := context.Background()
	q := NewQueue(pool, nil)
	id1, created, err := q.Enqueue(ctx, nil, "k", "same", map[string]int{"n": 1}, EnqueueOptions{})
	if err != nil || !created {
		t.Fatal(err)
	}
	id2, created2, _ := q.Enqueue(ctx, nil, "k", "same", map[string]int{"n": 2}, EnqueueOptions{})
	if id1 != id2 || created2 {
		t.Fatal("duplicate unique key must not create a second job")
	}
	for i := 0; i < 20; i++ {
		_, _, _ = q.Enqueue(ctx, nil, "k", "job-"+string(rune('a'+i)), nil, EnqueueOptions{})
	}
	// 워커 두 개(다른 소유자)가 동시에 선점해도 같은 작업을 두 번 잡지 않는다
	a, b := q.WithOwner("A"), q.WithOwner("B")
	var mu sync.Mutex
	seen := map[int64]string{}
	var wg sync.WaitGroup
	for _, qq := range []*Queue{a, b} {
		wg.Add(1)
		go func(qq *Queue) {
			defer wg.Done()
			for {
				j, err := qq.Claim(ctx, []string{"k"}, time.Minute)
				if err != nil {
					t.Error(err)
					return
				}
				if j == nil {
					return
				}
				mu.Lock()
				if prev, dup := seen[j.ID]; dup {
					t.Errorf("job %d claimed by %s and %s", j.ID, prev, qq.Owner())
				}
				seen[j.ID] = qq.Owner()
				mu.Unlock()
				if err := qq.Complete(ctx, j); err != nil {
					t.Error(err)
				}
			}
		}(qq)
	}
	wg.Wait()
	if len(seen) != 21 {
		t.Fatalf("processed %d jobs, want 21", len(seen))
	}
}

func TestLeaseLossAndRecoveryPolicies(t *testing.T) {
	pool := pgtest.New(t)
	ctx := context.Background()
	fc := clock.NewFake(time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC))
	q := NewQueue(pool, fc)
	_, _, _ = q.Enqueue(ctx, nil, "safe", "s1", nil, EnqueueOptions{RunAt: fc.Now()})
	_, _, _ = q.Enqueue(ctx, nil, "unsafe", "u1", nil, EnqueueOptions{RunAt: fc.Now()})
	w1 := q.WithOwner("w1")
	js, _ := w1.Claim(ctx, []string{"safe"}, time.Minute)
	ju, _ := w1.Claim(ctx, []string{"unsafe"}, time.Minute)
	if js == nil || ju == nil {
		t.Fatal("claims")
	}
	fc.Advance(2 * time.Minute)
	req, unk, err := q.RecoverExpired(ctx, func(kind string) RecoverPolicy {
		if kind == "unsafe" {
			return RecoverUnknown
		}
		return RecoverRequeue
	})
	if err != nil || req != 1 || unk != 1 {
		t.Fatalf("recover = %d %d %v", req, unk, err)
	}
	// 소유권을 잃은 워커의 결과 저장은 거부된다
	if err := w1.Complete(ctx, js); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale owner complete = %v", err)
	}
	if ok, _ := w1.StillOwner(ctx, ju); ok {
		t.Fatal("expired lease must not be owner")
	}
	var state string
	_ = pool.QueryRow(ctx, `SELECT state FROM jobs WHERE unique_key = 'u1'`).Scan(&state)
	if state != "unknown" {
		t.Fatalf("unsafe job state = %s (must not be retried automatically)", state)
	}
	// 안전한 작업은 다른 워커가 다시 잡는다
	j2, _ := q.WithOwner("w2").Claim(ctx, []string{"safe"}, time.Minute)
	if j2 == nil || j2.Attempts != 2 {
		t.Fatalf("requeued job = %+v", j2)
	}
	// Snooze는 시도 횟수를 쓰지 않는다
	if err := q.WithOwner("w2").Snooze(ctx, j2, fc.Now().Add(time.Hour), "일시정지"); err != nil {
		t.Fatal(err)
	}
	var attempts int
	_ = pool.QueryRow(ctx, `SELECT attempts FROM jobs WHERE unique_key = 's1'`).Scan(&attempts)
	if attempts != 1 {
		t.Fatalf("snooze consumed attempt: %d", attempts)
	}
}

func TestWorkerRunsRetriesAndStopsGracefully(t *testing.T) {
	pool := pgtest.New(t)
	q := NewQueue(pool, nil)
	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	w := &Worker{Queue: q, Concurrency: 2, Poll: 50 * time.Millisecond, Lease: 3 * time.Second, Handlers: map[string]Handler{
		"flaky": func(ctx context.Context, j *Job) Result {
			if calls.Add(1) == 1 {
				return Result{Outcome: RetryLater, Err: errors.New("일시 오류"), At: time.Now()}
			}
			return OK()
		},
	}}
	_, _, _ = q.Enqueue(context.Background(), nil, "flaky", "f1", nil, EnqueueOptions{MaxAttempts: 3})
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var state string
		_ = pool.QueryRow(context.Background(), `SELECT state FROM jobs WHERE unique_key = 'f1'`).Scan(&state)
		if state == "succeeded" {
			break
		}
		w.Kick()
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	<-done
	if calls.Load() != 2 {
		t.Fatalf("calls = %d", calls.Load())
	}
}

// 종료 신호(SIGTERM → ctx 취소) 뒤: 진행 중 작업은 끝까지 결과를 저장하고, 새 작업은 선점하지 않는다
func TestShutdownFinishesInFlightAndStopsClaiming(t *testing.T) {
	pool := pgtest.New(t)
	q := NewQueue(pool, nil)
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	var once sync.Once
	w := &Worker{Queue: q, Concurrency: 1, Poll: 20 * time.Millisecond, Lease: 3 * time.Second, Handlers: map[string]Handler{
		"slow": func(hctx context.Context, j *Job) Result {
			once.Do(func() { close(started) })
			select {
			case <-time.After(300 * time.Millisecond): // 외부 요청을 기다리는 중
			case <-hctx.Done():
				return Result{Outcome: Unknown, Err: errors.New("핸들러 컨텍스트가 종료 신호로 취소되면 안 된다")}
			}
			return OK()
		},
	}}
	_, _, _ = q.Enqueue(context.Background(), nil, "slow", "s1", nil, EnqueueOptions{})
	_, _, _ = q.Enqueue(context.Background(), nil, "slow", "s2", nil, EnqueueOptions{RunAt: time.Now().Add(100 * time.Millisecond)})
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	<-started
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not stop")
	}
	var s1, s2 string
	_ = pool.QueryRow(context.Background(), `SELECT state FROM jobs WHERE unique_key = 's1'`).Scan(&s1)
	_ = pool.QueryRow(context.Background(), `SELECT state FROM jobs WHERE unique_key = 's2'`).Scan(&s2)
	if s1 != "succeeded" || s2 != "queued" {
		t.Fatalf("after shutdown: s1=%s s2=%s", s1, s2)
	}
}
