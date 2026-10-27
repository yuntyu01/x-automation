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

func TestStateNotificationsDoNotPileUp(t *testing.T) {
	pool := pgtest.New(t)
	ctx := context.Background()
	svc := notification.NewService(pool)
	n := notification.New{Severity: notification.Warning, Category: "stock", GroupKey: "stock:shortage", Title: "곧 게시할 글이 부족해요", Body: "예약 2개"}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := svc.Upsert(ctx, n); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	list, _ := svc.List(ctx, notification.ListOptions{})
	if len(list) != 1 || list[0].Count != 1 {
		t.Fatalf("state upsert = %+v", list)
	}
	// 내용이 바뀌면 갱신만 한다
	n.Body = "예약 3개"
	_ = svc.Upsert(ctx, n)
	list, _ = svc.List(ctx, notification.ListOptions{})
	if len(list) != 1 || list[0].Body != "예약 3개" || list[0].Count != 1 {
		t.Fatalf("updated = %+v", list)
	}
	// 읽은 뒤에도 상태가 계속되면 새로 만들지 않는다 (매분 점검이 알림함을 채우지 않게)
	_ = svc.MarkRead(ctx, list[0].ID)
	for i := 0; i < 3; i++ {
		_ = svc.Upsert(ctx, n)
	}
	if c, _ := svc.Counts(ctx); c.Unread != 0 {
		t.Fatalf("unread after read = %d", c.Unread)
	}
	// 풀렸다가 다시 생기면 새 알림
	_ = svc.Resolve(ctx, "stock:shortage")
	_ = svc.Upsert(ctx, n)
	list, _ = svc.List(ctx, notification.ListOptions{})
	if len(list) != 2 || list[0].ResolvedAt != nil || list[0].ReadAt != nil {
		t.Fatalf("after resolve = %+v", list)
	}
}
