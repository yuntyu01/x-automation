// Package content는 콘텐츠 묶음·revision·자동 저장·승인·보관함 조회를 담당한다.
//
// 규칙
//   - 원문(raw_text)은 revision에 그대로 보존하고, 게시 부분(thread_parts)은 분할 규칙으로 다시 만든다
//   - 승인되었거나 게시에 쓰인(frozen) revision은 바꾸지 않는다. 그 뒤 편집은 새 revision이 된다
//   - 승인은 revision 단위다. 새 revision은 승인되지 않은 상태로 시작한다 (미승인 변경분 자동 게시 금지)
//   - 자동 저장은 edit_version으로 충돌을 감지한다
package content

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yuntyu01/x-automation/internal/clock"
	"github.com/yuntyu01/x-automation/internal/store"
	"github.com/yuntyu01/x-automation/internal/thread"
)

var (
	ErrNotFound     = errors.New("콘텐츠를 찾을 수 없습니다")
	ErrConflict     = errors.New("다른 곳에서 먼저 변경되었습니다")
	ErrNotDeletable = errors.New("예약되었거나 게시한 기록이 있는 콘텐츠는 삭제할 수 없습니다. 보관을 사용하세요")
)

// ValidationError는 필드별 오류다.
type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string { return "입력값을 확인해 주세요" }

// StateError는 현재 상태에서 할 수 없는 작업이다 (사용자에게 그대로 보여 줄 문장).
type StateError struct{ Message string }

func (e *StateError) Error() string { return e.Message }

type SplitOptions struct {
	Numbering  bool `json:"numbering"`
	MaxReplies int  `json:"max_replies"`
}

type Content struct {
	ID                 string     `json:"id"`
	Title              string     `json:"title"`
	Category           string     `json:"category"`
	Tags               []string   `json:"tags"`
	Source             string     `json:"source"`
	IsDemo             bool       `json:"is_demo"`
	CurrentRevisionID  string     `json:"current_revision_id"`
	ApprovedRevisionID *string    `json:"approved_revision_id"`
	GenerationState    string     `json:"generation_state"`
	GenerationError    *string    `json:"generation_error"`
	ArchivedAt         *time.Time `json:"archived_at"`
	EditVersion        int        `json:"edit_version"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

type Revision struct {
	ID            string          `json:"id"`
	ContentID     string          `json:"content_id"`
	Number        int             `json:"number"`
	RawText       string          `json:"raw_text"`
	SplitMode     thread.Mode     `json:"split_mode"`
	SplitOptions  SplitOptions    `json:"split_options"`
	ImageMode     string          `json:"image_mode"`
	Origin        string          `json:"origin"`
	PromptID      *string         `json:"prompt_id"`
	PromptVersion *int            `json:"prompt_version"`
	Model         *string         `json:"model"`
	GenOptions    json.RawMessage `json:"gen_options"`
	ReviewState   string          `json:"review_state"`
	ApprovedAt    *time.Time      `json:"approved_at"`
	FrozenAt      *time.Time      `json:"frozen_at"`
	Validation    Validation      `json:"validation"`
	Publishable   bool            `json:"publishable"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	Parts         []thread.Part   `json:"parts"`
	Media         []PartMedia     `json:"media"`
}

// Validation은 revision의 게시 가능 판정 요약이다.
type Validation struct {
	Note             string   `json:"note"`
	Problems         []string `json:"problems"`
	Replies          int      `json:"replies"`
	RequestedReplies int      `json:"requested_replies"`
	PostCount        int      `json:"post_count"`
	HasURL           bool     `json:"has_url"`
	Limit            int      `json:"limit"`
}

// PartMedia는 게시 부분에 붙인 이미지다. 06단계 media 패키지가 채운다.
type PartMedia struct {
	PartIndex int    `json:"part_index"`
	Position  int    `json:"position"`
	MediaID   string `json:"media_id"`
	URL       string `json:"url,omitempty"`
	ThumbURL  string `json:"thumb_url,omitempty"`
	AltText   string `json:"alt_text"`
	State     string `json:"state"`
}

// Detail은 편집 화면 한 장이다.
type Detail struct {
	Content
	Stage            string     `json:"stage"`
	Revision         Revision   `json:"revision"`
	ApprovedNumber   *int       `json:"approved_number"` // 승인된 revision 번호 (현재와 다를 수 있음)
	RevisionCount    int        `json:"revision_count"`
	ReservationID    *string    `json:"reservation_id"`
	ReservationAt    *time.Time `json:"reservation_at"`
	ReservationRevID *string    `json:"reservation_revision_id"`
	LastRunState     *string    `json:"last_run_state"`
}

// ThreadSettings는 분할에 쓰는 앱 전역 값이다 (장문 게시 여부).
type ThreadSettings func(ctx context.Context) (longPost bool)

// ImageCounter는 revision에 붙은 이미지 수를 센다 (06단계 media 패키지).
type MediaLoader func(ctx context.Context, db store.DB, revisionID string) ([]PartMedia, error)

type Service struct {
	pool      *pgxpool.Pool
	clock     clock.Clock
	longPost  ThreadSettings
	loadMedia MediaLoader
}

func NewService(pool *pgxpool.Pool, c clock.Clock, longPost ThreadSettings) *Service {
	if c == nil {
		c = clock.System{}
	}
	if longPost == nil {
		longPost = func(context.Context) bool { return false }
	}
	return &Service{pool: pool, clock: c, longPost: longPost, loadMedia: defaultMediaLoader}
}

// SetMediaLoader는 이미지 URL을 채우는 로더를 연결한다.
func (s *Service) SetMediaLoader(l MediaLoader) { s.loadMedia = l }

func defaultMediaLoader(ctx context.Context, db store.DB, revisionID string) ([]PartMedia, error) {
	rows, err := db.Query(ctx, `SELECT pm.part_index, pm.position, pm.media_id, m.alt_text, m.state
		FROM part_media pm JOIN media_assets m ON m.id = pm.media_id WHERE pm.revision_id = $1
		ORDER BY pm.part_index, pm.position`, revisionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PartMedia{}
	for rows.Next() {
		var m PartMedia
		if err := rows.Scan(&m.PartIndex, &m.Position, &m.MediaID, &m.AltText, &m.State); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Draft는 자동 저장 요청이다.
type Draft struct {
	EditVersion  int          `json:"edit_version"`
	Title        string       `json:"title"`
	Category     string       `json:"category"`
	Tags         []string     `json:"tags"`
	RawText      string       `json:"raw_text"`
	SplitMode    thread.Mode  `json:"split_mode"`
	SplitOptions SplitOptions `json:"split_options"`
	ImageMode    string       `json:"image_mode"`
}

// NewContent는 등록 요청이다.
type NewContent struct {
	Title        string          `json:"title"`
	Category     string          `json:"category"`
	Tags         []string        `json:"tags"`
	RawText      string          `json:"raw_text"`
	SplitMode    thread.Mode     `json:"split_mode"`
	SplitOptions SplitOptions    `json:"split_options"`
	ImageMode    string          `json:"image_mode"`
	Source       string          `json:"-"`
	Origin       string          `json:"-"`
	IsDemo       bool            `json:"-"`
	PromptID     *string         `json:"-"`
	PromptVer    *int            `json:"-"`
	Model        *string         `json:"-"`
	GenOptions   json.RawMessage `json:"-"`
	ReviewState  string          `json:"-"`
	Approve      bool            `json:"approve"` // 저장하면서 바로 승인
}

const (
	maxTitle    = 120
	maxCategory = 40
	maxTags     = 10
	maxTagLen   = 30
	maxRaw      = 60000
)

func cleanTags(tags []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, t := range tags {
		t = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(t), "#"))
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

func validateFields(title, category string, tags []string, raw string, mode thread.Mode, imageMode string, so SplitOptions) map[string]string {
	f := map[string]string{}
	if len([]rune(title)) > maxTitle {
		f["title"] = fmt.Sprintf("제목은 %d자 이하로 써 주세요", maxTitle)
	}
	if len([]rune(category)) > maxCategory {
		f["category"] = fmt.Sprintf("카테고리는 %d자 이하로 써 주세요", maxCategory)
	}
	if len(tags) > maxTags {
		f["tags"] = fmt.Sprintf("태그는 %d개까지예요", maxTags)
	}
	for _, t := range tags {
		if len([]rune(t)) > maxTagLen {
			f["tags"] = fmt.Sprintf("태그 하나는 %d자 이하로 써 주세요", maxTagLen)
		}
	}
	if len([]rune(raw)) > maxRaw {
		f["raw_text"] = fmt.Sprintf("원문은 %d자 이하로 써 주세요", maxRaw)
	}
	if !mode.Valid() {
		f["split_mode"] = "분할 방식을 선택하세요"
	}
	switch imageMode {
	case "none", "upload", "ai":
	default:
		f["image_mode"] = "이미지 방식을 선택하세요"
	}
	if so.MaxReplies < 0 || so.MaxReplies > 25 {
		f["split_options.max_replies"] = "최대 답글 수는 0(제한 없음)~25입니다"
	}
	return f
}

// evaluate는 분할하고 게시 가능 여부를 판정한다. 이미지 방식이 있으면 이미지가 붙어 있어야 한다.
func (s *Service) evaluate(ctx context.Context, raw string, mode thread.Mode, so SplitOptions, imageMode string, media []PartMedia) (thread.Result, Validation) {
	limit := thread.V3.MaxWeighted
	if s.longPost(ctx) {
		limit = thread.LongPostLimit
	}
	r := thread.Split(raw, thread.Options{Mode: mode, Numbering: so.Numbering, MaxReplies: so.MaxReplies, Limit: limit})
	v := Validation{Note: r.Note, Problems: append([]string{}, r.Problems...), Replies: r.Replies, RequestedReplies: r.RequestedReplies,
		PostCount: r.PostCount, HasURL: r.HasURL, Limit: limit}
	if imageMode != "none" {
		usable := 0
		for _, m := range media {
			if m.PartIndex >= len(r.Parts) {
				v.Problems = append(v.Problems, "이미지가 붙어 있던 부분이 분할 변경으로 사라졌어요. 이미지 위치를 다시 정해 주세요")
				r.Publishable = false
				continue
			}
			if m.State == "active" {
				usable++
			}
		}
		if usable == 0 {
			v.Problems = append(v.Problems, "이미지 방식이 선택되어 있지만 붙은 이미지가 없어요")
			r.Publishable = false
		}
	}
	perPart := map[int]int{}
	for _, m := range media {
		perPart[m.PartIndex]++
	}
	for idx, n := range perPart {
		if n > 4 {
			v.Problems = append(v.Problems, fmt.Sprintf("%d번째 부분의 이미지가 4장을 넘어요", idx+1))
			r.Publishable = false
		}
	}
	return r, v
}

func (s *Service) writeParts(ctx context.Context, tx pgx.Tx, revID string, r thread.Result, v Validation) error {
	if _, err := tx.Exec(ctx, `DELETE FROM thread_parts WHERE revision_id = $1`, revID); err != nil {
		return err
	}
	for _, p := range r.Parts {
		if _, err := tx.Exec(ctx, `INSERT INTO thread_parts (revision_id, position, text, weighted_length, valid) VALUES ($1, $2, $3, $4, $5)`,
			revID, p.Index, p.Text, p.Weighted, p.Valid); err != nil {
			return err
		}
	}
	vj, _ := json.Marshal(v)
	_, err := tx.Exec(ctx, `UPDATE content_revisions SET validation = $2, publishable = $3, updated_at = now() WHERE id = $1`, revID, vj, r.Publishable)
	return err
}

// Create는 콘텐츠와 첫 revision을 만든다.
func (s *Service) Create(ctx context.Context, n NewContent) (Detail, error) {
	n.Tags = cleanTags(n.Tags)
	n.Title, n.Category = strings.TrimSpace(n.Title), strings.TrimSpace(n.Category)
	if n.SplitMode == "" {
		n.SplitMode = thread.ModeTwoParts
	}
	if n.ImageMode == "" {
		n.ImageMode = "none"
	}
	if n.Source == "" {
		n.Source = "manual"
	}
	if n.Origin == "" {
		n.Origin = "user"
	}
	if n.ReviewState == "" {
		n.ReviewState = "unreviewed"
	}
	if f := validateFields(n.Title, n.Category, n.Tags, n.RawText, n.SplitMode, n.ImageMode, n.SplitOptions); len(f) > 0 {
		return Detail{}, &ValidationError{Fields: f}
	}
	if strings.TrimSpace(n.RawText) == "" {
		return Detail{}, &ValidationError{Fields: map[string]string{"raw_text": "내용을 입력하세요"}}
	}
	if n.Title == "" {
		n.Title = autoTitle(n.RawText)
	}
	var id string
	err := store.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		id, err = s.createTx(ctx, tx, n)
		return err
	})
	if err != nil {
		return Detail{}, err
	}
	if n.Approve {
		d, err := s.Get(ctx, id)
		if err != nil {
			return Detail{}, err
		}
		if d.Revision.Publishable {
			return s.Approve(ctx, id, d.Revision.ID)
		}
	}
	return s.Get(ctx, id)
}

func (s *Service) createTx(ctx context.Context, tx pgx.Tx, n NewContent) (string, error) {
	var id, revID string
	if err := tx.QueryRow(ctx, `INSERT INTO contents (title, category, tags, source, is_demo) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		n.Title, n.Category, n.Tags, n.Source, n.IsDemo).Scan(&id); err != nil {
		return "", err
	}
	so, _ := json.Marshal(n.SplitOptions)
	gen := n.GenOptions
	if len(gen) == 0 {
		gen = json.RawMessage(`{}`)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO content_revisions (content_id, number, raw_text, split_mode, split_options, image_mode, origin,
		prompt_id, prompt_version, model, gen_options, review_state) VALUES ($1, 1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) RETURNING id`,
		id, n.RawText, string(n.SplitMode), so, n.ImageMode, n.Origin, n.PromptID, n.PromptVer, n.Model, gen, n.ReviewState).Scan(&revID); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `UPDATE contents SET current_revision_id = $2 WHERE id = $1`, id, revID); err != nil {
		return "", err
	}
	r, v := s.evaluate(ctx, n.RawText, n.SplitMode, n.SplitOptions, n.ImageMode, nil)
	return id, s.writeParts(ctx, tx, revID, r, v)
}

// CreateTx는 다른 작업(가져오기·생성)과 같은 트랜잭션에서 만든다.
func (s *Service) CreateTx(ctx context.Context, tx pgx.Tx, n NewContent) (string, error) {
	n.Tags = cleanTags(n.Tags)
	if n.SplitMode == "" {
		n.SplitMode = thread.ModeTwoParts
	}
	if n.ImageMode == "" {
		n.ImageMode = "none"
	}
	if n.Source == "" {
		n.Source = "manual"
	}
	if n.Origin == "" {
		n.Origin = "user"
	}
	if n.ReviewState == "" {
		n.ReviewState = "unreviewed"
	}
	if n.Title == "" {
		n.Title = autoTitle(n.RawText)
	}
	return s.createTx(ctx, tx, n)
}

// autoTitle은 제목이 비었을 때 원문 첫 줄로 관리용 제목을 만든다.
func autoTitle(raw string) string {
	line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(thread.NormalizeForPost(raw)), "\n", 2)[0])
	r := []rune(line)
	if len(r) > 30 {
		return string(r[:30]) + "…"
	}
	if line == "" {
		return "제목 없음"
	}
	return line
}

// SaveDraft는 자동 저장이다. 현재 revision이 고정(승인·게시)되어 있으면 새 revision을 만든다.
func (s *Service) SaveDraft(ctx context.Context, id string, d Draft) (Detail, error) {
	d.Tags = cleanTags(d.Tags)
	d.Title, d.Category = strings.TrimSpace(d.Title), strings.TrimSpace(d.Category)
	if f := validateFields(d.Title, d.Category, d.Tags, d.RawText, d.SplitMode, d.ImageMode, d.SplitOptions); len(f) > 0 {
		return Detail{}, &ValidationError{Fields: f}
	}
	err := store.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var ver int
		var revID string
		var frozen *time.Time
		var number int
		err := tx.QueryRow(ctx, `SELECT c.edit_version, r.id, r.frozen_at, r.number FROM contents c
			JOIN content_revisions r ON r.id = c.current_revision_id WHERE c.id = $1 FOR UPDATE OF c`, id).Scan(&ver, &revID, &frozen, &number)
		if store.IsNoRows(err) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if ver != d.EditVersion {
			return ErrConflict
		}
		title := d.Title
		if title == "" {
			title = autoTitle(d.RawText)
		}
		if _, err := tx.Exec(ctx, `UPDATE contents SET title = $2, category = $3, tags = $4, edit_version = edit_version + 1, updated_at = now() WHERE id = $1`,
			id, title, d.Category, d.Tags); err != nil {
			return err
		}
		so, _ := json.Marshal(d.SplitOptions)
		if frozen != nil {
			// 고정된 revision은 그대로 두고 새 revision을 만든다. 승인 상태는 이어받지 않는다
			var newID string
			if err := tx.QueryRow(ctx, `INSERT INTO content_revisions (content_id, number, raw_text, split_mode, split_options, image_mode,
				origin, prompt_id, prompt_version, model, gen_options, review_state)
				SELECT content_id, (SELECT max(number) + 1 FROM content_revisions WHERE content_id = $1), $3, $4, $5, $6, 'user',
				       prompt_id, prompt_version, model, gen_options, 'unreviewed'
				FROM content_revisions WHERE id = $2 RETURNING id`, id, revID, d.RawText, string(d.SplitMode), so, d.ImageMode).Scan(&newID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO part_media (revision_id, part_index, media_id, position)
				SELECT $2, part_index, media_id, position FROM part_media WHERE revision_id = $1`, revID, newID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE contents SET current_revision_id = $2 WHERE id = $1`, id, newID); err != nil {
				return err
			}
			revID = newID
		} else {
			// 미고정 revision 수정: 반려 상태였다면 다시 검토 전 상태로
			if _, err := tx.Exec(ctx, `UPDATE content_revisions SET raw_text = $2, split_mode = $3, split_options = $4, image_mode = $5,
				review_state = CASE WHEN review_state = 'rejected' THEN 'unreviewed' ELSE review_state END, updated_at = now() WHERE id = $1`,
				revID, d.RawText, string(d.SplitMode), so, d.ImageMode); err != nil {
				return err
			}
		}
		media, err := s.loadMedia(ctx, tx, revID)
		if err != nil {
			return err
		}
		r, v := s.evaluate(ctx, d.RawText, d.SplitMode, d.SplitOptions, d.ImageMode, media)
		return s.writeParts(ctx, tx, revID, r, v)
	})
	if err != nil {
		return Detail{}, err
	}
	return s.Get(ctx, id)
}

// Revalidate는 이미지 연결 변경처럼 원문 밖의 변화가 있을 때 현재 revision을 다시 판정한다.
func (s *Service) Revalidate(ctx context.Context, db store.DB, revisionID string) error {
	var raw, mode, imageMode string
	var soRaw []byte
	if err := db.QueryRow(ctx, `SELECT raw_text, split_mode, split_options, image_mode FROM content_revisions WHERE id = $1`, revisionID).
		Scan(&raw, &mode, &soRaw, &imageMode); err != nil {
		return err
	}
	var so SplitOptions
	_ = json.Unmarshal(soRaw, &so)
	media, err := s.loadMedia(ctx, db, revisionID)
	if err != nil {
		return err
	}
	r, v := s.evaluate(ctx, raw, thread.Mode(mode), so, imageMode, media)
	vj, _ := json.Marshal(v)
	_, err = db.Exec(ctx, `UPDATE content_revisions SET validation = $2, publishable = $3, updated_at = now() WHERE id = $1 AND frozen_at IS NULL`,
		revisionID, vj, r.Publishable)
	return err
}

func scanRevision(row pgx.Row) (Revision, error) {
	var r Revision
	var so, val []byte
	var mode string
	err := row.Scan(&r.ID, &r.ContentID, &r.Number, &r.RawText, &mode, &so, &r.ImageMode, &r.Origin, &r.PromptID, &r.PromptVersion,
		&r.Model, &r.GenOptions, &r.ReviewState, &r.ApprovedAt, &r.FrozenAt, &val, &r.Publishable, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return r, err
	}
	r.SplitMode = thread.Mode(mode)
	_ = json.Unmarshal(so, &r.SplitOptions)
	_ = json.Unmarshal(val, &r.Validation)
	if r.Validation.Problems == nil {
		r.Validation.Problems = []string{}
	}
	return r, nil
}

const revisionCols = `id, content_id, number, raw_text, split_mode, split_options, image_mode, origin, prompt_id, prompt_version,
	model, gen_options, review_state, approved_at, frozen_at, validation, publishable, created_at, updated_at`

func (s *Service) loadRevision(ctx context.Context, db store.DB, revID string) (Revision, error) {
	r, err := scanRevision(db.QueryRow(ctx, `SELECT `+revisionCols+` FROM content_revisions WHERE id = $1`, revID))
	if err != nil {
		return r, err
	}
	rows, err := db.Query(ctx, `SELECT position, text, weighted_length, valid FROM thread_parts WHERE revision_id = $1 ORDER BY position`, revID)
	if err != nil {
		return r, err
	}
	defer rows.Close()
	r.Parts = []thread.Part{}
	for rows.Next() {
		var p thread.Part
		if err := rows.Scan(&p.Index, &p.Text, &p.Weighted, &p.Valid); err != nil {
			return r, err
		}
		if !p.Valid {
			if p.Text == "" {
				p.Problem = "빈 부분이에요"
			} else {
				p.Problem = fmt.Sprintf("%d자로 한도 %d자를 넘어요", p.Weighted, max(r.Validation.Limit, thread.V3.MaxWeighted))
			}
		}
		r.Parts = append(r.Parts, p)
	}
	if err := rows.Err(); err != nil {
		return r, err
	}
	r.Media, err = s.loadMedia(ctx, db, revID)
	return r, err
}

// Get은 편집 화면용 상세를 돌려준다.
func (s *Service) Get(ctx context.Context, id string) (Detail, error) {
	var d Detail
	err := s.pool.QueryRow(ctx, `SELECT o.id, o.title, o.category, o.tags, o.source, o.is_demo, o.current_revision_id, o.approved_revision_id,
		o.generation_state, o.generation_error, o.archived_at, o.edit_version, o.created_at, o.updated_at, o.stage,
		o.active_reservation_id, o.active_slot_at, o.last_run_state,
		(SELECT number FROM content_revisions WHERE id = o.approved_revision_id),
		(SELECT count(*) FROM content_revisions WHERE content_id = o.id),
		(SELECT revision_id FROM reservations WHERE id = o.active_reservation_id)
		FROM content_overview o WHERE o.id = $1`, id).
		Scan(&d.ID, &d.Title, &d.Category, &d.Tags, &d.Source, &d.IsDemo, &d.CurrentRevisionID, &d.ApprovedRevisionID,
			&d.GenerationState, &d.GenerationError, &d.ArchivedAt, &d.EditVersion, &d.CreatedAt, &d.UpdatedAt, &d.Stage,
			&d.ReservationID, &d.ReservationAt, &d.LastRunState, &d.ApprovedNumber, &d.RevisionCount, &d.ReservationRevID)
	if store.IsNoRows(err) {
		return d, ErrNotFound
	}
	if err != nil {
		return d, err
	}
	d.Revision, err = s.loadRevision(ctx, s.pool, d.CurrentRevisionID)
	return d, err
}

// GetRevision은 특정 revision을 돌려준다 (이력 비교).
func (s *Service) GetRevision(ctx context.Context, contentID, revID string) (Revision, error) {
	r, err := s.loadRevision(ctx, s.pool, revID)
	if store.IsNoRows(err) || (err == nil && r.ContentID != contentID) {
		return r, ErrNotFound
	}
	return r, err
}

type RevisionSummary struct {
	ID          string     `json:"id"`
	Number      int        `json:"number"`
	Origin      string     `json:"origin"`
	ReviewState string     `json:"review_state"`
	ApprovedAt  *time.Time `json:"approved_at"`
	FrozenAt    *time.Time `json:"frozen_at"`
	Publishable bool       `json:"publishable"`
	Preview     string     `json:"preview"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	Published   bool       `json:"published"`
}

func (s *Service) Revisions(ctx context.Context, contentID string) ([]RevisionSummary, error) {
	rows, err := s.pool.Query(ctx, `SELECT r.id, r.number, r.origin, r.review_state, r.approved_at, r.frozen_at, r.publishable,
		left(r.raw_text, 120), r.created_at, r.updated_at,
		EXISTS (SELECT 1 FROM publish_runs pr WHERE pr.revision_id = r.id AND pr.state IN ('completed', 'partial'))
		FROM content_revisions r WHERE r.content_id = $1 ORDER BY r.number DESC`, contentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RevisionSummary{}
	for rows.Next() {
		var r RevisionSummary
		if err := rows.Scan(&r.ID, &r.Number, &r.Origin, &r.ReviewState, &r.ApprovedAt, &r.FrozenAt, &r.Publishable, &r.Preview,
			&r.CreatedAt, &r.UpdatedAt, &r.Published); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Restore는 이전 revision 내용으로 새 편집본을 만든다 (수정 되돌리기). 이전 기록은 그대로 남는다.
func (s *Service) Restore(ctx context.Context, contentID, revID string, editVersion int) (Detail, error) {
	old, err := s.GetRevision(ctx, contentID, revID)
	if err != nil {
		return Detail{}, err
	}
	cur, err := s.Get(ctx, contentID)
	if err != nil {
		return Detail{}, err
	}
	return s.SaveDraft(ctx, contentID, Draft{EditVersion: editVersion, Title: cur.Title, Category: cur.Category, Tags: cur.Tags,
		RawText: old.RawText, SplitMode: old.SplitMode, SplitOptions: old.SplitOptions, ImageMode: old.ImageMode})
}

// Approve는 revision을 승인한다. 게시 가능한 revision만 승인할 수 있고, 승인하면 고정된다.
func (s *Service) Approve(ctx context.Context, contentID, revID string) (Detail, error) {
	err := store.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var publishable bool
		var owner string
		err := tx.QueryRow(ctx, `SELECT content_id, publishable FROM content_revisions WHERE id = $1 FOR UPDATE`, revID).Scan(&owner, &publishable)
		if store.IsNoRows(err) || (err == nil && owner != contentID) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if !publishable {
			return &StateError{Message: "길이 초과·이미지 누락 같은 문제를 먼저 해결해야 승인할 수 있어요"}
		}
		if _, err := tx.Exec(ctx, `UPDATE content_revisions SET review_state = 'approved', approved_at = COALESCE(approved_at, now()),
			frozen_at = COALESCE(frozen_at, now()) WHERE id = $1`, revID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE contents SET approved_revision_id = $2, updated_at = now() WHERE id = $1`, contentID, revID); err != nil {
			return err
		}
		// 아직 실행 전인 예약은 승인한 revision으로 바꾼다
		_, err = tx.Exec(ctx, `UPDATE reservations SET revision_id = $2, version = version + 1, updated_at = now()
			WHERE content_id = $1 AND state IN ('assigned', 'waiting')`, contentID, revID)
		return err
	})
	if err != nil {
		return Detail{}, err
	}
	return s.Get(ctx, contentID)
}

// Reject는 검토에서 반려한다. 승인되어 있던 이전 revision은 그대로 둔다.
func (s *Service) Reject(ctx context.Context, contentID, revID string) (Detail, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE content_revisions SET review_state = 'rejected', updated_at = now()
		WHERE id = $1 AND content_id = $2 AND frozen_at IS NULL`, revID, contentID)
	if err != nil {
		return Detail{}, err
	}
	if tag.RowsAffected() == 0 {
		return Detail{}, &StateError{Message: "승인되었거나 게시된 버전은 반려할 수 없어요"}
	}
	return s.Get(ctx, contentID)
}

// Duplicate는 현재 revision 내용으로 새 콘텐츠를 만든다 (이미지 연결 포함, 승인·예약은 없음).
func (s *Service) Duplicate(ctx context.Context, id string) (Detail, error) {
	cur, err := s.Get(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	var newID string
	err = store.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		newID, err = s.createTx(ctx, tx, NewContent{Title: strings.TrimSpace(cur.Title + " (복제)"), Category: cur.Category, Tags: cur.Tags,
			RawText: cur.Revision.RawText, SplitMode: cur.Revision.SplitMode, SplitOptions: cur.Revision.SplitOptions,
			ImageMode: cur.Revision.ImageMode, Source: cur.Source, Origin: "user", IsDemo: cur.IsDemo, ReviewState: "unreviewed"})
		if err != nil {
			return err
		}
		var newRev string
		if err := tx.QueryRow(ctx, `SELECT current_revision_id FROM contents WHERE id = $1`, newID).Scan(&newRev); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO part_media (revision_id, part_index, media_id, position)
			SELECT $2, part_index, media_id, position FROM part_media WHERE revision_id = $1`, cur.CurrentRevisionID, newRev); err != nil {
			return err
		}
		return s.Revalidate(ctx, tx, newRev)
	})
	if err != nil {
		return Detail{}, err
	}
	return s.Get(ctx, newID)
}

// Bulk 작업 결과
type BulkResult struct {
	Updated int               `json:"updated"`
	Skipped map[string]string `json:"skipped"` // id → 이유
}

func (s *Service) Archive(ctx context.Context, ids []string, archive bool) (BulkResult, error) {
	res := BulkResult{Skipped: map[string]string{}}
	for _, id := range ids {
		if archive {
			// 진행 중인 예약이 있으면 먼저 예약을 해제해야 한다
			var active bool
			if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM reservations WHERE content_id = $1
				AND state IN ('assigned', 'waiting', 'publishing', 'partial', 'unknown'))`, id).Scan(&active); err != nil {
				return res, err
			}
			if active {
				res.Skipped[id] = "예약 중이라 보관할 수 없어요. 예약을 먼저 해제하세요"
				continue
			}
		}
		var tag interface{ RowsAffected() int64 }
		var err error
		if archive {
			tag, err = s.pool.Exec(ctx, `UPDATE contents SET archived_at = now(), updated_at = now() WHERE id = $1 AND archived_at IS NULL`, id)
		} else {
			tag, err = s.pool.Exec(ctx, `UPDATE contents SET archived_at = NULL, updated_at = now() WHERE id = $1 AND archived_at IS NOT NULL`, id)
		}
		if err != nil {
			return res, err
		}
		res.Updated += int(tag.RowsAffected())
	}
	return res, nil
}

func (s *Service) SetCategory(ctx context.Context, ids []string, category string) (BulkResult, error) {
	category = strings.TrimSpace(category)
	if len([]rune(category)) > maxCategory {
		return BulkResult{}, &ValidationError{Fields: map[string]string{"category": fmt.Sprintf("카테고리는 %d자 이하로 써 주세요", maxCategory)}}
	}
	tag, err := s.pool.Exec(ctx, `UPDATE contents SET category = $2, updated_at = now(), edit_version = edit_version + 1 WHERE id = ANY($1::uuid[])`, ids, category)
	if err != nil {
		return BulkResult{}, err
	}
	return BulkResult{Updated: int(tag.RowsAffected()), Skipped: map[string]string{}}, nil
}

// Delete는 예약·게시 기록이 한 번도 없는 콘텐츠만 지운다. 앱에서의 삭제는 X 게시물 삭제가 아니다.
func (s *Service) Delete(ctx context.Context, ids []string) (BulkResult, error) {
	res := BulkResult{Skipped: map[string]string{}}
	for _, id := range ids {
		err := store.InTx(ctx, s.pool, func(tx pgx.Tx) error {
			var used bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM publish_runs WHERE content_id = $1)
				OR EXISTS (SELECT 1 FROM reservations WHERE content_id = $1 AND state <> 'canceled')
				OR EXISTS (SELECT 1 FROM generation_requests WHERE content_id = $1)`, id).Scan(&used); err != nil {
				return err
			}
			if used {
				return ErrNotDeletable
			}
			if _, err := tx.Exec(ctx, `UPDATE reservations SET content_id = NULL, revision_id = NULL WHERE content_id = $1`, id); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE contents SET current_revision_id = NULL, approved_revision_id = NULL WHERE id = $1`, id); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM content_revisions WHERE content_id = $1`, id); err != nil {
				return err
			}
			tag, err := tx.Exec(ctx, `DELETE FROM contents WHERE id = $1`, id)
			if err == nil {
				res.Updated += int(tag.RowsAffected())
			}
			return err
		})
		if errors.Is(err, ErrNotDeletable) {
			res.Skipped[id] = ErrNotDeletable.Error()
			continue
		}
		if err != nil {
			return res, err
		}
	}
	return res, nil
}
