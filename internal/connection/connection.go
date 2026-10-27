// Package connection은 X 계정 연결 상태를 저장·조회한다. 토큰은 암호화된 채로만 DB에 둔다.
package connection

import (
	"context"
	"time"

	"github.com/yuntyu01/x-automation/internal/store"
)

type Status string

const (
	Disconnected Status = "disconnected"
	Connected    Status = "connected"
	Expired      Status = "expired"
	Errored      Status = "error"
)

// View는 화면에 보여 줄 수 있는 정보만 담는다 (토큰 없음).
type View struct {
	Status        Status         `json:"status"`
	StatusDetail  string         `json:"status_detail,omitempty"`
	AccountID     string         `json:"account_id,omitempty"`
	Username      string         `json:"username,omitempty"`
	DisplayName   string         `json:"display_name,omitempty"`
	Scopes        []string       `json:"scopes"`
	Capabilities  map[string]any `json:"capabilities"`
	ConnectedAt   *time.Time     `json:"connected_at,omitempty"`
	TokenExpires  *time.Time     `json:"token_expires_at,omitempty"`
	HasRefreshKey bool           `json:"has_refresh_token"`
}

type Repo struct{ db store.DB }

func NewRepo(db store.DB) *Repo { return &Repo{db: db} }

func (r *Repo) View(ctx context.Context) (View, error) {
	v := View{Status: Disconnected, Scopes: []string{}, Capabilities: map[string]any{}}
	err := r.db.QueryRow(ctx, `SELECT status, COALESCE(status_detail, ''), COALESCE(account_id, ''), COALESCE(username, ''),
		COALESCE(display_name, ''), scopes, capabilities, connected_at, token_expires_at, refresh_token_enc IS NOT NULL
		FROM connections WHERE provider = 'x'`).
		Scan(&v.Status, &v.StatusDetail, &v.AccountID, &v.Username, &v.DisplayName, &v.Scopes, &v.Capabilities,
			&v.ConnectedAt, &v.TokenExpires, &v.HasRefreshKey)
	if store.IsNoRows(err) {
		return v, nil
	}
	return v, err
}
