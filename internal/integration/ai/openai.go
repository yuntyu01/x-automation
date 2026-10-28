package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/yuntyu01/x-automation/internal/integration"
	"github.com/yuntyu01/x-automation/internal/logx"
)

// OpenAI는 Responses API(구조화 출력)와 Images API를 쓰는 실제 어댑터다 (docs/INTEGRATIONS.md).
type OpenAI struct {
	apiKey     string
	baseURL    string
	textModel  string
	imageModel string
	http       *http.Client
	googleAuth bool
}

func NewOpenAI(apiKey, baseURL, textModel, imageModel string, hc *http.Client) (*OpenAI, error) {
	if apiKey == "" {
		return nil, integration.ErrNotConfigured("AI")
	}
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	if hc == nil {
		hc = &http.Client{Timeout: 3 * time.Minute}
	}
	return &OpenAI{apiKey: apiKey, baseURL: strings.TrimRight(baseURL, "/"), textModel: textModel, imageModel: imageModel, http: hc}, nil
}

func (o *OpenAI) Name() string { return "openai" }

// ThreadSchema는 생성 결과의 구조다. 모든 필드가 필수이고 추가 필드는 받지 않는다 (strict).
var ThreadSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"title":        map[string]any{"type": "string", "description": "관리용 짧은 제목"},
		"body":         map[string]any{"type": "string", "description": "게시할 전체 글 (분할 전)"},
		"image_prompt": map[string]any{"type": "string", "description": "이미지를 만든다면 쓸 장면 묘사. 필요 없으면 빈 문자열"},
		"tags":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
	},
	"required":             []string{"title", "body", "image_prompt", "tags"},
	"additionalProperties": false,
}

func (o *OpenAI) GenerateThread(ctx context.Context, req ThreadRequest) (ThreadDraft, Usage, error) {
	model := req.Model
	if model == "" {
		model = o.textModel
	}
	if model == "" {
		return ThreadDraft{}, Usage{}, &integration.Error{Class: integration.Config, Provider: "AI", Op: "text", Message: "텍스트 모델이 지정되지 않았어요 (AI_TEXT_MODEL)"}
	}
	maxOut := req.MaxOutput
	if maxOut <= 0 {
		maxOut = 2000
	}
	body := map[string]any{
		"model":             model,
		"instructions":      req.Instructions,
		"input":             req.Prompt,
		"max_output_tokens": maxOut,
		"store":             false,
		"text": map[string]any{"format": map[string]any{
			"type": "json_schema", "name": "thread_draft", "strict": true, "schema": ThreadSchema,
		}},
	}
	var resp struct {
		Status            string `json:"status"`
		IncompleteDetails *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type    string `json:"type"`
				Text    string `json:"text"`
				Refusal string `json:"refusal"`
			} `json:"content"`
		} `json:"output"`
		Usage struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := o.post(ctx, "/v1/responses", "text", body, &resp); err != nil {
		return ThreadDraft{}, Usage{}, err
	}
	u := Usage{InputTokens: resp.Usage.InputTokens, OutputTokens: resp.Usage.OutputTokens}
	var text strings.Builder
	for _, item := range resp.Output {
		if item.Type != "message" {
			continue
		}
		for _, c := range item.Content {
			switch c.Type {
			case "output_text":
				text.WriteString(c.Text)
			case "refusal":
				return ThreadDraft{}, u, &integration.Error{Class: integration.Review, Provider: "AI", Op: "text", Message: "AI가 요청을 거절했어요: " + truncate(c.Refusal, 200)}
			}
		}
	}
	if resp.Status == "incomplete" {
		reason := ""
		if resp.IncompleteDetails != nil {
			reason = resp.IncompleteDetails.Reason
		}
		return ThreadDraft{}, u, &integration.Error{Class: integration.Review, Provider: "AI", Op: "text", Message: "AI 응답이 중간에 끊겼어요 (" + reason + "). 길이를 줄여 다시 시도하세요"}
	}
	if resp.Status != "" && resp.Status != "completed" {
		return ThreadDraft{}, u, &integration.Error{Class: integration.Retryable, Provider: "AI", Op: "text", Message: "AI 응답 상태가 " + resp.Status + "예요"}
	}
	draft, err := ParseDraft(text.String())
	return draft, u, err
}

// ParseDraft는 모델 출력을 스키마대로 검증한다. 모델이 쓴 문장은 텍스트로만 쓴다 (명령으로 실행하지 않는다).
func ParseDraft(raw string) (ThreadDraft, error) {
	var d ThreadDraft
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return d, &integration.Error{Class: integration.Retryable, Provider: "AI", Op: "parse", Message: "AI 응답이 약속한 형식이 아니에요"}
	}
	d.Title = strings.TrimSpace(stripControl(d.Title))
	d.Body = strings.TrimSpace(stripControl(d.Body))
	d.ImagePrompt = strings.TrimSpace(stripControl(d.ImagePrompt))
	if d.Body == "" {
		return d, &integration.Error{Class: integration.Retryable, Provider: "AI", Op: "parse", Message: "AI가 빈 글을 돌려줬어요"}
	}
	if len([]rune(d.Body)) > 20000 {
		return d, &integration.Error{Class: integration.Review, Provider: "AI", Op: "parse", Message: "AI가 너무 긴 글을 돌려줬어요"}
	}
	if len([]rune(d.Title)) > 120 {
		d.Title = string([]rune(d.Title)[:120])
	}
	var tags []string
	for _, t := range d.Tags {
		t = strings.TrimSpace(stripControl(t))
		if t != "" && len(tags) < 10 && len([]rune(t)) <= 30 {
			tags = append(tags, t)
		}
	}
	d.Tags = tags
	return d, nil
}

func stripControl(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f || (r >= 0x200B && r <= 0x200F && r != 0x200D) {
			return -1
		}
		return r
	}, s)
}

func (o *OpenAI) GenerateImages(ctx context.Context, req ImageRequest) ([]Image, Usage, error) {
	model := req.Model
	if model == "" {
		model = o.imageModel
	}
	if model == "" {
		return nil, Usage{}, &integration.Error{Class: integration.Config, Provider: "AI", Op: "image", Message: "이미지 모델이 지정되지 않았어요 (AI_IMAGE_MODEL)"}
	}
	n := req.Count
	if n <= 0 {
		n = 1
	}
	body := map[string]any{
		"model": model, "prompt": req.Prompt, "n": n,
		// X 첨부 한도(5MB) 안에 들어오게 JPEG로 받는다
		"output_format": "jpeg", "output_compression": 85,
	}
	if req.Size != "" {
		body["size"] = req.Size
	}
	if req.Quality != "" {
		body["quality"] = req.Quality
	}
	var resp struct {
		Data []struct {
			B64JSON string `json:"b64_json"`
		} `json:"data"`
		Usage struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := o.post(ctx, "/v1/images/generations", "image", body, &resp); err != nil {
		return nil, Usage{}, err
	}
	u := Usage{InputTokens: resp.Usage.InputTokens, OutputTokens: resp.Usage.OutputTokens, Images: len(resp.Data)}
	var out []Image
	for _, d := range resp.Data {
		b, err := base64.StdEncoding.DecodeString(d.B64JSON)
		if err != nil || len(b) == 0 {
			return nil, u, &integration.Error{Class: integration.Retryable, Provider: "AI", Op: "image", Message: "이미지 응답을 읽을 수 없어요"}
		}
		out = append(out, Image{Data: b, ContentType: http.DetectContentType(b)})
	}
	if len(out) == 0 {
		return nil, u, &integration.Error{Class: integration.Retryable, Provider: "AI", Op: "image", Message: "AI가 이미지를 돌려주지 않았어요"}
	}
	return out, u, nil
}

func (o *OpenAI) post(ctx context.Context, path, op string, body any, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	if o.googleAuth {
		req.Header.Set("x-goog-api-key", o.apiKey)
	} else {
		req.Header.Set("Authorization", "Bearer "+o.apiKey)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := o.http.Do(req)
	if err != nil {
		// 요청이 나간 뒤 끊겼을 수 있어 과금 여부가 불명확하다. 재시도 상한 안에서만 다시 한다
		var ne net.Error
		msg := "AI 서버에 연결하지 못했어요"
		if errors.As(err, &ne) && ne.Timeout() {
			msg = "AI 응답 시간이 초과됐어요 (요금이 청구됐을 수 있어요)"
		}
		return &integration.Error{Class: integration.Retryable, Provider: "AI", Op: op, Message: msg, Err: errors.New(logx.Redact(err.Error()))}
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		if err := json.Unmarshal(data, out); err != nil {
			return &integration.Error{Class: integration.Retryable, Provider: "AI", Op: op, Message: "AI 응답을 읽을 수 없어요"}
		}
		return nil
	}
	var apiErr struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(data, &apiErr)
	googleNoQuota := o.googleAuth && (res.StatusCode == 402 || (res.StatusCode == 429 && strings.Contains(apiErr.Error.Message, "limit: 0")))
	if o.googleAuth {
		// 공급자가 API 키나 요청 내용을 오류에 되돌려도 로그에 남기지 않는다.
		apiErr.Error.Message = ""
	}
	e := &integration.Error{Provider: "AI", Op: op, HTTPStatus: res.StatusCode, RequestID: res.Header.Get("x-request-id"),
		Err: errors.New(logx.Redact(truncate(apiErr.Error.Message, 300)))}
	switch {
	case googleNoQuota:
		e.Class, e.Message = integration.Config, "Gemini 사용 한도 또는 결제 잔액이 부족해요 (Google AI Studio 확인)"
	case res.StatusCode == 401:
		e.Class, e.Message = integration.Config, "AI 키가 올바르지 않아요 (AI_API_KEY 확인)"
	case res.StatusCode == 403:
		e.Class, e.Message = integration.Config, "AI 키에 이 기능 권한이 없어요"
	case res.StatusCode == 404:
		e.Class, e.Message = integration.Config, "AI 모델을 찾을 수 없어요 (모델 이름 확인)"
	case res.StatusCode == 429 && (apiErr.Error.Code == "insufficient_quota" || apiErr.Error.Type == "insufficient_quota"):
		e.Class, e.Message = integration.Config, "AI 계정 잔액·한도가 부족해요 (공급자 콘솔 확인)"
	case res.StatusCode == 429:
		e.Class, e.Message = integration.Retryable, "AI 호출 한도에 걸렸어요. 잠시 후 다시 시도해요"
		if s, err := strconv.Atoi(res.Header.Get("retry-after")); err == nil {
			e.RetryAfter = time.Duration(s) * time.Second
		}
	case res.StatusCode == 400:
		e.Class = integration.Review
		e.Message = "AI가 요청을 거부했어요"
		if apiErr.Error.Code == "content_policy_violation" || strings.Contains(apiErr.Error.Code, "moderation") {
			e.Message = "AI 정책에 맞지 않아 거부됐어요. 프롬프트를 바꿔 주세요"
		} else if apiErr.Error.Message != "" {
			e.Message = fmt.Sprintf("AI가 요청을 거부했어요: %s", truncate(logx.Redact(apiErr.Error.Message), 160))
		}
	case res.StatusCode >= 500:
		e.Class, e.Message = integration.Retryable, "AI 서버 오류예요. 잠시 후 다시 시도해요"
	default:
		e.Class, e.Message = integration.Retryable, "AI 요청이 실패했어요"
	}
	return e
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
