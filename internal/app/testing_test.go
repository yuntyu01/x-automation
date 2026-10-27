package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/yuntyu01/x-automation/internal/app"
	"github.com/yuntyu01/x-automation/internal/auth"
	"github.com/yuntyu01/x-automation/internal/clock"
	"github.com/yuntyu01/x-automation/internal/config"
	"github.com/yuntyu01/x-automation/internal/testutil/pgtest"
)

const testPassword = "correct horse battery staple"

type harness struct {
	t     *testing.T
	srv   *httptest.Server
	clock *clock.Fake
	jar   http.CookieJar
	csrf  string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	pool := pgtest.New(t)
	hash, err := auth.HashPassword(testPassword, auth.Params{Memory: 8 * 1024, Time: 1, Threads: 1, KeyLen: 32, SaltLen: 16})
	if err != nil {
		t.Fatal(err)
	}
	fc := clock.NewFake(time.Date(2026, 10, 27, 3, 0, 0, 0, time.UTC))
	srv := httptest.NewUnstartedServer(nil)
	srv.Start()
	base, _ := url.Parse(srv.URL)
	cfg := &config.Config{
		Env: config.EnvTest, BaseURL: base, AdminUsername: "admin", AdminPasswordHash: hash,
		TokenKey: bytes.Repeat([]byte{1}, 32), SessionTTL: time.Hour, IntegrationMode: config.ModeMock,
		DataDir: t.TempDir(),
	}
	a := app.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), app.Deps{Clock: fc})
	if err := a.OpenWithPool(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	srv.Config.Handler = a.Handler()
	t.Cleanup(func() {
		srv.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		a.Shutdown(ctx)
	})
	jar, _ := cookiejar.New(nil)
	return &harness{t: t, srv: srv, clock: fc, jar: jar}
}

type resp struct {
	Status int
	Header http.Header
	Body   map[string]any
	Raw    []byte
}

func (h *harness) do(method, path string, body any, hdr map[string]string) resp {
	h.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, h.srv.URL+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if h.csrf != "" {
		req.Header.Set("X-CSRF-Token", h.csrf)
	}
	for k, v := range hdr {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	c := &http.Client{Jar: h.jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := c.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := resp{Status: res.StatusCode, Header: res.Header, Raw: raw}
	_ = json.Unmarshal(raw, &out.Body)
	return out
}

func (h *harness) login() {
	h.t.Helper()
	r := h.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": testPassword}, nil)
	if r.Status != 200 {
		h.t.Fatalf("login status %d: %s", r.Status, r.Raw)
	}
	h.csrf, _ = r.Body["csrf_token"].(string)
}

func errCode(r resp) string {
	e, _ := r.Body["error"].(map[string]any)
	s, _ := e["code"].(string)
	return s
}

// newHarnessClient는 같은 서버에 쿠키가 없는 다른 브라우저로 접속한다.
func newHarnessClient(h *harness) *harness {
	jar, _ := cookiejar.New(nil)
	return &harness{t: h.t, srv: h.srv, clock: h.clock, jar: jar}
}
