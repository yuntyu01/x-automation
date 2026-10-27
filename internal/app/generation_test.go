package app_test

import (
	"strings"
	"testing"
	"time"
)

func (h *harness) waitRequest(id string) map[string]any {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		r := h.do("GET", "/api/generation/"+id, nil, nil)
		if st, _ := r.Body["state"].(string); st != "queued" && st != "running" {
			return r.Body
		}
		time.Sleep(50 * time.Millisecond)
	}
	h.t.Fatal("generation did not finish")
	return nil
}

func TestGenerationAPIFlow(t *testing.T) {
	h := newHarness(t)
	if r := h.do("POST", "/api/generation", map[string]any{}, nil); r.Status != 401 {
		t.Fatalf("unauthenticated generation = %d", r.Status)
	}
	h.login()
	p := h.do("POST", "/api/prompts", map[string]any{"name": "단편", "body": "{{topic}} 이야기", "variables": []map[string]any{{"name": "topic", "label": "주제", "type": "text", "required": true}}}, nil)
	if p.Status != 201 {
		t.Fatalf("prompt = %d %s", p.Status, p.Raw)
	}
	pid := p.Body["id"].(string)

	// 필수 변수 누락은 필드 오류
	bad := h.do("POST", "/api/generation", map[string]any{"idempotency_key": "k0", "prompt_id": pid, "quantity": 1, "options": map[string]any{"text": true}}, nil)
	if bad.Status != 422 || !strings.Contains(string(bad.Raw), "variables.topic") {
		t.Fatalf("missing variable = %d %s", bad.Status, bad.Raw)
	}
	est := h.do("POST", "/api/generation/estimate", map[string]any{"quantity": 5, "options": map[string]any{"text": true}}, nil)
	if est.Status != 200 || est.Body["pricing_known"] != false {
		t.Fatalf("estimate = %d %s", est.Status, est.Raw)
	}
	body := map[string]any{"idempotency_key": "k1", "prompt_id": pid, "quantity": 2, "variables": map[string]string{"topic": "등대"}, "options": map[string]any{"text": true}}
	g := h.do("POST", "/api/generation", body, nil)
	if g.Status != 201 {
		t.Fatalf("submit = %d %s", g.Status, g.Raw)
	}
	again := h.do("POST", "/api/generation", body, nil)
	if again.Status != 200 || again.Body["id"] != g.Body["id"] {
		t.Fatalf("double tap must return the same request: %d %s", again.Status, again.Raw)
	}
	done := h.waitRequest(g.Body["id"].(string))
	if done["state"] != "succeeded" || done["produced"] != float64(2) {
		t.Fatalf("request = %v", done)
	}
	list := h.do("GET", "/api/contents?q=%EB%AA%A9%EC%97%85", nil, nil) // "목업"
	if list.Status != 200 || list.Body["total"] != float64(2) {
		t.Fatalf("generated contents = %s", list.Raw)
	}
	items := list.Body["items"].([]any)
	cid := items[0].(map[string]any)["id"].(string)

	// 부분 재생성 → 비교 → 적용
	re := h.do("POST", "/api/contents/"+cid+"/regenerate", map[string]any{"idempotency_key": "re1", "target": "all", "instruction": "짧게"}, nil)
	if re.Status != 201 {
		t.Fatalf("regenerate = %d %s", re.Status, re.Raw)
	}
	h.waitRequest(re.Body["id"].(string))
	res := h.do("GET", "/api/contents/"+cid+"/generation-results", nil, nil)
	results := res.Body["items"].([]any)
	if len(results) != 1 {
		t.Fatalf("results = %s", res.Raw)
	}
	rid := results[0].(map[string]any)["id"].(string)
	cur := h.do("GET", "/api/contents/"+cid, nil, nil)
	ap := h.do("POST", "/api/contents/"+cid+"/generation-results/"+rid+"/apply", map[string]any{"edit_version": cur.Body["edit_version"]}, nil)
	if ap.Status != 200 || !strings.Contains(ap.Body["revision"].(map[string]any)["raw_text"].(string), "작은 단서 하나가 모든 것을 바꿨다") {
		t.Fatalf("apply = %d %s", ap.Status, ap.Raw)
	}
	if twice := h.do("POST", "/api/contents/"+cid+"/generation-results/"+rid+"/apply", map[string]any{"edit_version": ap.Body["edit_version"]}, nil); twice.Status != 409 {
		t.Fatalf("second apply = %d", twice.Status)
	}

	plan := h.do("GET", "/api/generation/plan", nil, nil)
	if plan.Status != 200 || plan.Body["active"] != false || len(plan.Body["blockers"].([]any)) == 0 {
		t.Fatalf("plan = %d %s", plan.Status, plan.Raw)
	}
	if u := h.do("GET", "/api/usage", nil, nil); u.Status != 200 || u.Body["counts"].(map[string]any)["ai_text"] != float64(3) {
		t.Fatalf("usage = %s", u.Raw)
	}
	// 자동 운영 시작 (생성 시점이 '버튼'이 아니고 바로 채우기가 켜져 있으면 부족분 판단)
	a := h.do("POST", "/api/automation", map[string]any{"automation_enabled": true}, nil)
	if a.Status != 200 || a.Body["settings"].(map[string]any)["automation_enabled"] != true {
		t.Fatalf("automation = %d %s", a.Status, a.Raw)
	}
}
