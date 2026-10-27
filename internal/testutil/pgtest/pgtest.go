// Package pgtest는 통합 테스트용 격리 DB를 만든다.
//
// TEST_DATABASE_URL(데이터베이스를 만들 수 있는 계정)이 있어야 한다. 없으면 테스트를 건너뛴다.
// 테스트마다 무작위 이름의 DB를 만들고 마이그레이션을 적용한 뒤, 끝나면 지운다.
package pgtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yuntyu01/x-automation/internal/store"
	"github.com/yuntyu01/x-automation/migrations"
)

// AdminURL은 테스트 DB를 만들 때 쓰는 연결 문자열이다.
func AdminURL(t testing.TB) string {
	t.Helper()
	u := os.Getenv("TEST_DATABASE_URL")
	if u == "" {
		t.Skip("TEST_DATABASE_URL이 없어 DB 통합 테스트를 건너뜁니다 (README의 테스트 절 참고)")
	}
	return u
}

// New는 마이그레이션까지 적용한 새 DB의 풀을 돌려준다.
func New(t testing.TB) *pgxpool.Pool {
	t.Helper()
	pool, _ := NewWithURL(t)
	return pool
}

// NewWithURL은 풀과 그 DB의 연결 문자열을 함께 돌려준다 (두 번째 풀·워커 경쟁 테스트용).
func NewWithURL(t testing.TB) (*pgxpool.Pool, string) {
	t.Helper()
	pool, dbURL := NewEmpty(t)
	if _, err := store.Migrate(context.Background(), pool, migrations.FS); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pool, dbURL
}

// NewEmpty는 마이그레이션을 적용하지 않은 빈 DB를 만든다.
func NewEmpty(t testing.TB) (*pgxpool.Pool, string) {
	t.Helper()
	admin := AdminURL(t)
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatalf("test db connect: %v", err)
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	name := "xa_test_" + hex.EncodeToString(b)
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		_ = conn.Close(ctx)
		t.Fatalf("create test db: %v", err)
	}
	_ = conn.Close(ctx)

	u, _ := url.Parse(admin)
	u.Path = "/" + name
	dbURL := u.String()
	pool, err := store.Open(ctx, dbURL)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		c, err := pgx.Connect(cctx, admin)
		if err != nil {
			return
		}
		defer c.Close(cctx)
		_, _ = c.Exec(cctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	})
	return pool, dbURL
}
