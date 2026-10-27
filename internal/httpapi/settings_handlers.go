package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/yuntyu01/x-automation/internal/connection"
	"github.com/yuntyu01/x-automation/internal/notification"
	"github.com/yuntyu01/x-automation/internal/settings"
)

// IntegrationStatus는 연결 상태 화면에 쓰는 요약이다 (비밀값·키 원문 없음).
type IntegrationStatus struct {
	Mode string `json:"mode"`
	X    struct {
		Configured bool            `json:"configured"`
		Connection connection.View `json:"connection"`
	} `json:"x"`
	AI struct {
		Configured bool   `json:"configured"`
		Provider   string `json:"provider"`
		TextModel  string `json:"text_model"`
		ImageModel string `json:"image_model"`
	} `json:"ai"`
	R2 struct {
		Configured    bool   `json:"configured"`
		Bucket        string `json:"bucket"`
		Prefix        string `json:"prefix"`
		PublicBaseURL string `json:"public_base_url"`
		DevURL        bool   `json:"dev_url"` // r2.dev 주소 (운영 트래픽 제한)
	} `json:"r2"`
	PublishingEnabled bool `json:"publishing_enabled"`
}

// IntegrationProvider는 앱이 채워 주는 연동 설정 요약이다.
type IntegrationProvider func() IntegrationStatus

func (s *Server) mountSettings(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/settings", func(w http.ResponseWriter, r *http.Request) {
		st, err := s.Settings.Get(r.Context())
		if err != nil {
			WriteError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, st)
	})
	mux.HandleFunc("PUT /api/settings", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Version       int                    `json:"version"`
			OperationMode settings.OperationMode `json:"operation_mode"`
			Timezone      string                 `json:"timezone"`
			Policy        settings.Policy        `json:"policy"`
		}
		if err := DecodeJSON(w, r, &in); err != nil {
			WriteError(w, r, err)
			return
		}
		fields := in.Policy.Validate()
		if !in.OperationMode.Valid() {
			fields["operation_mode"] = "운영 모드를 선택하세요"
		}
		if _, err := time.LoadLocation(in.Timezone); err != nil || in.Timezone == "" {
			fields["timezone"] = "올바른 시간대가 아닙니다 (예: Asia/Seoul)"
		}
		if len(fields) > 0 {
			WriteError(w, r, FieldError(fields))
			return
		}
		cur, err := s.Settings.Get(r.Context())
		if err != nil {
			WriteError(w, r, err)
			return
		}
		// 생성 정책이 바뀌면 정책 버전을 올려 이전 정책으로 잡힌 생성 요청과 구분한다
		p := in.Policy
		p.Generation.PolicyVersion = cur.Policy.Generation.PolicyVersion
		if generationChanged(cur.Policy.Generation, p.Generation) {
			p.Generation.PolicyVersion++
		}
		st, err := s.Settings.Update(r.Context(), in.Version, in.OperationMode, in.Timezone, p)
		if errors.Is(err, settings.ErrVersionConflict) {
			WriteError(w, r, ErrConflict)
			return
		}
		if err != nil {
			WriteError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, st)
	})
	mux.HandleFunc("POST /api/setup/complete", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Settings.CompleteSetup(r.Context()); err != nil {
			WriteError(w, r, err)
			return
		}
		st, err := s.Settings.Get(r.Context())
		if err != nil {
			WriteError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, st)
	})
	mux.HandleFunc("GET /api/integrations", func(w http.ResponseWriter, r *http.Request) {
		st := s.Integrations()
		v, err := s.Connections.View(r.Context())
		if err != nil {
			WriteError(w, r, err)
			return
		}
		st.X.Connection = v
		WriteJSON(w, http.StatusOK, st)
	})
}

func generationChanged(a, b settings.GenerationPolicy) bool {
	a.PolicyVersion, b.PolicyVersion = 0, 0
	return a != b
}

func (s *Server) mountNotifications(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/notifications", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
		limit, _ := strconv.Atoi(q.Get("limit"))
		list, err := s.Notifications.List(r.Context(), notification.ListOptions{UnreadOnly: q.Get("unread") == "1", Before: before, Limit: limit})
		if err != nil {
			WriteError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"items": list})
	})
	mux.HandleFunc("GET /api/notifications/counts", func(w http.ResponseWriter, r *http.Request) {
		c, err := s.Notifications.Counts(r.Context())
		if err != nil {
			WriteError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, c)
	})
	mux.HandleFunc("POST /api/notifications/{id}/read", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			WriteError(w, r, ErrNotFound)
			return
		}
		if err := s.Notifications.MarkRead(r.Context(), id); err != nil {
			WriteError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/notifications/read-all", func(w http.ResponseWriter, r *http.Request) {
		n, err := s.Notifications.MarkAllRead(r.Context())
		if err != nil {
			WriteError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]int64{"updated": n})
	})
}
