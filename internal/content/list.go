package content

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Stage는 보관함 필터다 (content_overview.stage).
var Stages = []string{"draft", "review", "ready", "scheduled", "published", "problem", "archived"}

type Filter struct {
	Stage    string // 빈 값이면 보관된 것을 뺀 전체
	Category string
	Query    string    // 제목·원문 검색
	From     time.Time // 생성일 범위 (현지 날짜 경계를 호출부가 계산)
	To       time.Time
	Sort     string // updated | created | title | slot
	Limit    int
	Offset   int
}

type Item struct {
	ID               string     `json:"id"`
	Title            string     `json:"title"`
	Category         string     `json:"category"`
	Tags             []string   `json:"tags"`
	Source           string     `json:"source"`
	IsDemo           bool       `json:"is_demo"`
	Stage            string     `json:"stage"`
	Preview          string     `json:"preview"`
	PartCount        int        `json:"part_count"`
	ImageMode        string     `json:"image_mode"`
	ReviewState      string     `json:"review_state"`
	Publishable      bool       `json:"publishable"`
	Problems         []string   `json:"problems"`
	GenerationState  string     `json:"generation_state"`
	ReservationAt    *time.Time `json:"reservation_at"`
	ReservationState *string    `json:"reservation_state"`
	WaitReason       *string    `json:"wait_reason"`
	LastRunState     *string    `json:"last_run_state"`
	LastPublishedAt  *time.Time `json:"last_published_at"`
	RevisionNumber   int        `json:"revision_number"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type Page struct {
	Items  []Item         `json:"items"`
	Total  int            `json:"total"`
	Counts map[string]int `json:"counts"` // 단계별 개수 (필터 탭)
	Limit  int            `json:"limit"`
	Offset int            `json:"offset"`
}

func (s *Service) List(ctx context.Context, f Filter) (Page, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 20
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	var where []string
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if f.Category != "" {
		where = append(where, "category = "+arg(f.Category))
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		like := "%" + escapeLike(q) + "%"
		p := arg(like)
		where = append(where, fmt.Sprintf("(title ILIKE %s OR raw_text ILIKE %s)", p, p))
	}
	if !f.From.IsZero() {
		where = append(where, "created_at >= "+arg(f.From))
	}
	if !f.To.IsZero() {
		where = append(where, "created_at < "+arg(f.To))
	}
	base := "TRUE"
	if len(where) > 0 {
		base = strings.Join(where, " AND ")
	}

	counts := map[string]int{}
	rows, err := s.pool.Query(ctx, `SELECT stage, count(*) FROM content_overview WHERE `+base+` GROUP BY stage`, args...)
	if err != nil {
		return Page{}, err
	}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			rows.Close()
			return Page{}, err
		}
		counts[st] = n
	}
	rows.Close()
	all := 0
	for st, n := range counts {
		if st != "archived" {
			all += n
		}
	}
	counts["all"] = all

	cond := base
	switch {
	case f.Stage == "" || f.Stage == "all":
		cond += " AND stage <> 'archived'"
	default:
		cond += " AND stage = " + arg(f.Stage)
	}
	order := "updated_at DESC, id"
	switch f.Sort {
	case "created":
		order = "created_at DESC, id"
	case "title":
		order = "title ASC, id"
	case "slot":
		order = "active_slot_at ASC NULLS LAST, updated_at DESC, id"
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM content_overview WHERE `+cond, args...).Scan(&total); err != nil {
		return Page{}, err
	}
	q := `SELECT id, title, category, tags, source, is_demo, stage, left(raw_text, 160), part_count, image_mode, review_state,
		publishable, COALESCE(validation->'problems', '[]'::jsonb), generation_state, active_slot_at, reservation_state, wait_reason,
		last_run_state, last_published_at, revision_number, created_at, updated_at
		FROM content_overview WHERE ` + cond + ` ORDER BY ` + order + fmt.Sprintf(" LIMIT %d OFFSET %d", f.Limit, f.Offset)
	rows, err = s.pool.Query(ctx, q, args...)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()
	page := Page{Items: []Item{}, Total: total, Counts: counts, Limit: f.Limit, Offset: f.Offset}
	for rows.Next() {
		var it Item
		var problems []string
		if err := rows.Scan(&it.ID, &it.Title, &it.Category, &it.Tags, &it.Source, &it.IsDemo, &it.Stage, &it.Preview, &it.PartCount,
			&it.ImageMode, &it.ReviewState, &it.Publishable, &problems, &it.GenerationState, &it.ReservationAt, &it.ReservationState,
			&it.WaitReason, &it.LastRunState, &it.LastPublishedAt, &it.RevisionNumber, &it.CreatedAt, &it.UpdatedAt); err != nil {
			return Page{}, err
		}
		it.Problems = problems
		if it.Problems == nil {
			it.Problems = []string{}
		}
		page.Items = append(page.Items, it)
	}
	return page, rows.Err()
}

// Categories는 쓰인 카테고리 목록이다 (필터·자동 완성).
func (s *Service) Categories(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT category FROM contents WHERE category <> '' ORDER BY category`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}
