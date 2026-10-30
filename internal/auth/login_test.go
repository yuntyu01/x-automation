package auth_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/yuntyu01/x-automation/internal/auth"
	"github.com/yuntyu01/x-automation/internal/clock"
	"github.com/yuntyu01/x-automation/internal/testutil/pgtest"
)

const pw = "correct-horse-battery"

func newService(t *testing.T) *auth.Service {
	t.Helper()
	hash, err := auth.HashPassword(pw, auth.Params{Memory: 8 * 1024, Time: 1, Threads: 1, KeyLen: 32, SaltLen: 16})
	if err != nil {
		t.Fatal(err)
	}
	fc := clock.NewFake(time.Date(2026, 10, 27, 3, 0, 0, 0, time.UTC))
	return auth.NewService(pgtest.New(t), auth.Config{Username: "admin", PasswordHash: hash, TTL: time.Hour}, fc)
}

// 한 곳에서 틀린 비밀번호를 거듭 보내도 다른 곳의 관리자 로그인은 막히지 않는다
func TestLockoutIsPerIP(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		if _, _, err := s.Login(ctx, "admin", "wrong-password", "203.0.113.7", "test"); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	var rl *auth.RateLimitedError
	if _, _, err := s.Login(ctx, "admin", pw, "203.0.113.7", "test"); !errors.As(err, &rl) {
		t.Fatalf("locked ip with right password = %v", err)
	}
	if _, _, err := s.Login(ctx, "admin", pw, "198.51.100.4", "test"); err != nil {
		t.Fatalf("other ip = %v", err)
	}
}

// 동시에 몰려온 요청도 실패 횟수를 하나씩 확인한다: 한도 직전에서 한꺼번에 보내도 하나만 비밀번호 확인까지 간다
func TestParallelLoginsRespectLimit(t *testing.T) {
	s := newService(t)
	ctx := context.Background()
	for i := 0; i < 9; i++ {
		_, _, _ = s.Login(ctx, "admin", "wrong-password", "203.0.113.7", "test")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := s.Login(ctx, "admin", "wrong-password", "203.0.113.7", "test")
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	checked := 0
	for err := range errs {
		var rl *auth.RateLimitedError
		switch {
		case errors.Is(err, auth.ErrInvalidCredentials):
			checked++
		case errors.As(err, &rl):
		default:
			t.Fatalf("unexpected %v", err)
		}
	}
	if checked != 1 {
		t.Fatalf("password checked %d times past the limit, want 1", checked)
	}
}
