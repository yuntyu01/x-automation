package notification_test

import (
	"context"
	"sync"
	"testing"

	"github.com/yuntyu01/x-automation/internal/notification"
	"github.com/yuntyu01/x-automation/internal/testutil/pgtest"
)

func TestRepeatedFailuresAreGrouped(t *testing.T) {
	pool := pgtest.New(t)
	ctx := context.Background()
	svc := notification.NewService(pool)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := svc.Notify(ctx, notification.New{Severity: notification.Warning, Category: "publish", GroupKey: "publish:auth", Title: "X 인증이 만료됐어요"}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	list, err := svc.List(ctx, notification.ListOptions{})
	if err != nil || len(list) != 1 || list[0].Count != 8 {
		t.Fatalf("grouped = %+v %v", list, err)
	}
	c, _ := svc.Counts(ctx)
	if c.Unread != 1 {
		t.Fatalf("unread = %d", c.Unread)
	}
	// 읽은 뒤 같은 원인이 다시 생기면 새 알림이 된다 (누적 상태를 다시 알린다)
	if err := svc.MarkRead(ctx, list[0].ID); err != nil {
		t.Fatal(err)
	}
	_ = svc.Notify(ctx, notification.New{Severity: notification.Critical, Category: "publish", GroupKey: "publish:auth", Title: "X 인증이 만료됐어요"})
	list, _ = svc.List(ctx, notification.ListOptions{})
	if len(list) != 2 || list[0].Count != 1 || list[0].Severity != notification.Critical {
		t.Fatalf("after read = %+v", list)
	}
	if n, _ := svc.MarkAllRead(ctx); n != 1 {
		t.Fatalf("mark all = %d", n)
	}
}
