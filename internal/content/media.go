package content

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/yuntyu01/x-automation/internal/store"
)

// MaxImagesPerPart는 X 게시물 하나에 붙일 수 있는 사진 수다.
const MaxImagesPerPart = 4

// SetPartMedia는 한 게시 부분의 이미지 목록(순서 포함)을 바꾼다.
// 이미지를 바꾸는 것도 편집이라, 승인·게시로 고정된 revision이면 새 편집본이 생기고 승인은 이어지지 않는다.
func (s *Service) SetPartMedia(ctx context.Context, id string, editVersion, partIndex int, mediaIDs []string, imageMode string) (Detail, error) {
	if partIndex < 0 || partIndex > 25 {
		return Detail{}, &ValidationError{Fields: map[string]string{"part_index": "부분 번호가 올바르지 않습니다"}}
	}
	if len(mediaIDs) > MaxImagesPerPart {
		return Detail{}, &ValidationError{Fields: map[string]string{"media_ids": fmt.Sprintf("한 부분에 이미지는 %d장까지예요", MaxImagesPerPart)}}
	}
	seen := map[string]bool{}
	for _, m := range mediaIDs {
		if seen[m] {
			return Detail{}, &ValidationError{Fields: map[string]string{"media_ids": "같은 이미지를 한 부분에 두 번 붙일 수 없어요"}}
		}
		seen[m] = true
	}
	switch imageMode {
	case "", "none", "upload", "ai":
	default:
		return Detail{}, &ValidationError{Fields: map[string]string{"image_mode": "이미지 방식을 선택하세요"}}
	}
	err := store.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		revID, err := s.lockForEdit(ctx, tx, id, editVersion)
		if err != nil {
			return err
		}
		if len(mediaIDs) > 0 {
			var ok int
			// 삭제 중·삭제된 이미지는 새로 붙일 수 없다 (정리 작업과 경쟁 방지)
			// FOR SHARE로 잠가 같은 순간 정리 작업이 deleting으로 바꾸지 못하게 한다 (정리는 FOR UPDATE 후 참조를 다시 확인)
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM (SELECT id FROM media_assets WHERE id = ANY($1::uuid[]) AND state = 'active' FOR SHARE) t`, mediaIDs).Scan(&ok); err != nil {
				return err
			}
			if ok != len(mediaIDs) {
				return &StateError{Message: "사용할 수 없는 이미지가 있어요 (정리되었거나 처리 중)"}
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM part_media WHERE revision_id = $1 AND part_index = $2`, revID, partIndex); err != nil {
			return err
		}
		for pos, m := range mediaIDs {
			if _, err := tx.Exec(ctx, `INSERT INTO part_media (revision_id, part_index, media_id, position) VALUES ($1, $2, $3, $4)`, revID, partIndex, m, pos); err != nil {
				return err
			}
		}
		if imageMode != "" {
			if _, err := tx.Exec(ctx, `UPDATE content_revisions SET image_mode = $2, review_state = CASE WHEN review_state = 'rejected' THEN 'unreviewed' ELSE review_state END,
				updated_at = now() WHERE id = $1`, revID, imageMode); err != nil {
				return err
			}
		}
		return s.Revalidate(ctx, tx, revID)
	})
	if err != nil {
		return Detail{}, err
	}
	return s.Get(ctx, id)
}

// MediaUsage는 이미지가 쓰인 콘텐츠다 (보관함의 "사용 중" 표시).
type MediaUsage struct {
	ContentID string `json:"content_id"`
	Title     string `json:"title"`
	Revision  int    `json:"revision"`
	Current   bool   `json:"current"`
	PartIndex int    `json:"part_index"`
}

func (s *Service) MediaUsage(ctx context.Context, mediaID string) ([]MediaUsage, error) {
	rows, err := s.pool.Query(ctx, `SELECT c.id, c.title, r.number, c.current_revision_id = r.id, pm.part_index
		FROM part_media pm JOIN content_revisions r ON r.id = pm.revision_id JOIN contents c ON c.id = r.content_id
		WHERE pm.media_id = $1 ORDER BY c.updated_at DESC, r.number DESC LIMIT 50`, mediaID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MediaUsage{}
	for rows.Next() {
		var u MediaUsage
		if err := rows.Scan(&u.ContentID, &u.Title, &u.Revision, &u.Current, &u.PartIndex); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
