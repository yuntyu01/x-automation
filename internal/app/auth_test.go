package app_test

import (
	"strings"
	"testing"
	"time"
)

func TestUnauthenticatedAPIIsBlocked(t *testing.T) {
	h := newHarness(t)
	for _, p := range []string{"/api/runtime", "/api/settings", "/api/contents", "/api/does-not-exist"} {
		r := h.do("GET", p, nil, nil)
		if r.Status != 401 || errCode(r) != "unauthorized" {
			t.Errorf("%s = %d %s", p, r.Status, r.Raw)
		}
		if strings.Contains(string(r.Raw), "<html") {
			t.Errorf("%s returned SPA html", p)
		}
	}
	// 상태 변경도 마찬가지
	if r := h.do("POST", "/api/auth/logout", nil, nil); r.Status != 401 {
		t.Errorf("logout without session = %d", r.Status)
	}
}

func TestLoginSessionCSRFAndLogout(t *testing.T) {
	h := newHarness(t)
	bad := h.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "wrong-password"}, nil)
	if bad.Status != 401 || errCode(bad) != "invalid_credentials" {
		t.Fatalf("bad login = %d %s", bad.Status, bad.Raw)
	}
	h.login()

	// 쿠키 속성
	r := h.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": testPassword}, nil)
	sc := r.Header.Get("Set-Cookie")
	if !strings.Contains(sc, "HttpOnly") || !strings.Contains(sc, "SameSite=Lax") {
		t.Fatalf("cookie attrs = %s", sc)
	}
	h.csrf, _ = r.Body["csrf_token"].(string)

	if r := h.do("GET", "/api/runtime", nil, nil); r.Status != 200 || r.Body["integration_mode"] != "mock" {
		t.Fatalf("runtime = %d %s", r.Status, r.Raw)
	}
	if r := h.do("GET", "/api/auth/session", nil, nil); r.Status != 200 || r.Body["csrf_token"] != h.csrf {
		t.Fatalf("session = %d %s", r.Status, r.Raw)
	}
	// 알 수 없는 API는 JSON 404 (index.html 아님)
	if r := h.do("GET", "/api/nope", nil, nil); r.Status != 404 || errCode(r) != "not_found" {
		t.Fatalf("unknown api = %d %s", r.Status, r.Raw)
	}

	// CSRF 토큰 없이 상태 변경 → 403
	saved := h.csrf
	h.csrf = ""
	if r := h.do("POST", "/api/auth/logout", nil, nil); r.Status != 403 || errCode(r) != "csrf_failed" {
		t.Fatalf("logout without csrf = %d %s", r.Status, r.Raw)
	}
	h.csrf = saved
	// 다른 출처 → 403
	if r := h.do("POST", "/api/auth/logout", nil, map[string]string{"Origin": "https://evil.example"}); r.Status != 403 || errCode(r) != "bad_origin" {
		t.Fatalf("cross origin = %d %s", r.Status, r.Raw)
	}
	if r := h.do("POST", "/api/auth/logout", nil, nil); r.Status != 200 {
		t.Fatalf("logout = %d %s", r.Status, r.Raw)
	}
	if r := h.do("GET", "/api/runtime", nil, nil); r.Status != 401 {
		t.Fatalf("after logout = %d", r.Status)
	}
}

func TestSessionExpires(t *testing.T) {
	h := newHarness(t)
	h.login()
	h.clock.Advance(59 * time.Minute)
	if r := h.do("GET", "/api/runtime", nil, nil); r.Status != 200 {
		t.Fatalf("before expiry = %d", r.Status)
	}
	h.clock.Advance(2 * time.Minute)
	if r := h.do("GET", "/api/runtime", nil, nil); r.Status != 401 {
		t.Fatalf("after expiry = %d", r.Status)
	}
}

func TestLoginRateLimit(t *testing.T) {
	h := newHarness(t)
	var last resp
	for i := 0; i < 11; i++ {
		last = h.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "nope-nope-nope"}, nil)
	}
	if last.Status != 429 || last.Header.Get("Retry-After") == "" {
		t.Fatalf("expected 429 with Retry-After, got %d %s", last.Status, last.Raw)
	}
	// 올바른 비밀번호도 잠금 중에는 거부
	if r := h.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": testPassword}, nil); r.Status != 429 {
		t.Fatalf("locked login = %d", r.Status)
	}
	// X-Forwarded-For로 우회할 수 없다 (신뢰 프록시 없음)
	if r := h.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": testPassword},
		map[string]string{"X-Forwarded-For": "203.0.113.9"}); r.Status != 429 {
		t.Fatalf("xff bypass = %d", r.Status)
	}
	h.clock.Advance(16 * time.Minute)
	if r := h.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": testPassword}, nil); r.Status != 200 {
		t.Fatalf("after window = %d %s", r.Status, r.Raw)
	}
}

func TestDeepLinkServesSPAAndHealth(t *testing.T) {
	h := newHarness(t)
	if r := h.do("GET", "/healthz", nil, nil); r.Status != 200 {
		t.Fatalf("healthz = %d", r.Status)
	}
	if r := h.do("GET", "/readyz", nil, nil); r.Status != 200 {
		t.Fatalf("readyz = %d %s", r.Status, r.Raw)
	}
	r := h.do("GET", "/contents/abc?x=1", nil, nil)
	if r.Status != 200 && r.Status != 503 {
		t.Fatalf("deep link = %d", r.Status)
	}
}

func TestSessionEndpointForAnonymous(t *testing.T) {
	h := newHarness(t)
	r := h.do("GET", "/api/auth/session", nil, nil)
	if r.Status != 200 || r.Body["authenticated"] != false || r.Body["csrf_token"] != nil {
		t.Fatalf("anonymous session = %d %s", r.Status, r.Raw)
	}
}
