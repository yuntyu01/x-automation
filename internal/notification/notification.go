// Package notification은 앱 내부 알림만 다룬다 (텔레그램·이메일·브라우저 푸시 없음).
// 같은 원인의 반복은 group_key로 묶어 하나를 갱신한다.
package notification

import (
	"context"
	"time"

	"github.com/yuntyu01/x-automation/internal/store"
)

type Severity string

const (
	Critical Severity = "critical"
	Warning  Severity = "warning"
	Info     Severity = "info"
)

type Notification struct {
	ID         int64      `json:"id"`
	Severity   Severity   `json:"severity"`
	Category   string     `json:"category"`
	GroupKey   string     `json:"group_key"`
	Title      string     `json:"title"`
	Body       string     `json:"body"`
	Link       string     `json:"link"`
	Count      int        `json:"count"`
	FirstAt    time.Time  `json:"first_at"`
	LastAt     time.Time  `json:"last_at"`
	ReadAt     *time.Time `json:"read_at"`
	ResolvedAt *time.Time `json:"resolved_at"`
}

// New는 알림 하나를 만든다. GroupKey가 같고 아직 읽지 않은 알림이 있으면 그 알림의 횟수를 올린다.
type New struct {
	Severity Severity
	Category string // generation · publish · auth · stock · budget · retention · system
	GroupKey string
	Title    string
	Body     string
	Link     string
}

type Service struct{ db store.DB }

func NewService(db store.DB) *Service { return &Service{db: db} }

func (s *Service) Notify(ctx context.Context, n New) error {
	if n.GroupKey == "" {
		n.GroupKey = n.Category + ":" + n.Title
	}
	// 열린 묶음이 있으면 갱신, 없으면 새로 만든다 (부분 고유 인덱스로 경쟁 시에도 하나만 남는다)
	_, err := s.db.Exec(ctx, `INSERT INTO notifications (severity, category, group_key, title, body, link)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (group_key) WHERE read_at IS NULL AND resolved_at IS NULL
		DO UPDATE SET count = notifications.count + 1, last_at = now(), title = EXCLUDED.title,
			body = EXCLUDED.body, link = EXCLUDED.link,
			severity = CASE WHEN EXCLUDED.severity = 'critical' OR notifications.severity = 'critical' THEN 'critical'
			                WHEN EXCLUDED.severity = 'warning' OR notifications.severity = 'warning' THEN 'warning'
			                ELSE 'info' END`,
		string(n.Severity), n.Category, n.GroupKey, n.Title, n.Body, n.Link)
	return err
}

// Upsert는 "지금 계속되는 상태"를 알린다 (콘텐츠 부족·예산 한도·게시 막힘 등 주기 점검 결과).
// 같은 묶음이 해결되지 않은 채 있으면 내용만 갱신하고 횟수를 올리지 않는다. 읽은 뒤에도 해결 전에는 다시 만들지 않는다.
// 상태가 풀리면 Resolve로 닫고, 다시 생기면 새 알림이 된다.
func (s *Service) Upsert(ctx context.Context, n New) error {
	if n.GroupKey == "" {
		n.GroupKey = n.Category + ":" + n.Title
	}
	tag, err := s.db.Exec(ctx, `UPDATE notifications SET title = $2, body = $3, link = $4, severity = $5
		WHERE group_key = $1 AND resolved_at IS NULL AND (title, body, link, severity) IS DISTINCT FROM ($2, $3, $4, $5)`,
		n.GroupKey, n.Title, n.Body, n.Link, string(n.Severity))
	if err != nil || tag.RowsAffected() > 0 {
		return err
	}
	_, err = s.db.Exec(ctx, `INSERT INTO notifications (severity, category, group_key, title, body, link)
		SELECT $1, $2, $3, $4, $5, $6 WHERE NOT EXISTS (SELECT 1 FROM notifications WHERE group_key = $3 AND resolved_at IS NULL)
		ON CONFLICT (group_key) WHERE read_at IS NULL AND resolved_at IS NULL DO NOTHING`,
		string(n.Severity), n.Category, n.GroupKey, n.Title, n.Body, n.Link)
	return err
}

// Resolve는 문제가 해결된 묶음을 닫는다 (예: 인증 복구). 읽음과 별개다.
func (s *Service) Resolve(ctx context.Context, groupKey string) error {
	_, err := s.db.Exec(ctx, `UPDATE notifications SET resolved_at = now() WHERE group_key = $1 AND resolved_at IS NULL`, groupKey)
	return err
}

type ListOptions struct {
	UnreadOnly bool
	Before     int64 // 커서 (id 미만)
	Limit      int
}

func (s *Service) List(ctx context.Context, o ListOptions) ([]Notification, error) {
	if o.Limit <= 0 || o.Limit > 100 {
		o.Limit = 30
	}
	rows, err := s.db.Query(ctx, `SELECT id, severity, category, group_key, title, body, link, count, first_at, last_at, read_at, resolved_at
		FROM notifications WHERE ($1 = false OR read_at IS NULL) AND ($2 = 0 OR id < $2)
		ORDER BY last_at DESC, id DESC LIMIT $3`, o.UnreadOnly, o.Before, o.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Notification{}
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.Severity, &n.Category, &n.GroupKey, &n.Title, &n.Body, &n.Link, &n.Count,
			&n.FirstAt, &n.LastAt, &n.ReadAt, &n.ResolvedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

type Counts struct {
	Unread   int `json:"unread"`
	Critical int `json:"critical"`
}

func (s *Service) Counts(ctx context.Context) (Counts, error) {
	var c Counts
	err := s.db.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE severity = 'critical')
		FROM notifications WHERE read_at IS NULL`).Scan(&c.Unread, &c.Critical)
	return c, err
}

func (s *Service) MarkRead(ctx context.Context, id int64) error {
	_, err := s.db.Exec(ctx, `UPDATE notifications SET read_at = now() WHERE id = $1 AND read_at IS NULL`, id)
	return err
}

func (s *Service) MarkAllRead(ctx context.Context) (int64, error) {
	tag, err := s.db.Exec(ctx, `UPDATE notifications SET read_at = now() WHERE read_at IS NULL`)
	return tag.RowsAffected(), err
}
