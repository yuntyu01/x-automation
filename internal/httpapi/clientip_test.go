package httpapi

import (
	"context"
	"fmt"
	"net"
	"net/http/httptest"
	"testing"
)

func TestClientIP(t *testing.T) {
	_, proxy, _ := net.ParseCIDR("10.0.0.0/8")
	trusted := []*net.IPNet{proxy}
	cases := []struct {
		name   string
		remote string
		xff    []string
		want   string
	}{
		{"untrusted peer ignores header", "203.0.113.5:1000", []string{"198.51.100.1"}, "203.0.113.5"},
		{"trusted proxy", "10.1.2.3:1000", []string{"198.51.100.1"}, "198.51.100.1"},
		{"spoofed left entry", "10.1.2.3:1000", []string{"1.1.1.1, 198.51.100.1"}, "198.51.100.1"},
		// 프록시가 덧붙이지 않고 새 줄을 달면 클라이언트가 보낸 첫 줄을 믿으면 안 된다
		{"separate header lines", "10.1.2.3:1000", []string{"1.1.1.1", "198.51.100.1"}, "198.51.100.1"},
		{"only proxies", "10.1.2.3:1000", []string{"10.9.9.9"}, "10.1.2.3"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		for _, v := range c.xff {
			r.Header.Add("X-Forwarded-For", v)
		}
		if got := ClientIP(r, trusted); got != c.want {
			t.Errorf("%s: got %s want %s", c.name, got, c.want)
		}
	}
}

// 클라이언트가 끊은 요청은 서버 오류(500)로 기록하지 않는다
func TestCanceledRequestIsNotInternalError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("GET", "/api/contents", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	WriteError(w, r, fmt.Errorf("query: %w", context.Canceled))
	if w.Code != 499 {
		t.Fatalf("status = %d", w.Code)
	}
	// 요청은 살아 있는데 내부에서 취소된 오류는 여전히 서버 오류다
	r2 := httptest.NewRequest("GET", "/api/contents", nil)
	w2 := httptest.NewRecorder()
	WriteError(w2, r2, context.Canceled)
	if w2.Code != 500 {
		t.Fatalf("internal cancel status = %d", w2.Code)
	}
}
