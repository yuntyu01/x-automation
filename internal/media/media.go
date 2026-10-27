// Package media는 이미지 원본·썸네일 저장(R2)과 메타데이터·참조·보관 상태를 담당한다.
//
//   - 파일은 저장소(R2 또는 로컬 대역), 객체 키·크기·형식·해시·참조·게시 시각은 PostgreSQL
//   - 객체 키는 서버가 만든다 (사용자 파일명은 쓰지 않는다)
//   - 업로드 순서: DB에 pending 행 → 저장소에 쓰기 → active. 중간에 실패하면 pending 행·고아 객체가 남고 정리 작업이 찾는다
package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yuntyu01/x-automation/internal/clock"
	"github.com/yuntyu01/x-automation/internal/content"
	"github.com/yuntyu01/x-automation/internal/integration/storage"
	"github.com/yuntyu01/x-automation/internal/store"
)

var (
	ErrNotFound = errors.New("이미지를 찾을 수 없습니다")
	ErrInUse    = errors.New("글에서 쓰고 있는 이미지는 지울 수 없어요. 먼저 글에서 빼 주세요")
)

type Asset struct {
	ID                string     `json:"id"`
	ParentID          *string    `json:"parent_id"`
	Source            string     `json:"source"`
	ContentType       string     `json:"content_type"`
	Bytes             int64      `json:"bytes"`
	Width             int        `json:"width"`
	Height            int        `json:"height"`
	SHA256            string     `json:"sha256"`
	AltText           string     `json:"alt_text"`
	Prompt            *string    `json:"prompt"`
	Model             *string    `json:"model"`
	State             string     `json:"state"`
	LastPublishedAt   *time.Time `json:"last_published_at"`
	RetentionAnchorAt *time.Time `json:"retention_anchor_at"`
	DeletedAt         *time.Time `json:"deleted_at"`
	DeleteError       *string    `json:"delete_error"`
	CreatedAt         time.Time  `json:"created_at"`
	URL               string     `json:"url"`
	ThumbURL          string     `json:"thumb_url"`
	UsageCount        int        `json:"usage_count"`
	CleanupAfter      *time.Time `json:"cleanup_after"` // 보관 기준 + 보관 일수 (그 뒤 정리 실행 때 삭제)

	storageKey string
	thumbKey   *string
}

type Service struct {
	pool          *pgxpool.Pool
	store         storage.Store
	clock         clock.Clock
	sem           chan struct{}
	retentionDays func(ctx context.Context) int
}

func NewService(pool *pgxpool.Pool, st storage.Store, c clock.Clock, retentionDays func(context.Context) int) *Service {
	if c == nil {
		c = clock.System{}
	}
	if retentionDays == nil {
		retentionDays = func(context.Context) int { return 14 }
	}
	// 이미지 디코딩은 메모리를 많이 쓰므로 한 번에 하나씩 (KoDeploy 256Mi)
	return &Service{pool: pool, store: st, clock: c, sem: make(chan struct{}, 1), retentionDays: retentionDays}
}

func (s *Service) Store() storage.Store { return s.store }

func (s *Service) urls(a *Asset) {
	a.URL = s.store.PublicURL(a.storageKey)
	if a.thumbKey != nil {
		a.ThumbURL = s.store.PublicURL(*a.thumbKey)
	}
	// 공개 주소가 없으면 로그인한 화면에서만 보이는 프록시 경로를 쓴다
	if a.URL == "" {
		a.URL = "/api/media/" + a.ID + "/file"
		a.ThumbURL = "/api/media/" + a.ID + "/file?thumb=1"
	}
	if a.ThumbURL == "" {
		a.ThumbURL = a.URL
	}
}

const assetCols = `m.id, m.parent_id, m.source, m.content_type, m.bytes, m.width, m.height, m.sha256, m.alt_text, m.prompt, m.model, m.state,
	m.last_published_at, m.retention_anchor_at, m.deleted_at, m.delete_error, m.created_at, m.storage_key, m.thumb_key,
	(SELECT count(DISTINCT pm.revision_id) FROM part_media pm WHERE pm.media_id = m.id)`

func (s *Service) scan(ctx context.Context, row pgx.Row) (Asset, error) {
	var a Asset
	err := row.Scan(&a.ID, &a.ParentID, &a.Source, &a.ContentType, &a.Bytes, &a.Width, &a.Height, &a.SHA256, &a.AltText, &a.Prompt, &a.Model,
		&a.State, &a.LastPublishedAt, &a.RetentionAnchorAt, &a.DeletedAt, &a.DeleteError, &a.CreatedAt, &a.storageKey, &a.thumbKey, &a.UsageCount)
	if err != nil {
		return a, err
	}
	s.urls(&a)
	if a.RetentionAnchorAt != nil && a.State == "active" {
		t := a.RetentionAnchorAt.AddDate(0, 0, s.retentionDays(ctx))
		a.CleanupAfter = &t
	}
	return a, nil
}

func (s *Service) Get(ctx context.Context, id string) (Asset, error) {
	a, err := s.scan(ctx, s.pool.QueryRow(ctx, `SELECT `+assetCols+` FROM media_assets m WHERE m.id = $1`, id))
	if store.IsNoRows(err) {
		return a, ErrNotFound
	}
	return a, err
}

type SaveInput struct {
	Data     []byte
	Source   string // upload | ai | crop
	AltText  string
	Prompt   *string
	Model    *string
	ParentID *string
}

// Save는 이미지를 검사·정리해 저장한다. 같은 내용(해시)의 활성 이미지가 있으면 그것을 재사용한다.
func (s *Service) Save(ctx context.Context, in SaveInput) (Asset, error) {
	select {
	case s.sem <- struct{}{}:
	case <-ctx.Done():
		return Asset{}, ctx.Err()
	}
	p, err := Process(in.Data)
	<-s.sem
	if err != nil {
		return Asset{}, err
	}
	sum := sha256.Sum256(p.Data)
	hash := hex.EncodeToString(sum[:])
	if in.Source != "crop" {
		var existing string
		err := s.pool.QueryRow(ctx, `SELECT id FROM media_assets WHERE sha256 = $1 AND state = 'active' AND parent_id IS NULL ORDER BY created_at LIMIT 1`, hash).Scan(&existing)
		if err == nil {
			return s.Get(ctx, existing)
		}
		if !store.IsNoRows(err) {
			return Asset{}, err
		}
	}
	now := s.clock.Now().UTC()
	var id string
	alt := strings.TrimSpace(in.AltText)
	if len([]rune(alt)) > 1000 {
		return Asset{}, &ImageError{"대체 텍스트는 1,000자 이하로 써 주세요"}
	}
	// 키는 서버가 만든다: 날짜/무작위 ID
	if err := s.pool.QueryRow(ctx, `INSERT INTO media_assets (storage_key, thumb_key, parent_id, source, content_type, bytes, width, height, sha256,
		alt_text, prompt, model, state) VALUES ('pending:' || gen_random_uuid(), NULL, $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'pending') RETURNING id`,
		in.ParentID, in.Source, p.ContentType, len(p.Data), p.Width, p.Height, hash, alt, in.Prompt, in.Model).Scan(&id); err != nil {
		return Asset{}, err
	}
	key := fmt.Sprintf("media/%s/%s.%s", now.Format("2006/01"), id, p.Ext)
	thumb := fmt.Sprintf("thumb/%s/%s.jpg", now.Format("2006/01"), id)
	if err := s.store.Put(ctx, key, p.ContentType, p.Data); err != nil {
		_, _ = s.pool.Exec(context.WithoutCancel(ctx), `DELETE FROM media_assets WHERE id = $1 AND state = 'pending'`, id)
		return Asset{}, err
	}
	if err := s.store.Put(ctx, thumb, "image/jpeg", p.Thumb); err != nil {
		_ = s.store.Delete(context.WithoutCancel(ctx), key)
		_, _ = s.pool.Exec(context.WithoutCancel(ctx), `DELETE FROM media_assets WHERE id = $1 AND state = 'pending'`, id)
		return Asset{}, err
	}
	if _, err := s.pool.Exec(ctx, `UPDATE media_assets SET storage_key = $2, thumb_key = $3, state = 'active' WHERE id = $1`, id, key, thumb); err != nil {
		// 저장소에는 올라갔지만 DB 갱신 실패 → 고아 객체로 남는다. 정리 작업이 접두사 안에서 찾아 처리한다
		return Asset{}, err
	}
	return s.Get(ctx, id)
}

// Crop은 원본에서 영역을 잘라 새 이미지를 만든다 (원본은 그대로).
func (s *Service) Crop(ctx context.Context, id string, r image.Rectangle) (Asset, error) {
	src, err := s.Get(ctx, id)
	if err != nil {
		return Asset{}, err
	}
	if src.State != "active" {
		return Asset{}, &ImageError{"정리된 이미지는 자를 수 없어요"}
	}
	data, _, err := s.store.Get(ctx, src.storageKey)
	if err != nil {
		return Asset{}, err
	}
	select {
	case s.sem <- struct{}{}:
	case <-ctx.Done():
		return Asset{}, ctx.Err()
	}
	p, err := Crop(data, r)
	<-s.sem
	if err != nil {
		return Asset{}, err
	}
	// Save가 다시 검사하므로 처리된 바이트를 넘긴다
	return s.Save(ctx, SaveInput{Data: p.Data, Source: "crop", AltText: src.AltText, ParentID: &src.ID})
}

func (s *Service) UpdateAlt(ctx context.Context, id, alt string) (Asset, error) {
	alt = strings.TrimSpace(alt)
	if len([]rune(alt)) > 1000 {
		return Asset{}, &ImageError{"대체 텍스트는 1,000자 이하로 써 주세요"}
	}
	tag, err := s.pool.Exec(ctx, `UPDATE media_assets SET alt_text = $2 WHERE id = $1`, id, alt)
	if err != nil {
		return Asset{}, err
	}
	if tag.RowsAffected() == 0 {
		return Asset{}, ErrNotFound
	}
	return s.Get(ctx, id)
}

// File은 원본 또는 썸네일 바이트를 읽는다 (다운로드·X 업로드·공개 주소가 없을 때 미리보기).
func (s *Service) File(ctx context.Context, id string, thumb bool) ([]byte, string, error) {
	a, err := s.Get(ctx, id)
	if err != nil {
		return nil, "", err
	}
	if a.State == "deleted" {
		return nil, "", storage.ErrNotFound
	}
	key := a.storageKey
	if thumb && a.thumbKey != nil {
		key = *a.thumbKey
	}
	return s.store.Get(ctx, key)
}

// Delete는 어떤 글에서도 쓰지 않는 이미지를 지운다. 메타데이터는 deleted로 남긴다.
func (s *Service) Delete(ctx context.Context, id string) error {
	var key string
	var thumb *string
	err := store.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var state string
		if err := tx.QueryRow(ctx, `SELECT state, storage_key, thumb_key FROM media_assets WHERE id = $1 FOR UPDATE`, id).Scan(&state, &key, &thumb); err != nil {
			if store.IsNoRows(err) {
				return ErrNotFound
			}
			return err
		}
		var used bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM part_media WHERE media_id = $1)`, id).Scan(&used); err != nil {
			return err
		}
		if used {
			return ErrInUse
		}
		_, err := tx.Exec(ctx, `UPDATE media_assets SET state = 'deleting' WHERE id = $1`, id)
		return err
	})
	if err != nil {
		return err
	}
	return s.finishDelete(ctx, id, key, thumb)
}

func (s *Service) finishDelete(ctx context.Context, id, key string, thumb *string) error {
	err := s.store.Delete(ctx, key)
	if err == nil && thumb != nil {
		err = s.store.Delete(ctx, *thumb)
	}
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		_, _ = s.pool.Exec(context.WithoutCancel(ctx), `UPDATE media_assets SET state = 'delete_failed', delete_attempts = delete_attempts + 1,
			delete_error = $2 WHERE id = $1`, id, err.Error())
		return err
	}
	_, err = s.pool.Exec(ctx, `UPDATE media_assets SET state = 'deleted', deleted_at = $2, delete_error = NULL WHERE id = $1`, id, s.clock.Now())
	return err
}

type ListFilter struct {
	Usage  string // all | used | unused | cleaned
	Query  string
	Limit  int
	Offset int
}

type Page struct {
	Items []Asset `json:"items"`
	Total int     `json:"total"`
}

func (s *Service) List(ctx context.Context, f ListFilter) (Page, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 30
	}
	cond := "m.state IN ('active', 'delete_failed', 'deleting')"
	switch f.Usage {
	case "used":
		cond += " AND EXISTS (SELECT 1 FROM part_media pm WHERE pm.media_id = m.id)"
	case "unused":
		cond += " AND NOT EXISTS (SELECT 1 FROM part_media pm WHERE pm.media_id = m.id)"
	case "cleaned":
		cond = "m.state = 'deleted'"
	}
	args := []any{}
	if q := strings.TrimSpace(f.Query); q != "" {
		args = append(args, "%"+strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)+"%")
		cond += " AND (m.alt_text ILIKE $1 OR COALESCE(m.prompt, '') ILIKE $1)"
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM media_assets m WHERE `+cond, args...).Scan(&total); err != nil {
		return Page{}, err
	}
	rows, err := s.pool.Query(ctx, `SELECT `+assetCols+` FROM media_assets m WHERE `+cond+
		fmt.Sprintf(` ORDER BY m.created_at DESC LIMIT %d OFFSET %d`, f.Limit, max(f.Offset, 0)), args...)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()
	page := Page{Items: []Asset{}, Total: total}
	for rows.Next() {
		a, err := s.scan(ctx, rows)
		if err != nil {
			return Page{}, err
		}
		page.Items = append(page.Items, a)
	}
	return page, rows.Err()
}

// ContentLoader는 content 패키지가 revision의 이미지와 공개 주소를 채울 때 쓴다.
func (s *Service) ContentLoader() content.MediaLoader {
	return func(ctx context.Context, db store.DB, revisionID string) ([]content.PartMedia, error) {
		rows, err := db.Query(ctx, `SELECT pm.part_index, pm.position, pm.media_id, m.alt_text, m.state, m.storage_key, m.thumb_key
			FROM part_media pm JOIN media_assets m ON m.id = pm.media_id WHERE pm.revision_id = $1 ORDER BY pm.part_index, pm.position`, revisionID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []content.PartMedia{}
		for rows.Next() {
			var pm content.PartMedia
			var a Asset
			if err := rows.Scan(&pm.PartIndex, &pm.Position, &pm.MediaID, &pm.AltText, &pm.State, &a.storageKey, &a.thumbKey); err != nil {
				return nil, err
			}
			a.ID = pm.MediaID
			s.urls(&a)
			pm.URL, pm.ThumbURL = a.URL, a.ThumbURL
			if pm.State == "deleted" {
				pm.URL, pm.ThumbURL = "", ""
			}
			out = append(out, pm)
		}
		return out, rows.Err()
	}
}

// Orphan은 저장소 접두사 안에 있지만 DB에 연결되지 않은 객체다.
type Orphan struct {
	Key          string    `json:"key"`
	Size         int64     `json:"size"`
	LastModified time.Time `json:"last_modified"`
}

// FindOrphans는 유예 기간이 지난 고아 객체를 찾는다. 버킷 전체가 아니라 앱 전용 접두사 안만 본다.
func (s *Service) FindOrphans(ctx context.Context, grace time.Duration, limit int) ([]Orphan, error) {
	cutoff := s.clock.Now().Add(-grace)
	var out []Orphan
	after := ""
	for pages := 0; pages < 50; pages++ {
		objs, next, err := s.store.List(ctx, after, 500)
		if err != nil {
			return nil, err
		}
		var keys []string
		for _, o := range objs {
			keys = append(keys, o.Key)
		}
		known := map[string]bool{}
		if len(keys) > 0 {
			rows, err := s.pool.Query(ctx, `SELECT storage_key FROM media_assets WHERE storage_key = ANY($1)
				UNION SELECT thumb_key FROM media_assets WHERE thumb_key = ANY($1)`, keys)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var k string
				if err := rows.Scan(&k); err == nil {
					known[k] = true
				}
			}
			rows.Close()
		}
		for _, o := range objs {
			if !known[o.Key] && o.LastModified.Before(cutoff) {
				out = append(out, Orphan{Key: o.Key, Size: o.Size, LastModified: o.LastModified})
				if len(out) >= limit {
					return out, nil
				}
			}
		}
		if next == "" {
			break
		}
		after = next
	}
	return out, nil
}
