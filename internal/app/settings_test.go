package app_test

import "testing"

func TestSettingsAPIValidatesAndDetectsConflict(t *testing.T) {
	h := newHarness(t)
	h.login()
	r := h.do("GET", "/api/settings", nil, nil)
	if r.Status != 200 || r.Body["automation_enabled"] != false {
		t.Fatalf("get = %d %s", r.Status, r.Raw)
	}
	version := r.Body["version"]
	policy := r.Body["policy"].(map[string]any)
	gen := policy["generation"].(map[string]any)
	gen["daily_at"] = "99:00"
	bad := h.do("PUT", "/api/settings", map[string]any{"version": version, "operation_mode": "full_auto", "timezone": "Asia/Seoul", "policy": policy}, nil)
	fields, _ := bad.Body["error"].(map[string]any)["fields"].(map[string]any)
	if bad.Status != 422 || fields["generation.daily_at"] == nil {
		t.Fatalf("validation = %d %s", bad.Status, bad.Raw)
	}
	gen["daily_at"] = "22:30"
	ok := h.do("PUT", "/api/settings", map[string]any{"version": version, "operation_mode": "full_auto", "timezone": "Asia/Seoul", "policy": policy}, nil)
	if ok.Status != 200 || ok.Body["operation_mode"] != "full_auto" {
		t.Fatalf("save = %d %s", ok.Status, ok.Raw)
	}
	// 생성 정책이 바뀌면 정책 버전이 오른다
	if v := ok.Body["policy"].(map[string]any)["generation"].(map[string]any)["policy_version"]; v != float64(2) {
		t.Fatalf("policy_version = %v", v)
	}
	stale := h.do("PUT", "/api/settings", map[string]any{"version": version, "operation_mode": "review", "timezone": "Asia/Seoul", "policy": policy}, nil)
	if stale.Status != 409 {
		t.Fatalf("stale = %d %s", stale.Status, stale.Raw)
	}
	integ := h.do("GET", "/api/integrations", nil, nil)
	if integ.Status != 200 || integ.Body["mode"] != "mock" {
		t.Fatalf("integrations = %d %s", integ.Status, integ.Raw)
	}
	counts := h.do("GET", "/api/notifications/counts", nil, nil)
	if counts.Status != 200 || counts.Body["unread"] != float64(0) {
		t.Fatalf("counts = %d %s", counts.Status, counts.Raw)
	}
}
