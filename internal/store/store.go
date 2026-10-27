// Package store는 PostgreSQL 연결·트랜잭션·마이그레이션을 담당한다.
// 각 도메인 패키지는 여기서 받은 풀로 자기 테이블의 SQL을 직접 실행한다.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB는 도메인 패키지가 공통으로 쓰는 질의 인터페이스다 (풀과 트랜잭션 모두 만족).
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Open은 풀을 만들고 한 번 연결을 확인한다.
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("DATABASE_URL 해석 실패: %w", sanitize(err))
	}
	cfg.MaxConns = 10
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.ConnConfig.RuntimeParams["application_name"] = "x-automation"
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("DB 풀 생성 실패: %w", sanitize(err))
	}
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("DB 연결 실패: %w", sanitize(err))
	}
	return pool, nil
}

// sanitize는 연결 문자열의 비밀번호가 오류 메시지에 실리지 않게 한다.
func sanitize(err error) error {
	var pgErr *pgconn.ConnectError
	if errors.As(err, &pgErr) {
		return errors.New("데이터베이스에 연결할 수 없습니다 (호스트·포트·계정을 확인하세요)")
	}
	return err
}

// InTx는 fn을 트랜잭션 안에서 실행한다. fn이 오류를 돌려주면 롤백한다.
func InTx(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// IsUniqueViolation은 고유 제약 위반인지 알려 준다 (중복 작업·중복 배정 감지).
func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// IsNoRows는 행이 없는 경우다.
func IsNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
