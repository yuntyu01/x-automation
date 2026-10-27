package jobs

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

type Outcome int

const (
	Done       Outcome = iota
	RetryLater         // 일시 오류: 제한된 backoff 후 다시 (시도 횟수 사용)
	Failed             // 재시도해도 같은 결과 (설정 수정·검토 필요)
	Unknown            // 외부 결과 불명확: 자동 재실행 금지
	Snoozed            // 작업 탓이 아닌 대기 (시도 횟수를 쓰지 않음)
)

type Result struct {
	Outcome Outcome
	Err     error
	At      time.Time // RetryLater·Snoozed의 다음 실행 시각
	Class   string    // Failed의 분류 (config | review | canceled)
}

func OK() Result { return Result{Outcome: Done} }

type Handler func(ctx context.Context, j *Job) Result

// Worker는 지정한 종류의 작업을 선점해 실행한다. 종료 신호를 받으면 새 선점을 멈추고 진행 중인 작업의 결과를 저장한다.
type Worker struct {
	Queue       *Queue
	Handlers    map[string]Handler
	Concurrency int
	Lease       time.Duration
	Poll        time.Duration
	Log         *slog.Logger

	kick chan struct{}
	once sync.Once
}

func (w *Worker) init() {
	w.once.Do(func() {
		w.kick = make(chan struct{}, 1)
		if w.Concurrency <= 0 {
			w.Concurrency = 1
		}
		if w.Lease <= 0 {
			w.Lease = 2 * time.Minute
		}
		if w.Poll <= 0 {
			w.Poll = 5 * time.Second
		}
		if w.Log == nil {
			w.Log = slog.Default()
		}
	})
}

// Kick은 대기 중인 폴링을 깨운다 (새 작업을 넣은 직후).
func (w *Worker) Kick() {
	w.init()
	select {
	case w.kick <- struct{}{}:
	default:
	}
}

func (w *Worker) kinds() []string {
	out := make([]string, 0, len(w.Handlers))
	for k := range w.Handlers {
		out = append(out, k)
	}
	return out
}

// Run은 ctx가 끝날 때까지 작업을 처리하고, 진행 중인 작업이 끝나면 돌아온다.
func (w *Worker) Run(ctx context.Context) {
	w.init()
	sem := make(chan struct{}, w.Concurrency)
	var wg sync.WaitGroup
	defer wg.Wait()
	kinds := w.kinds()
	for {
		select {
		case <-ctx.Done():
			return
		case sem <- struct{}{}:
		}
		j, err := w.Queue.Claim(ctx, kinds, w.Lease)
		if err != nil || j == nil {
			<-sem
			if err != nil && !errors.Is(err, context.Canceled) {
				w.Log.Warn("job claim failed", "err", err.Error())
			}
			select {
			case <-ctx.Done():
				return
			case <-w.kick:
			case <-time.After(w.Poll):
			}
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			w.process(ctx, j)
		}()
	}
}

func (w *Worker) process(parent context.Context, j *Job) {
	// 종료 신호가 와도 진행 중인 작업은 끝까지 결과를 저장한다 (핸들러가 각자 시간 제한을 둔다)
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	defer cancel()
	hbDone := make(chan struct{})
	go func() {
		defer close(hbDone)
		t := time.NewTicker(w.Lease / 3)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := w.Queue.Heartbeat(ctx, j, w.Lease); errors.Is(err, ErrLeaseLost) {
					w.Log.Warn("job lease lost; stopping", "job", j.ID, "kind", j.Kind)
					cancel()
					return
				}
			}
		}
	}()

	var res Result
	func() {
		defer func() {
			if p := recover(); p != nil {
				w.Log.Error("job handler panic", "job", j.ID, "kind", j.Kind, "panic", p)
				res = Result{Outcome: RetryLater, Err: errors.New("내부 오류"), At: time.Now().Add(time.Minute)}
			}
		}()
		res = w.Handlers[j.Kind](ctx, j)
	}()
	cancel()
	<-hbDone

	sctx, scancel := context.WithTimeout(context.WithoutCancel(parent), 10*time.Second)
	defer scancel()
	err := w.Queue.Finish(sctx, j, res)
	// 운영 로그: 사람이 봐야 하는 결과만 남긴다 (메시지는 사용자용 설명이며 토큰·원문은 담지 않는다)
	switch res.Outcome {
	case Failed:
		w.Log.Warn("job failed", "job", j.ID, "kind", j.Kind, "attempt", j.Attempts, "class", res.Class, "err", msg(res.Err))
	case Unknown:
		w.Log.Error("job result unknown", "job", j.ID, "kind", j.Kind, "attempt", j.Attempts, "err", msg(res.Err))
	case RetryLater:
		w.Log.Info("job will retry", "job", j.ID, "kind", j.Kind, "attempt", j.Attempts, "err", msg(res.Err))
	}
	if errors.Is(err, ErrLeaseLost) {
		w.Log.Warn("job result discarded: lease lost", "job", j.ID, "kind", j.Kind)
	} else if err != nil {
		w.Log.Error("job result save failed", "job", j.ID, "err", err.Error())
	}
}

func orErr(err error) error {
	if err == nil {
		return errors.New("알 수 없는 오류")
	}
	return err
}

func msg(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// Finish는 핸들러 결과를 작업 상태로 저장한다. 소유권을 잃었으면 ErrLeaseLost.
func (q *Queue) Finish(ctx context.Context, j *Job, res Result) error {
	switch res.Outcome {
	case Done:
		return q.Complete(ctx, j)
	case RetryLater:
		at := res.At
		if at.IsZero() {
			at = q.clock.Now().Add(Backoff(j.Attempts, 30*time.Second, 30*time.Minute))
		}
		return q.Retry(ctx, j, orErr(res.Err), at)
	case Snoozed:
		return q.Snooze(ctx, j, res.At, msg(res.Err))
	case Unknown:
		return q.Unknown(ctx, j, orErr(res.Err))
	}
	class := res.Class
	if class == "" {
		class = "config"
	}
	return q.Fail(ctx, j, orErr(res.Err), class)
}
