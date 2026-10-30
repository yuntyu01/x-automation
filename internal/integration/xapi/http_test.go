package xapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// 공개 클라이언트(Secret 없음)는 토큰 교환·해제 요청 본문에 client_id를 실어야 한다
func TestPublicClientSendsClientID(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		_, _, basic := r.BasicAuth()
		if basic {
			t.Error("public client must not use basic auth")
		}
		got = append(got, r.URL.Path+" "+r.PostForm.Get("client_id"))
		_, _ = w.Write([]byte(`{"access_token":"a","refresh_token":"r","expires_in":7200,"scope":"tweet.write"}`))
	}))
	defer srv.Close()
	h := NewHTTP(HTTPConfig{ClientID: "cid", RedirectURI: "http://localhost/cb", TokenURL: srv.URL + "/2/oauth2/token"})
	if _, err := h.ExchangeCode(context.Background(), "code", "verifier"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Refresh(context.Background(), "r"); err != nil {
		t.Fatal(err)
	}
	if err := h.Revoke(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	want := []string{"/2/oauth2/token cid", "/2/oauth2/token cid", "/2/oauth2/revoke cid"}
	if len(got) != len(want) {
		t.Fatalf("requests = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("request %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// 기밀 클라이언트는 Basic 인증을 쓰고 본문에 client_id를 넣지 않는다
func TestConfidentialClientUsesBasicAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if u, p, ok := r.BasicAuth(); !ok || u != "cid" || p != "secret" || r.PostForm.Get("client_id") != "" || r.PostForm.Get("code") != "code" {
			t.Errorf("bad confidential request: basic=%v form=%v", ok, r.PostForm)
		}
		_, _ = w.Write([]byte(`{"access_token":"a","expires_in":7200}`))
	}))
	defer srv.Close()
	h := NewHTTP(HTTPConfig{ClientID: "cid", ClientSecret: "secret", TokenURL: srv.URL + "/2/oauth2/token"})
	if _, err := h.ExchangeCode(context.Background(), "code", "verifier"); err != nil {
		t.Fatal(err)
	}
}
