package budget_test

import (
	"context"
	"sync"
	"testing"

	"github.com/yuntyu01/x-automation/internal/budget"
	"github.com/yuntyu01/x-automation/internal/settings"
	"github.com/yuntyu01/x-automation/internal/testutil/pgtest"
)

func TestConcurrentReservationsNeverExceedLimit(t *testing.T) {
	pool := pgtest.New(t)
	ctx := context.Background()
	repo := settings.NewRepo(pool)
	st, _ := repo.Get(ctx)
	p := st.Policy
	p.Budget.MonthlyLimitMicros = 1_000_000 // $1
	p.Budget.OnLimit = "stop_generation"
	if _, err := repo.Update(ctx, st.Version, st.OperationMode, st.Timezone, p); err != nil {
		t.Fatal(err)
	}
	svc := budget.NewService(pool, nil, repo)
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok, blocked := 0, 0
	var won []string // 어느 요청이 막힐지는 실행마다 다르다. 아래 정산·해제는 예약에 성공한 키로 한다
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := "gen:" + string(rune('a'+i))
			err := svc.Reserve(ctx, key, budget.AIText, 100_000) // $0.10씩
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				ok++
				won = append(won, key)
			} else if budget.IsExceeded(err) {
				blocked++
			} else {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if ok != 10 || blocked != 2 {
		t.Fatalf("ok=%d blocked=%d (limit allows exactly 10)", ok, blocked)
	}
	settled, released := won[0], won[1]
	// 재시도는 같은 예약을 다시 쓴다
	if err := svc.Reserve(ctx, settled, budget.AIText, 100_000); err != nil {
		t.Fatalf("idempotent reserve = %v", err)
	}
	// 생성만 중지 정책이면 게시 비용은 막지 않는다
	if err := svc.Reserve(ctx, "post:1", budget.XPost, 15_000); err != nil {
		t.Fatalf("posting should pass under stop_generation: %v", err)
	}
	// 정산: 같은 사용은 한 번만 기록
	for i := 0; i < 3; i++ {
		if err := svc.Record(ctx, budget.Entry{Category: budget.AIText, RefKey: settled, Micros: 90_000}, settled); err != nil {
			t.Fatal(err)
		}
	}
	_ = svc.Release(ctx, released)
	sum, err := svc.Summary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if sum.SpentMicros != 90_000 || sum.Counts["ai_text"] != 1 {
		t.Fatalf("summary = %+v", sum)
	}
	// 남은 예약: 10 - 정산 1 - 해제 1 = 8건 × 0.1 + 게시 0.015
	if sum.HeldMicros != 815_000 {
		t.Fatalf("held = %d", sum.HeldMicros)
	}
}

func TestNoLimitMeansNoReservation(t *testing.T) {
	pool := pgtest.New(t)
	ctx := context.Background()
	svc := budget.NewService(pool, nil, settings.NewRepo(pool))
	if err := svc.Reserve(ctx, "x", budget.AIImage, 999_999_999); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM budget_reservations`).Scan(&n)
	if n != 0 {
		t.Fatal("no reservation should be stored without a limit")
	}
	if budget.PricingKnown(settings.Defaults().Pricing) {
		t.Fatal("default pricing must be 'unknown' (no fabricated prices)")
	}
}
