package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yuntyu01/x-automation/internal/integration"
)

func TestGeminiStructuredTextAndUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models/gemini-test:generateContent" || r.Header.Get("x-goog-api-key") != "test-key" {
			t.Errorf("incorrect Gemini request")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		cfg := body["generationConfig"].(map[string]any)
		if cfg["responseMimeType"] != "application/json" || cfg["responseJsonSchema"] == nil || body["systemInstruction"] == nil {
			t.Error("missing structured output or system instruction")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"candidates": []any{map[string]any{"finishReason": "STOP", "content": map[string]any{"parts": []any{map[string]any{"text": `{"title":"제목","body":"본문","image_prompt":"사진","tags":["a"]}`}}}}}, "usageMetadata": map[string]any{"promptTokenCount": 10, "totalTokenCount": 18}})
	}))
	defer srv.Close()
	g, err := NewGemini("test-key", srv.URL, "gemini-test", "image-test", nil)
	if err != nil {
		t.Fatal(err)
	}
	d, u, err := g.GenerateThread(context.Background(), ThreadRequest{Prompt: "소설", Instructions: "한국어"})
	if err != nil || d.Body != "본문" || d.ImagePrompt != "사진" || u.InputTokens != 10 || u.OutputTokens != 8 {
		t.Fatalf("draft=%+v usage=%+v err=%v", d, u, err)
	}
}

func TestGeminiImageAndRefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "refuse") {
			_, _ = w.Write([]byte(`{"candidates":[{"finishReason":"SAFETY"}]}`))
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		cfg := body["generationConfig"].(map[string]any)
		if cfg["imageConfig"].(map[string]any)["aspectRatio"] != "1:1" {
			t.Error("expected square image")
		}
		png := []byte("\x89PNG\r\n\x1a\n1234")
		_ = json.NewEncoder(w).Encode(map[string]any{"candidates": []any{map[string]any{"finishReason": "STOP", "content": map[string]any{"parts": []any{map[string]any{"inlineData": map[string]any{"mimeType": "image/png", "data": base64.StdEncoding.EncodeToString(png)}}}}}}})
	}))
	defer srv.Close()
	g, _ := NewGemini("key", srv.URL, "text", "image", nil)
	imgs, u, err := g.GenerateImages(context.Background(), ImageRequest{Prompt: "사진", Size: "1024x1024"})
	if err != nil || len(imgs) != 1 || imgs[0].ContentType != "image/png" || u.Images != 1 {
		t.Fatalf("images=%v usage=%+v err=%v", imgs, u, err)
	}
	_, _, err = g.GenerateThread(context.Background(), ThreadRequest{Model: "refuse", Prompt: "p"})
	if integration.ClassOf(err) != integration.Review {
		t.Fatalf("refusal not surfaced: %v", err)
	}
}

func TestGeminiRedactsProviderError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":{"message":"secret-key","status":"INVALID_ARGUMENT"}}`))
	}))
	defer srv.Close()
	g, _ := NewGemini("secret-key", srv.URL, "text", "", nil)
	_, _, err := g.GenerateThread(context.Background(), ThreadRequest{Prompt: "p"})
	if err == nil || strings.Contains(err.Error(), "secret-key") {
		t.Fatalf("unsafe error: %v", err)
	}
}

func TestGeminiZeroQuotaIsConfigurationError(t *testing.T) {
	for _, status := range []int{402, 429} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"message":"quota limit: 0"}}`))
		}))
		g, _ := NewGemini("key", srv.URL, "text", "image", nil)
		_, _, err := g.GenerateImages(context.Background(), ImageRequest{Prompt: "사진"})
		srv.Close()
		if integration.ClassOf(err) != integration.Config {
			t.Fatalf("%d quota should not retry: %v", status, err)
		}
	}
}

// Google은 잘못된 키를 400으로 돌려준다. 내용 거절이 아니라 설정 오류로 알려야 한다
func TestGeminiInvalidKeyIsConfigError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":400,"message":"API key not valid. Please pass a valid API key.","status":"INVALID_ARGUMENT",
			"details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"API_KEY_INVALID","domain":"googleapis.com"}]}}`))
	}))
	defer srv.Close()
	g, _ := NewGemini("bad-key", srv.URL, "text", "image", nil)
	_, _, err := g.GenerateThread(context.Background(), ThreadRequest{Prompt: "소설"})
	if integration.ClassOf(err) != integration.Config || !strings.Contains(err.Error(), "AI_API_KEY") {
		t.Fatalf("err = %v (class %s)", err, integration.ClassOf(err))
	}
}
