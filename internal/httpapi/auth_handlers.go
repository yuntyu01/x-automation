package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/yuntyu01/x-automation/internal/auth"
)

type sessionView struct {
	Authenticated bool      `json:"authenticated"`
	Username      string    `json:"username,omitempty"`
	CSRFToken     string    `json:"csrf_token,omitempty"`
	ExpiresAt     time.Time `json:"expires_at,omitzero"`
}

func (s *Server) setCookie(w http.ResponseWriter, raw string, expires time.Time) {
	now := time.Now()
	if s.Clock != nil {
		now = s.Clock.Now()
	}
	// 만료는 서버 시계 기준 상대값(Max-Age)으로 준다
	http.SetCookie(w, &http.Cookie{
		Name: auth.CookieName, Value: raw, Path: "/",
		MaxAge: max(int(expires.Sub(now).Seconds()), 1), HttpOnly: true, Secure: s.SecureCookies,
		// OAuth 콜백(최상위 GET 이동)에 쿠키가 실려야 하므로 Lax. 상태 변경은 CSRF 토큰이 막는다
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: auth.CookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: s.SecureCookies, SameSite: http.SameSiteLaxMode})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(r) {
		WriteError(w, r, NewError(http.StatusForbidden, "bad_origin", "다른 사이트에서 온 요청은 처리하지 않습니다"))
		return
	}
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := DecodeJSONLimit(w, r, &in, 8<<10); err != nil {
		WriteError(w, r, err)
		return
	}
	fields := map[string]string{}
	if strings.TrimSpace(in.Username) == "" {
		fields["username"] = "아이디를 입력하세요"
	}
	if in.Password == "" {
		fields["password"] = "비밀번호를 입력하세요"
	}
	if len(in.Password) > 1024 {
		fields["password"] = "비밀번호가 너무 깁니다"
	}
	if len(fields) > 0 {
		WriteError(w, r, FieldError(fields))
		return
	}
	// 이미 로그인한 세션이 있으면 무효화하고 새로 발급한다 (세션 고정 방지)
	if c, err := r.Cookie(auth.CookieName); err == nil {
		_ = s.Auth.Logout(r.Context(), c.Value)
	}
	raw, sess, err := s.Auth.Login(r.Context(), strings.TrimSpace(in.Username), in.Password,
		ClientIP(r, s.TrustedProxies), r.UserAgent())
	var rl *auth.RateLimitedError
	switch {
	case errors.As(err, &rl):
		w.Header().Set("Retry-After", strconv.Itoa(int(rl.RetryAfter.Seconds())+1))
		WriteError(w, r, NewError(http.StatusTooManyRequests, "rate_limited", rl.Error()))
		return
	case errors.Is(err, auth.ErrInvalidCredentials):
		WriteError(w, r, NewError(http.StatusUnauthorized, "invalid_credentials", err.Error()))
		return
	case err != nil:
		WriteError(w, r, err)
		return
	}
	s.setCookie(w, raw, sess.ExpiresAt)
	WriteJSON(w, http.StatusOK, sessionView{Authenticated: true, Username: sess.Username, CSRFToken: sess.CSRFToken, ExpiresAt: sess.ExpiresAt})
}

// handleSession은 로그인 여부를 알려 준다. 비로그인도 정상 응답(200)이라 브라우저 콘솔에 오류가 남지 않는다.
func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	anon := sessionView{Authenticated: false}
	c, err := r.Cookie(auth.CookieName)
	if err != nil {
		WriteJSON(w, http.StatusOK, anon)
		return
	}
	sess, err := s.Auth.Authenticate(r.Context(), c.Value)
	if err != nil {
		if errors.Is(err, auth.ErrNoSession) {
			s.clearCookie(w)
			WriteJSON(w, http.StatusOK, anon)
			return
		}
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, sessionView{Authenticated: true, Username: sess.Username, CSRFToken: sess.CSRFToken, ExpiresAt: sess.ExpiresAt})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(auth.CookieName); err == nil {
		if err := s.Auth.Logout(r.Context(), c.Value); err != nil {
			WriteError(w, r, err)
			return
		}
	}
	s.clearCookie(w)
	WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
