package httpapi

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/yuntyu01/x-automation/internal/auth"
	"github.com/yuntyu01/x-automation/internal/budget"
	"github.com/yuntyu01/x-automation/internal/connection"
	"github.com/yuntyu01/x-automation/internal/integration"
	"github.com/yuntyu01/x-automation/internal/publish"
)

func publishError(err error) error {
	var ve *publish.ValidationError
	var we *publish.WarningError
	switch {
	case errors.As(err, &ve):
		return FieldError(ve.Fields)
	case errors.As(err, &we):
		e := NewError(http.StatusConflict, "needs_confirm", "확인이 필요해요")
		e.Details = map[string]any{"warnings": we.Warnings}
		return e
	case errors.Is(err, publish.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, publish.ErrBusy):
		return NewError(http.StatusConflict, "busy", err.Error())
	case budget.IsExceeded(err):
		return NewError(http.StatusConflict, "budget_exceeded", err.Error())
	}
	return connError(err)
}

func connError(err error) error {
	var fe *connection.FlowError
	var ie *integration.Error
	switch {
	case errors.As(err, &fe):
		return NewError(http.StatusBadRequest, "x_flow", fe.Message)
	case errors.Is(err, connection.ErrOtherAccount):
		return NewError(http.StatusConflict, "x_other_account", err.Error())
	case errors.Is(err, connection.ErrNotConnected):
		return NewError(http.StatusConflict, "x_not_connected", err.Error())
	case errors.As(err, &ie) && ie.Class == integration.Config:
		return NewError(http.StatusConflict, "x_config", ie.Message)
	case errors.As(err, &ie):
		return NewError(http.StatusBadGateway, "x_"+string(ie.Class), ie.Message)
	}
	return err
}

// handleXCallback은 X 인가 뒤 돌아오는 주소다. 결과를 연결 화면 주소의 쿼리로 넘긴다 (토큰·코드는 넘기지 않는다).
func (s *Server) handleXCallback(w http.ResponseWriter, r *http.Request) {
	back := func(q url.Values) {
		http.Redirect(w, r, "/settings/connections?"+q.Encode(), http.StatusSeeOther)
	}
	if s.XConn == nil {
		back(url.Values{"x_error": {"X 연결 기능이 준비되지 않았어요"}})
		return
	}
	c, err := r.Cookie(auth.CookieName)
	if err != nil {
		back(url.Values{"x_error": {"로그인이 풀렸어요. 다시 로그인한 뒤 연결해 주세요"}})
		return
	}
	sess, err := s.Auth.Authenticate(r.Context(), c.Value)
	if err != nil {
		back(url.Values{"x_error": {"로그인이 풀렸어요. 다시 로그인한 뒤 연결해 주세요"}})
		return
	}
	q := r.URL.Query()
	if _, err := s.XConn.Complete(r.Context(), sess.IDHash, q.Get("state"), q.Get("code"), q.Get("error")); err != nil {
		msg := "X 연결에 실패했어요. 다시 시도해 주세요"
		var e *Error
		if errors.As(connError(err), &e) {
			msg = e.Message
		}
		back(url.Values{"x_error": {msg}})
		return
	}
	back(url.Values{"x": {"connected"}})
}

func (s *Server) mountPublish(mux *http.ServeMux) {
	if s.XConn != nil {
		mux.HandleFunc("POST /api/x/oauth/start", func(w http.ResponseWriter, r *http.Request) {
			sess, _ := SessionFrom(r.Context())
			u, err := s.XConn.Begin(r.Context(), sess.IDHash)
			if err != nil {
				WriteError(w, r, connError(err))
				return
			}
			WriteJSON(w, http.StatusOK, map[string]string{"url": u})
		})
		mux.HandleFunc("POST /api/x/disconnect", func(w http.ResponseWriter, r *http.Request) {
			if err := s.XConn.Disconnect(r.Context()); err != nil {
				WriteError(w, r, connError(err))
				return
			}
			v, err := s.Connections.View(r.Context())
			if err != nil {
				WriteError(w, r, err)
				return
			}
			WriteJSON(w, http.StatusOK, v)
		})
		mux.HandleFunc("POST /api/x/check", func(w http.ResponseWriter, r *http.Request) {
			v, err := s.XConn.Check(r.Context())
			if err != nil {
				WriteError(w, r, connError(err))
				return
			}
			WriteJSON(w, http.StatusOK, v)
		})
	}
	if s.Publish == nil {
		return
	}
	mux.HandleFunc("POST /api/publish/now", func(w http.ResponseWriter, r *http.Request) {
		var in publish.PostNowInput
		if err := DecodeJSON(w, r, &in); err != nil {
			WriteError(w, r, err)
			return
		}
		id, err := s.Publish.PostNow(r.Context(), in)
		if err != nil {
			WriteError(w, r, publishError(err))
			return
		}
		v, err := s.Publish.Run(r.Context(), id)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusAccepted, v)
	})
	mux.HandleFunc("GET /api/publish/runs", func(w http.ResponseWriter, r *http.Request) {
		list, err := s.Publish.ListRuns(r.Context())
		if err != nil {
			WriteError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, list)
	})
	mux.HandleFunc("GET /api/publish/runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, err := s.Publish.Run(r.Context(), r.PathValue("id"))
		if err != nil {
			WriteError(w, r, publishError(err))
			return
		}
		WriteJSON(w, http.StatusOK, v)
	})
	after := func(w http.ResponseWriter, r *http.Request, id string, err error) {
		if err != nil {
			WriteError(w, r, publishError(err))
			return
		}
		v, err := s.Publish.Run(r.Context(), id)
		if err != nil {
			WriteError(w, r, publishError(err))
			return
		}
		WriteJSON(w, http.StatusOK, v)
	}
	mux.HandleFunc("POST /api/publish/runs/{id}/resolve", func(w http.ResponseWriter, r *http.Request) {
		var in publish.ResolveInput
		if err := DecodeJSON(w, r, &in); err != nil {
			WriteError(w, r, err)
			return
		}
		id := r.PathValue("id")
		after(w, r, id, s.Publish.Resolve(r.Context(), id, in))
	})
	mux.HandleFunc("POST /api/publish/runs/{id}/retry", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			AcceptDuplicateRisk bool `json:"accept_duplicate_risk"`
		}
		if err := DecodeJSON(w, r, &in); err != nil {
			WriteError(w, r, err)
			return
		}
		id := r.PathValue("id")
		after(w, r, id, s.Publish.Retry(r.Context(), id, in.AcceptDuplicateRisk))
	})
	mux.HandleFunc("POST /api/publish/runs/{id}/abandon", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		after(w, r, id, s.Publish.Abandon(r.Context(), id))
	})
}
