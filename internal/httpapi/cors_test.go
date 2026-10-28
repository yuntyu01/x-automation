package httpapi

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestFrontendOriginCORS(t *testing.T) {
	base, _ := url.Parse("https://example-api.kodeploy.com")
	front, _ := url.Parse("https://example.kodeploy.com")
	s := &Server{BaseURL: base, FrontendURL: front}
	h := s.cors(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for _, tc := range []struct {
		origin  string
		allowed bool
	}{{front.String(), true}, {"https://evil.example", false}, {"https://example.kodeploy.com.evil.example", false}} {
		r := httptest.NewRequest(http.MethodOptions, "/api/auth/login", nil)
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Access-Control-Request-Method", "POST")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if tc.allowed {
			if w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Allow-Origin") != tc.origin || w.Header().Get("Access-Control-Allow-Credentials") != "true" || !s.sameOrigin(r) {
				t.Fatalf("allowed frontend preflight failed: %d %v", w.Code, w.Header())
			}
		} else if w.Code != http.StatusForbidden || w.Header().Get("Access-Control-Allow-Origin") != "" || s.sameOrigin(r) {
			t.Fatalf("untrusted origin accepted: %s %d %v", tc.origin, w.Code, w.Header())
		}
	}
}

func TestOAuthRedirectUsesFrontend(t *testing.T) {
	front, _ := url.Parse("https://example.kodeploy.com")
	s := &Server{FrontendURL: front}
	w := httptest.NewRecorder()
	s.handleXCallback(w, httptest.NewRequest(http.MethodGet, "/api/x/oauth/callback", nil))
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "https://example.kodeploy.com/settings/connections?x_error=X+%EC%97%B0%EA%B2%B0+%EA%B8%B0%EB%8A%A5%EC%9D%B4+%EC%A4%80%EB%B9%84%EB%90%98%EC%A7%80+%EC%95%8A%EC%95%98%EC%96%B4%EC%9A%94" {
		t.Fatalf("unexpected callback redirect: %d %s", w.Code, w.Header().Get("Location"))
	}
}
