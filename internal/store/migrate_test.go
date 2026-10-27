package store_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/yuntyu01/x-automation/internal/store"
	"github.com/yuntyu01/x-automation/internal/testutil/pgtest"
	"github.com/yuntyu01/x-automation/migrations"
)

func TestMigrateFreshAndRerunIsSafe(t *testing.T) {
	pool, _ := pgtest.NewEmpty(t)
	ctx := context.Background()
	ready, _ := store.SchemaReady(ctx, pool, migrations.FS)
	if ready {
		t.Fatal("empty db must not be ready")
	}
	applied, err := store.Migrate(ctx, pool, migrations.FS)
	if err != nil || len(applied) == 0 {
		t.Fatalf("first migrate: %v %v", applied, err)
	}
	again, err := store.Migrate(ctx, pool, migrations.FS)
	if err != nil || len(again) != 0 {
		t.Fatalf("rerun should apply nothing: %v %v", again, err)
	}
	if ready, err := store.SchemaReady(ctx, pool, migrations.FS); !ready || err != nil {
		t.Fatalf("ready=%v err=%v", ready, err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schedule_windows`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("seed windows = %d err=%v", n, err)
	}
}

func TestConcurrentMigrateRunsOnce(t *testing.T) {
	pool, _ := pgtest.NewEmpty(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = store.Migrate(ctx, pool, migrations.FS)
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("concurrent migrate: %v", err)
		}
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&n)
	mig, _ := store.LoadMigrations(migrations.FS)
	if n != len(mig) {
		t.Fatalf("schema_migrations rows = %d, want %d", n, len(mig))
	}
}

func TestChangedMigrationIsRefused(t *testing.T) {
	pool, _ := pgtest.NewEmpty(t)
	ctx := context.Background()
	v1 := fstest.MapFS{"0001_a.sql": {Data: []byte("CREATE TABLE a (id int);")}}
	if _, err := store.Migrate(ctx, pool, v1); err != nil {
		t.Fatal(err)
	}
	changed := fstest.MapFS{"0001_a.sql": {Data: []byte("CREATE TABLE a (id bigint);")}}
	_, err := store.Migrate(ctx, pool, changed)
	if err == nil || !strings.Contains(err.Error(), "내용이 바뀌었습니다") {
		t.Fatalf("want checksum refusal, got %v", err)
	}
}

func TestFailedMigrationRollsBack(t *testing.T) {
	pool, _ := pgtest.NewEmpty(t)
	ctx := context.Background()
	bad := fstest.MapFS{
		"0001_ok.sql":  {Data: []byte("CREATE TABLE ok (id int);")},
		"0002_bad.sql": {Data: []byte("CREATE TABLE half (id int); SELECT broken_syntax(;")},
	}
	applied, err := store.Migrate(ctx, pool, bad)
	if err == nil || len(applied) != 1 {
		t.Fatalf("want failure after first, got %v %v", applied, err)
	}
	var exists bool
	_ = pool.QueryRow(ctx, `SELECT to_regclass('half') IS NOT NULL`).Scan(&exists)
	if exists {
		t.Fatal("partial migration must roll back")
	}
}
