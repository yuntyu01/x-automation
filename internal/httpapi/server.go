package httpapi

import (
	"context"
	"net"
	"net/http"
	"net/url"

	"github.com/yuntyu01/x-automation/internal/auth"
	"github.com/yuntyu01/x-automation/internal/budget"
	"github.com/yuntyu01/x-automation/internal/clock"
	"github.com/yuntyu01/x-automation/internal/connection"
	"github.com/yuntyu01/x-automation/internal/content"
	"github.com/yuntyu01/x-automation/internal/dashboard"
	"github.com/yuntyu01/x-automation/internal/generation"
	"github.com/yuntyu01/x-automation/internal/media"
	"github.com/yuntyu01/x-automation/internal/notification"
	"github.com/yuntyu01/x-automation/internal/prompt"
	"github.com/yuntyu01/x-automation/internal/publish"
	"github.com/yuntyu01/x-automation/internal/retention"
	"github.com/yuntyu01/x-automation/internal/schedule"
	"github.com/yuntyu01/x-automation/internal/settings"
)

// Server는 API 핸들러가 쓰는 서비스 묶음이다. 도메인 규칙은 각 서비스에 있고 여기서는 요청·응답만 다룬다.
type Server struct {
	BaseURL        *url.URL
	FrontendURL    *url.URL
	SecureCookies  bool
	TrustedProxies []*net.IPNet
	Clock          clock.Clock
	Info           RuntimeInfo

	Auth          *auth.Service
	Settings      *settings.Repo
	Notifications *notification.Service
	Connections   *connection.Repo
	Integrations  IntegrationProvider
	Contents      *content.Service
	Prompts       *prompt.Service
	Media         *media.Service
	Generation    *generation.Service
	Planner       *generation.Planner
	Budget        *budget.Service
	Schedule      *schedule.Service
	XConn         *connection.Service
	Publish       *publish.Service
	Dashboard     *dashboard.Service
	Retention     *retention.Service

	extra []func(mux *http.ServeMux) // 단계별 기능 라우트
}

// RuntimeInfo는 화면에 보여 줄 실행 정보다 (비밀 없음).
type RuntimeInfo struct {
	Env               string `json:"env"`
	IntegrationMode   string `json:"integration_mode"`
	PublishingEnabled bool   `json:"publishing_enabled"`
	Version           string `json:"version"`
}

type sessionKey struct{}

func SessionFrom(ctx context.Context) (auth.Session, bool) {
	s, ok := ctx.Value(sessionKey{}).(auth.Session)
	return s, ok
}

// Mount는 인증이 필요한 기능 라우트를 추가한다.
func (s *Server) Mount(fn func(mux *http.ServeMux)) { s.extra = append(s.extra, fn) }

// Routes는 /api/ 핸들러를 만든다. 공개 경로는 로그인·세션 확인뿐이다.
func (s *Server) Routes() http.Handler {
	public := http.NewServeMux()
	public.HandleFunc("POST /api/auth/login", s.handleLogin)
	public.HandleFunc("GET /api/auth/session", s.handleSession)
	// X 인가 콜백은 브라우저 이동으로 들어온다: JSON 401 대신 화면으로 돌려보내려고 세션을 직접 확인한다
	public.HandleFunc("GET /api/x/oauth/callback", s.handleXCallback)

	protected := http.NewServeMux()
	protected.HandleFunc("POST /api/auth/logout", s.handleLogout)
	protected.HandleFunc("GET /api/runtime", func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, s.Info)
	})
	s.mountSettings(protected)
	s.mountNotifications(protected)
	s.mountContents(protected)
	s.mountPrompts(protected)
	s.mountMedia(protected)
	s.mountGeneration(protected)
	s.mountSchedule(protected)
	s.mountPublish(protected)
	if s.Dashboard != nil {
		protected.HandleFunc("GET /api/home", func(w http.ResponseWriter, r *http.Request) {
			h, err := s.Dashboard.Home(r.Context())
			if err != nil {
				WriteError(w, r, err)
				return
			}
			WriteJSON(w, http.StatusOK, h)
		})
	}
	for _, fn := range s.extra {
		fn(protected)
	}
	protected.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, r, ErrNotFound)
	})
	public.Handle("/api/", s.requireAuth(protected))
	return s.cors(public)
}

// Only the configured frontend origin may make credentialed cross-origin API calls.
func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Origin")
		origin := r.Header.Get("Origin")
		allowed := s.FrontendURL != nil && origin == s.FrontendURL.String()
		if allowed {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			if !allowed {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE")
			w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, X-CSRF-Token")
			w.Header().Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isUnsafe(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

// sameOrigin은 Origin(없으면 Referer)이 앱 주소와 같은지 본다. 둘 다 없으면 통과(동일 출처 요청이 아닐 수 있으나 CSRF 토큰이 따로 막는다).
func (s *Server) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		if ref := r.Header.Get("Referer"); ref != "" {
			if u, err := url.Parse(ref); err == nil {
				origin = u.Scheme + "://" + u.Host
			}
		}
	}
	if origin == "" || s.BaseURL == nil {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if u.Scheme == s.BaseURL.Scheme && u.Host == s.BaseURL.Host {
		return true
	}
	if s.FrontendURL != nil && origin == s.FrontendURL.String() {
		return true
	}
	// 개발 중 Vite 프록시(localhost:5173)는 같은 호스트의 다른 포트로 온다
	if s.BaseURL.Scheme == "http" && u.Scheme == "http" && u.Hostname() == s.BaseURL.Hostname() {
		return true
	}
	return false
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(auth.CookieName)
		if err != nil {
			WriteError(w, r, ErrUnauthorized)
			return
		}
		sess, err := s.Auth.Authenticate(r.Context(), c.Value)
		if err != nil {
			if err == auth.ErrNoSession {
				s.clearCookie(w)
				WriteError(w, r, ErrUnauthorized)
				return
			}
			WriteError(w, r, err)
			return
		}
		if isUnsafe(r.Method) {
			if !s.sameOrigin(r) {
				WriteError(w, r, NewError(http.StatusForbidden, "bad_origin", "다른 사이트에서 온 요청은 처리하지 않습니다"))
				return
			}
			if !auth.ValidCSRF(sess, r.Header.Get("X-CSRF-Token")) {
				WriteError(w, r, NewError(http.StatusForbidden, "csrf_failed", "보안 토큰이 맞지 않습니다. 새로고침 후 다시 시도해 주세요"))
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionKey{}, sess)))
	})
}
