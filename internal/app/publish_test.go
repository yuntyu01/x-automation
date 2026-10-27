package app_test

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestXConnectAndPostNowMockFlow(t *testing.T) {
	h := newHarness(t) // 12:00 KST, 기본 허용 구간 시작
	h.login()
	// 연결 전에는 지금 게시가 막힌다
	c := h.do("POST", "/api/contents", map[string]any{"raw_text": "지금 게시할 본문.\n---\n이어지는 답글.", "split_mode": "manual"}, nil)
	cid := c.Body["id"].(string)
	// 기본은 검토 후 게시 모드: 승인한 revision만 게시한다
	if ap := h.do("POST", "/api/contents/"+cid+"/approve", map[string]any{"revision_id": c.Body["current_revision_id"]}, nil); ap.Status != 200 {
		t.Fatalf("approve = %d %s", ap.Status, ap.Raw)
	}
	if r := h.do("POST", "/api/publish/now", map[string]any{"content_id": cid}, nil); r.Status != 422 {
		t.Fatalf("post-now without x = %d %s", r.Status, r.Raw)
	}
	// 연결 시작은 CSRF 토큰이 필요하다
	if r := h.do("POST", "/api/x/oauth/start", nil, map[string]string{"X-CSRF-Token": ""}); r.Status != 403 {
		t.Fatalf("start without csrf = %d", r.Status)
	}
	st := h.do("POST", "/api/x/oauth/start", nil, nil)
	if st.Status != 200 {
		t.Fatalf("start = %d %s", st.Status, st.Raw)
	}
	u, _ := url.Parse(st.Body["url"].(string))
	// 다른 세션(로그인 안 한 브라우저)으로 콜백 → 연결 화면으로 오류와 함께 돌아간다
	anon := newHarnessClient(h)
	if r := anon.do("GET", u.Path+"?"+u.RawQuery, nil, nil); r.Status != 303 || !strings.Contains(r.Header.Get("Location"), "x_error=") {
		t.Fatalf("anonymous callback = %d %s", r.Status, r.Header.Get("Location"))
	}
	cb := h.do("GET", u.Path+"?"+u.RawQuery, nil, nil)
	if cb.Status != 303 || cb.Header.Get("Location") != "/settings/connections?x=connected" {
		t.Fatalf("callback = %d %s", cb.Status, cb.Header.Get("Location"))
	}
	if again := h.do("GET", u.Path+"?"+u.RawQuery, nil, nil); !strings.Contains(again.Header.Get("Location"), "x_error=") {
		t.Fatalf("state reuse = %s", again.Header.Get("Location"))
	}
	in := h.do("GET", "/api/integrations", nil, nil)
	conn := in.Body["x"].(map[string]any)["connection"].(map[string]any)
	if conn["status"] != "connected" || conn["username"] != "mock_account" {
		t.Fatalf("connection = %v", conn)
	}
	if strings.Contains(string(in.Raw), "mock-access") {
		t.Fatal("token leaked in API response")
	}
	pn := h.do("POST", "/api/publish/now", map[string]any{"content_id": cid}, nil)
	if pn.Status != 202 {
		t.Fatalf("post-now = %d %s", pn.Status, pn.Raw)
	}
	runID := pn.Body["id"].(string)
	var run resp
	for i := 0; i < 100; i++ {
		run = h.do("GET", "/api/publish/runs/"+runID, nil, nil)
		if run.Body["state"] == "completed" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if run.Body["state"] != "completed" {
		t.Fatalf("run = %s", run.Raw)
	}
	parts := run.Body["parts"].([]any)
	p0, p1 := parts[0].(map[string]any), parts[1].(map[string]any)
	if p1["in_reply_to"] != p0["x_post_id"] || !strings.Contains(p0["x_url"].(string), "/mock_account/status/") {
		t.Fatalf("parts = %v", parts)
	}
	// 같은 글을 바로 다시 게시하려 하면 직전 게시와 간격 경고 → 확인 필요
	again := h.do("POST", "/api/publish/now", map[string]any{"content_id": cid}, nil)
	if again.Status != 409 || errCode(again) != "needs_confirm" {
		t.Fatalf("second post-now = %d %s", again.Status, again.Raw)
	}
	list := h.do("GET", "/api/publish/runs", nil, nil)
	if len(list.Body["recent"].([]any)) != 1 || len(list.Body["attention"].([]any)) != 0 {
		t.Fatalf("runs = %s", list.Raw)
	}
	// 연결 해제
	if d := h.do("POST", "/api/x/disconnect", nil, nil); d.Status != 200 || d.Body["status"] != "disconnected" {
		t.Fatalf("disconnect = %d %s", d.Status, d.Raw)
	}
}
