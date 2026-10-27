package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
)

// 여러 인스턴스가 동시에 떠도 마이그레이션은 하나만 실행한다
const migrationLockID = 7243100417

var migrationName = regexp.MustCompile(`^(\d{4})_[a-z0-9_]+\.sql$`)

type Migration struct {
	Version  int
	Name     string
	SQL      string
	Checksum string
}

// LoadMigrations는 embed된 파일을 버전 순으로 읽는다.
func LoadMigrations(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	var out []Migration
	seen := map[int]string{}
	for _, e := range entries {
		if e.IsDir() || !migrationName.MatchString(e.Name()) {
			continue
		}
		m := migrationName.FindStringSubmatch(e.Name())
		v, _ := strconv.Atoi(m[1])
		if prev, dup := seen[v]; dup {
			return nil, fmt.Errorf("마이그레이션 버전 중복: %s, %s", prev, e.Name())
		}
		seen[v] = e.Name()
		b, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(b)
		out = append(out, Migration{Version: v, Name: e.Name(), SQL: string(b), Checksum: hex.EncodeToString(sum[:])})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// Migrate는 아직 적용하지 않은 마이그레이션을 순서대로 적용한다.
// 이미 적용한 파일의 내용이 바뀌었으면 적용하지 않고 멈춘다 — 운영 DB를 추측으로 고치지 않는다.
func Migrate(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS) (applied []string, err error) {
	migs, err := LoadMigrations(fsys)
	if err != nil {
		return nil, err
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLockID); err != nil {
		return nil, fmt.Errorf("마이그레이션 잠금 실패: %w", err)
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, migrationLockID) }()

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version integer PRIMARY KEY,
		name text NOT NULL,
		checksum text NOT NULL,
		applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return nil, err
	}
	rows, err := conn.Query(ctx, `SELECT version, checksum FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	done := map[int]string{}
	for rows.Next() {
		var v int
		var sum string
		if err := rows.Scan(&v, &sum); err != nil {
			rows.Close()
			return nil, err
		}
		done[v] = sum
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	known := map[int]bool{}
	for _, m := range migs {
		known[m.Version] = true
		if sum, ok := done[m.Version]; ok {
			if sum != m.Checksum {
				return applied, fmt.Errorf("이미 적용한 마이그레이션 %s의 내용이 바뀌었습니다. 파일을 되돌리고 새 마이그레이션으로 변경하세요", m.Name)
			}
			continue
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return applied, err
		}
		if _, err := tx.Exec(ctx, m.SQL); err != nil {
			_ = tx.Rollback(ctx)
			return applied, fmt.Errorf("마이그레이션 %s 실패: %w", m.Name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version, name, checksum) VALUES ($1, $2, $3)`,
			m.Version, m.Name, m.Checksum); err != nil {
			_ = tx.Rollback(ctx)
			return applied, err
		}
		if err := tx.Commit(ctx); err != nil {
			return applied, err
		}
		applied = append(applied, m.Name)
	}
	for v := range done {
		if !known[v] {
			return applied, fmt.Errorf("DB에 이 버전이 모르는 마이그레이션 %d가 있습니다. 더 새 버전의 앱이 적용한 것일 수 있습니다", v)
		}
	}
	return applied, nil
}

// SchemaReady는 embed된 최신 마이그레이션까지 적용됐는지 확인한다 (/readyz).
func SchemaReady(ctx context.Context, db DB, fsys fs.FS) (bool, error) {
	migs, err := LoadMigrations(fsys)
	if err != nil || len(migs) == 0 {
		return false, err
	}
	latest := migs[len(migs)-1].Version
	var v *int
	if err := db.QueryRow(ctx, `SELECT max(version) FROM schema_migrations`).Scan(&v); err != nil {
		return false, nil
	}
	return v != nil && *v >= latest, nil
}
