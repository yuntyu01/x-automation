package app_test

import (
	"testing"
)

func TestScheduleAPIFlow(t *testing.T) {
	h := newHarness(t) // 2026-10-27 12:00 KST (화)
	if r := h.do("GET", "/api/schedule", nil, nil); r.Status != 401 {
		t.Fatalf("unauthenticated = %d", r.Status)
	}
	h.login()
	r := h.do("GET", "/api/schedule?from=2026-10-27&days=3", nil, nil)
	if r.Status != 200 {
		t.Fatalf("list = %d %s", r.Status, r.Raw)
	}
	days := r.Body["days"].([]any)
	if len(days) != 3 {
		t.Fatalf("days = %d", len(days))
	}
	tomorrow := days[1].(map[string]any)
	if tomorrow["count"] != float64(20) || len(tomorrow["items"].([]any)) != 20 {
		t.Fatalf("tomorrow = %v", tomorrow["count"])
	}
	// 설정 미리보기 → 저장
	st := h.do("GET", "/api/schedule/settings", nil, nil)
	rules := st.Body["rules"].(map[string]any)
	rules["daily_target"] = 10
	body := map[string]any{"rules": rules, "windows": st.Body["windows"], "scope": "tomorrow", "seed": 3}
	pv := h.do("POST", "/api/schedule/settings/preview", body, nil)
	if pv.Status != 200 {
		t.Fatalf("preview = %d %s", pv.Status, pv.Raw)
	}
	ap := h.do("PUT", "/api/schedule/settings", body, nil)
	if ap.Status != 200 || len(ap.Body["slots"].([]any)) != len(pv.Body["slots"].([]any)) {
		t.Fatalf("apply = %d %s", ap.Status, ap.Raw)
	}
	stale := h.do("PUT", "/api/schedule/settings", body, nil) // 같은 version으로 다시
	if stale.Status != 409 {
		t.Fatalf("stale version = %d", stale.Status)
	}
	bad := h.do("POST", "/api/schedule/settings/preview", map[string]any{"rules": rules, "windows": []map[string]any{{"label": "", "weekdays": []int{}, "start_minute": 60, "end_minute": 60, "enabled": true}}, "scope": "now"}, nil)
	if bad.Status != 422 {
		t.Fatalf("invalid window = %d", bad.Status)
	}
	// 글을 만들고 지정 시각(구간 밖)에 넣으면 확인을 요구한다
	c := h.do("POST", "/api/contents", map[string]any{"raw_text": "첫 문단.\n\n둘째 문단."}, nil)
	cid := c.Body["id"].(string)
	pin := map[string]any{"date": "2026-10-28", "time": "19:30", "content_id": cid}
	w := h.do("POST", "/api/schedule/pinned", pin, nil)
	if w.Status != 409 || errCode(w) != "needs_confirm" {
		t.Fatalf("pin warning = %d %s", w.Status, w.Raw)
	}
	pin["confirm"] = true
	if ok := h.do("POST", "/api/schedule/pinned", pin, nil); ok.Status != 201 {
		t.Fatalf("pin confirm = %d %s", ok.Status, ok.Raw)
	}
	// 여러 글 한 번에 다음 빈 시간에 배치
	var ids []string
	for i := 0; i < 3; i++ {
		x := h.do("POST", "/api/contents", map[string]any{"raw_text": "배치할 글입니다.\n\n답글로 이어집니다."}, nil)
		ids = append(ids, x.Body["id"].(string))
	}
	pl := h.do("POST", "/api/schedule/place", map[string]any{"content_ids": ids}, nil)
	if pl.Status != 200 || len(pl.Body["placed"].([]any)) != 3 {
		t.Fatalf("place = %d %s", pl.Status, pl.Raw)
	}
	// 쉬는 날
	ex := h.do("POST", "/api/schedule/exceptions", map[string]any{"date": "2026-10-29", "note": "휴가"}, nil)
	if ex.Status != 200 {
		t.Fatalf("exception = %d %s", ex.Status, ex.Raw)
	}
	l2 := h.do("GET", "/api/schedule?from=2026-10-29&days=1", nil, nil)
	d := l2.Body["days"].([]any)[0].(map[string]any)
	if d["excluded"] != true || d["count"] != float64(0) {
		t.Fatalf("excluded day = %v count %v", d["excluded"], d["count"])
	}
}
