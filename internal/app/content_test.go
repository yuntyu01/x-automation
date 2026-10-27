package app_test

import (
	"strings"
	"testing"
)

func TestContentAPIFlow(t *testing.T) {
	h := newHarness(t)
	h.login()
	raw := "첫 문단입니다. 조금 길게 써 봅니다.\n\n둘째 문단입니다. 답글로 이어집니다."
	c := h.do("POST", "/api/contents", map[string]any{"raw_text": raw, "category": "소설"}, nil)
	if c.Status != 201 {
		t.Fatalf("create = %d %s", c.Status, c.Raw)
	}
	id := c.Body["id"].(string)
	ver := c.Body["edit_version"]
	rev := c.Body["revision"].(map[string]any)
	if len(rev["parts"].([]any)) != 2 {
		t.Fatalf("parts = %v", rev["parts"])
	}
	// 미리보기는 저장하지 않는다
	pv := h.do("POST", "/api/contents/split-preview", map[string]any{"raw_text": raw, "split_mode": "overflow", "split_options": map[string]any{"numbering": false, "max_replies": 0}, "to_manual": true}, nil)
	if pv.Status != 200 || pv.Body["manual_text"] == nil {
		t.Fatalf("preview = %d %s", pv.Status, pv.Raw)
	}
	// 자동 저장 충돌: 서버 현재 상태를 같이 준다
	ok := h.do("PUT", "/api/contents/"+id, map[string]any{"edit_version": ver, "title": "t", "category": "", "tags": []string{}, "raw_text": raw + " 추가", "split_mode": "two_parts", "split_options": map[string]any{"numbering": false, "max_replies": 0}, "image_mode": "none"}, nil)
	if ok.Status != 200 {
		t.Fatalf("save = %d %s", ok.Status, ok.Raw)
	}
	stale := h.do("PUT", "/api/contents/"+id, map[string]any{"edit_version": ver, "title": "x", "category": "", "tags": []string{}, "raw_text": "x", "split_mode": "two_parts", "split_options": map[string]any{"numbering": false, "max_replies": 0}, "image_mode": "none"}, nil)
	if stale.Status != 409 || stale.Body["current"] == nil {
		t.Fatalf("conflict = %d %s", stale.Status, stale.Raw)
	}
	// 승인
	cur := ok.Body["revision"].(map[string]any)["id"]
	ap := h.do("POST", "/api/contents/"+id+"/approve", map[string]any{"revision_id": cur}, nil)
	if ap.Status != 200 || ap.Body["approved_revision_id"] != cur {
		t.Fatalf("approve = %d %s", ap.Status, ap.Raw)
	}
	// 목록·검색
	list := h.do("GET", "/api/contents?q="+"%EB%91%98%EC%A7%B8", nil, nil) // "둘째"
	if list.Status != 200 || list.Body["total"] != float64(1) {
		t.Fatalf("list = %d %s", list.Status, list.Raw)
	}
	if bad := h.do("GET", "/api/contents?stage=nope", nil, nil); bad.Status != 422 {
		t.Fatalf("bad stage = %d", bad.Status)
	}
	// 가져오기 미리보기·등록
	csv := "title,text\n가져온 글,\"한 문단.\"\n,\n"
	parse := h.do("POST", "/api/contents/import/parse", map[string]any{"format": "csv", "data": csv}, nil)
	if parse.Status != 200 || parse.Body["row_count"] != float64(2) {
		t.Fatalf("parse = %d %s", parse.Status, parse.Raw)
	}
	mapping := parse.Body["mapping"]
	commit := h.do("POST", "/api/contents/import/commit", map[string]any{"format": "csv", "data": csv, "mapping": mapping, "defaults": map[string]any{"split_mode": "two_parts", "category": ""}}, nil)
	if commit.Status != 200 || commit.Body["imported"] != float64(1) {
		t.Fatalf("commit = %d %s", commit.Status, commit.Raw)
	}
	// 내보내기는 첨부 파일이고 캐시하지 않는다
	exp := h.do("GET", "/api/contents/export?format=csv", nil, nil)
	if exp.Status != 200 || !strings.Contains(exp.Header.Get("Content-Disposition"), "attachment") || exp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("export = %d %v", exp.Status, exp.Header)
	}
	if strings.Contains(string(exp.Raw), "csrf") || strings.Contains(string(exp.Raw), "token") {
		t.Fatal("export must not contain credentials")
	}
	tpl := h.do("GET", "/api/contents/import/template?format=json", nil, nil)
	if tpl.Status != 200 || !strings.Contains(string(tpl.Raw), "(예시)") {
		t.Fatalf("template = %d", tpl.Status)
	}
	// 프롬프트
	p := h.do("POST", "/api/prompts", map[string]any{"name": "썰", "body": "주제 {{topic}}", "variables": []any{map[string]any{"name": "topic", "label": "주제", "type": "text", "required": true, "default": ""}}, "defaults": map[string]any{}}, nil)
	if p.Status != 201 {
		t.Fatalf("prompt = %d %s", p.Status, p.Raw)
	}
	r := h.do("POST", "/api/prompts/"+p.Body["id"].(string)+"/render", map[string]any{"values": map[string]string{}}, nil)
	if r.Status != 422 {
		t.Fatalf("render missing required = %d %s", r.Status, r.Raw)
	}
	// 예시 플래그는 일반 등록으로 붙일 수 없다
	fake := h.do("POST", "/api/prompts", map[string]any{"name": "x", "body": "b", "is_example": true}, nil)
	if fake.Status != 201 || fake.Body["is_example"] != false {
		t.Fatalf("is_example spoof = %s", fake.Raw)
	}
}
