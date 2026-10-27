package app_test

import (
	"strings"
	"testing"
)

func TestHomeDashboardAPI(t *testing.T) {
	h := newHarness(t)
	h.login()
	r := h.do("GET", "/api/home", nil, nil)
	if r.Status != 200 {
		t.Fatalf("home = %d %s", r.Status, r.Raw)
	}
	auto := r.Body["automation"].(map[string]any)
	if auto["enabled"] != false || auto["mock"] != true {
		t.Fatalf("automation = %v", auto)
	}
	joined := ""
	for _, b := range auto["publish_blockers"].([]any) {
		joined += b.(string) + "|"
	}
	if !strings.Contains(joined, "자동 운영") || !strings.Contains(joined, "X 계정") || strings.Contains(joined, "PUBLISHING_ENABLED") {
		t.Fatalf("blockers (mock ignores PUBLISHING_ENABLED) = %s", joined)
	}
	today := r.Body["today"].(map[string]any)
	if today["target"] != float64(20) || today["published"] != float64(0) {
		t.Fatalf("today = %v", today)
	}
	if _, ok := r.Body["usage"].(map[string]any)["counts"]; !ok {
		t.Fatalf("usage = %v", r.Body["usage"])
	}
	// 자동 운영 시작 → 미연결이 중대 조치로 보인다
	a := h.do("POST", "/api/automation", map[string]any{"automation_enabled": true}, nil)
	if a.Status != 200 || a.Body["settings"].(map[string]any)["automation_enabled"] != true {
		t.Fatalf("automation = %d %s", a.Status, a.Raw)
	}
	r = h.do("GET", "/api/home", nil, nil)
	found := false
	for _, x := range r.Body["actions"].([]any) {
		m := x.(map[string]any)
		if m["key"] == "x_connection" && m["severity"] == "critical" && m["link"] == "/settings/connections" {
			found = true
		}
	}
	if !found {
		t.Fatalf("actions = %v", r.Body["actions"])
	}
	if anon := newHarnessClient(h).do("GET", "/api/home", nil, nil); anon.Status != 401 {
		t.Fatalf("anonymous home = %d", anon.Status)
	}
}
