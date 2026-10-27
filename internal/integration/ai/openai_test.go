package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yuntyu01/x-automation/internal/integration"
)

// 계약 테스트: 실제 공급자를 부르지 않고, 우리가 보내는 요청 형식과 응답 해석을 검증한다
func TestOpenAIThreadRequestAndParsing(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("bad request %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		draft := `{"title":"제목","body":"본문\u0007입니다","image_prompt":"","tags":["a"," ",""]}`
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "completed",
			"output": []any{
				map[string]any{"type": "reasoning"},
				map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": draft}}},
			},
			"usage": map[string]any{"input_tokens": 100, "output_tokens": 50},
		})
	}))
	defer srv.Close()
	o, _ := NewOpenAI("sk-test", srv.URL, "text-model-x", "img-model-x", nil)
	d, u, err := o.GenerateThread(context.Background(), ThreadRequest{Instructions: "지시", Prompt: "프롬프트"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Body != "본문입니다" || d.Title != "제목" || len(d.Tags) != 1 || u.InputTokens != 100 {
		t.Fatalf("draft = %+v usage %+v", d, u)
	}
	format := got["text"].(map[string]any)["format"].(map[string]any)
	if got["model"] != "text-model-x" || format["type"] != "json_schema" || format["strict"] != true || got["store"] != false {
		t.Fatalf("request = %v", got)
	}
}

func TestOpenAIErrorClassification(t *testing.T) {
	cases := []struct {
		status int
		body   string
		header map[string]string
		class  integration.Class
	}{
		{401, `{"error":{"message":"bad key sk-abcdefghijklmnop","type":"invalid_request_error"}}`, nil, integration.Config},
		{429, `{"error":{"message":"quota","type":"insufficient_quota","code":"insufficient_quota"}}`, nil, integration.Config},
		{429, `{"error":{"message":"slow down"}}`, map[string]string{"retry-after": "7"}, integration.Retryable},
		{400, `{"error":{"message":"policy","code":"content_policy_violation"}}`, nil, integration.Review},
		{503, `{}`, nil, integration.Retryable},
	}
	for _, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for k, v := range c.header {
				w.Header().Set(k, v)
			}
			w.WriteHeader(c.status)
			_, _ = w.Write([]byte(c.body))
		}))
		o, _ := NewOpenAI("sk-test", srv.URL, "m", "m", nil)
		_, _, err := o.GenerateThread(context.Background(), ThreadRequest{Prompt: "p"})
		srv.Close()
		var ie *integration.Error
		if !errors.As(err, &ie) || ie.Class != c.class {
			t.Errorf("%d %s → %v", c.status, c.body, err)
			continue
		}
		if strings.Contains(err.Error(), "sk-abcdefghijklmnop") {
			t.Errorf("secret leaked in error: %v", err)
		}
		if c.header["retry-after"] == "7" && ie.RetryAfter.Seconds() != 7 {
			t.Errorf("retry-after not parsed")
		}
	}
}

func TestOpenAIRefusalAndIncomplete(t *testing.T) {
	for _, resp := range []map[string]any{
		{"status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "refusal", "refusal": "못 해요"}}}}},
		{"status": "incomplete", "incomplete_details": map[string]any{"reason": "max_output_tokens"}, "output": []any{}},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(resp) }))
		o, _ := NewOpenAI("k", srv.URL, "m", "m", nil)
		_, _, err := o.GenerateThread(context.Background(), ThreadRequest{Prompt: "p"})
		srv.Close()
		if integration.ClassOf(err) != integration.Review {
			t.Errorf("want review error, got %v", err)
		}
	}
}

func TestOpenAIImages(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"b64_json": base64.StdEncoding.EncodeToString([]byte("\xff\xd8\xff\xe0fakejpeg"))}}})
	}))
	defer srv.Close()
	o, _ := NewOpenAI("k", srv.URL, "m", "image-model", nil)
	imgs, u, err := o.GenerateImages(context.Background(), ImageRequest{Prompt: "비 오는 골목", Size: "1024x1024", Quality: "medium"})
	if err != nil || len(imgs) != 1 || u.Images != 1 {
		t.Fatalf("images = %v %v", imgs, err)
	}
	if got["output_format"] != "jpeg" || got["model"] != "image-model" || got["quality"] != "medium" {
		t.Fatalf("image request = %v", got)
	}
}

func TestParseDraftRejectsUnknownShape(t *testing.T) {
	if _, err := ParseDraft(`{"title":"a","body":"b","image_prompt":"","tags":[],"run":"rm -rf /"}`); err == nil {
		t.Fatal("unknown fields must be rejected")
	}
	if _, err := ParseDraft(`not json`); err == nil {
		t.Fatal("non-json must be rejected")
	}
}
