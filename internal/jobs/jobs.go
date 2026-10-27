// Package jobs는 PostgreSQL 작업 큐다.
//
//   - 작업은 (kind, unique_key)로 한 번만 만들어진다 (같은 예약·revision·게시 부분의 중복 작업 방지)
//   - FOR UPDATE SKIP LOCKED로 선점하고 lease_owner·lease_expires_at·lease_version을 남긴다
//   - 결과 저장은 여전히 소유자인지(owner + version) 확인한다. 소유권을 잃은 워커의 결과는 버린다
//   - 외부 호출을 기다리는 동안 DB 트랜잭션을 잡지 않는다
//   - lease가 끝난 작업은 종류별 정책에 따라 다시 대기시키거나(안전한 작업) 결과 확인 필요(unknown)로 둔다
package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yuntyu01/x-automation/internal/clock"
	"github.com/yuntyu01/x-automation/internal/store"
)

var ErrLeaseLost = errors.New("작업 소유권을 잃었습니다")

type Job struct {
	ID           int64
	Kind         string
	UniqueKey    string
	Payload      json.RawMessage
	Attempts     int
	MaxAttempts  int
	LeaseOwner   string
	LeaseVersion int64
	RunAt        time.Time
}

func (j *Job) Decode(v any) error { return json.Unmarshal(j.Payload, v) }

type Queue struct {
	pool  *pgxpool.Pool
	clock clock.Clock
	owner string
}

func NewQueue(pool *pgxpool.Pool, c clock.Clock) *Queue {
	if c == nil {
		c = clock.System{}
	}
	host, _ := os.Hostname()
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return &Queue{pool: pool, clock: c, owner: fmt.Sprintf("%s-%d-%s", host, os.Getpid(), hex.EncodeToString(b))}
}

// WithOwner는 같은 프로세스 안에서 다른 인스턴스처럼 동작하는 큐를 만든다 (워커 경쟁 테스트).
func (q *Queue) WithOwner(owner string) *Queue {
	return &Queue{pool: q.pool, clock: q.clock, owner: owner}
}

func (q *Queue) Owner() string { return q.owner }

type EnqueueOptions struct {
	RunAt       time.Time
	MaxAttempts int
}

// Enqueue는 작업을 만든다. 같은 (kind, unique_key)가 이미 있으면 새로 만들지 않고 기존 ID를 돌려준다.
func (q *Queue) Enqueue(ctx context.Context, db store.DB, kind, key string, payload any, o EnqueueOptions) (id int64, created bool, err error) {
	if db == nil {
		db = q.pool
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, false, err
	}
	if o.RunAt.IsZero() {
		o.RunAt = q.clock.Now()
	}
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = 5
	}
	err = db.QueryRow(ctx, `INSERT INTO jobs (kind, unique_key, payload, run_at, max_attempts) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (kind, unique_key) DO NOTHING RETURNING id`, kind, key, raw, o.RunAt, o.MaxAttempts).Scan(&id)
	if store.IsNoRows(err) {
		err = db.QueryRow(ctx, `SELECT id FROM jobs WHERE kind = $1 AND unique_key = $2`, kind, key).Scan(&id)
		return id, false, err
	}
	return id, err == nil, err
}

// Claim은 실행할 작업 하나를 선점한다. 없으면 nil.
func (q *Queue) Claim(ctx context.Context, kinds []string, lease time.Duration) (*Job, error) {
	now := q.clock.Now()
	var j Job
	err := q.pool.QueryRow(ctx, `UPDATE jobs SET state = 'running', lease_owner = $1, lease_expires_at = $2,
		lease_version = lease_version + 1, attempts = attempts + 1, updated_at = $3
		WHERE id = (SELECT id FROM jobs WHERE state = 'queued' AND run_at <= $3 AND kind = ANY($4)
		            ORDER BY run_at, id FOR UPDATE SKIP LOCKED LIMIT 1)
		RETURNING id, kind, unique_key, payload, attempts, max_attempts, lease_owner, lease_version, run_at`,
		q.owner, now.Add(lease), now, kinds).
		Scan(&j.ID, &j.Kind, &j.UniqueKey, &j.Payload, &j.Attempts, &j.MaxAttempts, &j.LeaseOwner, &j.LeaseVersion, &j.RunAt)
	if store.IsNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &j, nil
}

// Heartbeat는 lease를 늘린다. 소유권을 잃었으면 ErrLeaseLost.
func (q *Queue) Heartbeat(ctx context.Context, j *Job, lease time.Duration) error {
	tag, err := q.pool.Exec(ctx, `UPDATE jobs SET lease_expires_at = $4, updated_at = now()
		WHERE id = $1 AND lease_owner = $2 AND lease_version = $3 AND state = 'running'`, j.ID, j.LeaseOwner, j.LeaseVersion, q.clock.Now().Add(lease))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
}

// StillOwner는 새 외부 호출을 시작하기 전에 소유권을 확인한다.
func (q *Queue) StillOwner(ctx context.Context, j *Job) (bool, error) {
	var ok bool
	err := q.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM jobs WHERE id = $1 AND lease_owner = $2 AND lease_version = $3
		AND state = 'running' AND lease_expires_at > $4)`, j.ID, j.LeaseOwner, j.LeaseVersion, q.clock.Now()).Scan(&ok)
	return ok, err
}

func (q *Queue) finish(ctx context.Context, j *Job, state, errMsg, class string, runAt *time.Time, refundAttempt bool) error {
	sql := `UPDATE jobs SET state = $4, last_error = NULLIF($5, ''), error_class = NULLIF($6, ''), updated_at = now(),
		lease_owner = NULL, lease_expires_at = NULL,
		finished_at = CASE WHEN $4 IN ('succeeded', 'failed', 'canceled', 'unknown') THEN now() ELSE NULL END,
		run_at = COALESCE($7, run_at),
		attempts = CASE WHEN $8 THEN greatest(attempts - 1, 0) ELSE attempts END
		WHERE id = $1 AND lease_owner = $2 AND lease_version = $3 AND state = 'running'`
	tag, err := q.pool.Exec(ctx, sql, j.ID, j.LeaseOwner, j.LeaseVersion, state, errMsg, class, runAt, refundAttempt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
}

func (q *Queue) Complete(ctx context.Context, j *Job) error {
	return q.finish(ctx, j, "succeeded", "", "", nil, false)
}

// Retry는 시도 횟수 안이면 다시 대기시키고, 넘었으면 실패로 끝낸다.
func (q *Queue) Retry(ctx context.Context, j *Job, err error, at time.Time) error {
	if j.Attempts >= j.MaxAttempts {
		return q.finish(ctx, j, "failed", truncate(err.Error()), "retryable", nil, false)
	}
	return q.finish(ctx, j, "queued", truncate(err.Error()), "retryable", &at, false)
}

// Snooze는 시도 횟수를 쓰지 않고 나중으로 미룬다 (일시정지·허용 시간 밖 등 작업 탓이 아닌 대기).
func (q *Queue) Snooze(ctx context.Context, j *Job, at time.Time, reason string) error {
	return q.finish(ctx, j, "queued", reason, "", &at, true)
}

func (q *Queue) Fail(ctx context.Context, j *Job, err error, class string) error {
	return q.finish(ctx, j, "failed", truncate(err.Error()), class, nil, false)
}

// Unknown은 외부 요청을 보냈지만 결과가 불명확할 때다. 자동으로 다시 실행하지 않는다.
func (q *Queue) Unknown(ctx context.Context, j *Job, err error) error {
	return q.finish(ctx, j, "unknown", truncate(err.Error()), "unknown", nil, false)
}

// Cancel은 대기 중인 작업을 취소한다. 이미 실행 중인 작업은 취소하지 않는다 (외부 과금 요청은 되돌릴 수 없다).
func (q *Queue) Cancel(ctx context.Context, kind, key string) (bool, error) {
	tag, err := q.pool.Exec(ctx, `UPDATE jobs SET state = 'canceled', error_class = 'canceled', finished_at = now(), updated_at = now()
		WHERE kind = $1 AND unique_key = $2 AND state = 'queued'`, kind, key)
	return tag.RowsAffected() > 0, err
}

// Requeue는 끝난 작업을 다시 대기시킨다 (사용자가 확인 후 재시도를 고른 경우).
func (q *Queue) Requeue(ctx context.Context, kind, key string, at time.Time) (bool, error) {
	tag, err := q.pool.Exec(ctx, `UPDATE jobs SET state = 'queued', run_at = $3, attempts = 0, last_error = NULL, error_class = NULL,
		finished_at = NULL, updated_at = now() WHERE kind = $1 AND unique_key = $2 AND state IN ('failed', 'unknown', 'canceled')`, kind, key, at)
	return tag.RowsAffected() > 0, err
}

// RecoverPolicy는 lease가 끝난 작업을 어떻게 다룰지다.
type RecoverPolicy int

const (
	RecoverRequeue RecoverPolicy = iota // 같은 작업을 다시 해도 외부에 중복이 생기지 않는다
	RecoverUnknown                      // 외부 요청 중이었을 수 있다 → 결과 확인 필요
)

// RecoverExpired는 lease가 끝난 실행 중 작업을 정리한다 (워커가 죽었거나 재배포 중 겹친 경우).
func (q *Queue) RecoverExpired(ctx context.Context, policy func(kind string) RecoverPolicy) (requeued, unknown int, err error) {
	rows, err := q.pool.Query(ctx, `SELECT id, kind FROM jobs WHERE state = 'running' AND lease_expires_at < $1 FOR UPDATE SKIP LOCKED`, q.clock.Now())
	if err != nil {
		return 0, 0, err
	}
	type rec struct {
		id   int64
		kind string
	}
	var list []rec
	for rows.Next() {
		var r rec
		if err := rows.Scan(&r.id, &r.kind); err != nil {
			rows.Close()
			return 0, 0, err
		}
		list = append(list, r)
	}
	rows.Close()
	for _, r := range list {
		if policy(r.kind) == RecoverUnknown {
			tag, err := q.pool.Exec(ctx, `UPDATE jobs SET state = 'unknown', error_class = 'unknown', last_error = '실행 중 작업이 응답 없이 끊겼습니다 (결과 확인 필요)',
				lease_owner = NULL, lease_expires_at = NULL, finished_at = now(), updated_at = now()
				WHERE id = $1 AND state = 'running' AND lease_expires_at < $2`, r.id, q.clock.Now())
			if err != nil {
				return requeued, unknown, err
			}
			unknown += int(tag.RowsAffected())
			continue
		}
		tag, err := q.pool.Exec(ctx, `UPDATE jobs SET state = CASE WHEN attempts >= max_attempts THEN 'failed' ELSE 'queued' END,
			last_error = '실행 중 작업이 끊겨 다시 시도합니다', lease_owner = NULL, lease_expires_at = NULL, run_at = $2, updated_at = now(),
			finished_at = CASE WHEN attempts >= max_attempts THEN now() ELSE NULL END
			WHERE id = $1 AND state = 'running' AND lease_expires_at < $2`, r.id, q.clock.Now())
		if err != nil {
			return requeued, unknown, err
		}
		requeued += int(tag.RowsAffected())
	}
	return requeued, unknown, nil
}

// Backoff는 제한된 지수 대기 시간이다.
func Backoff(attempt int, base, maxWait time.Duration) time.Duration {
	d := base
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= maxWait {
			return maxWait
		}
	}
	return d
}

func truncate(s string) string {
	if len(s) > 1000 {
		return s[:1000]
	}
	return s
}
