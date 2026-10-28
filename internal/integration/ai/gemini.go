package ai

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"

	"github.com/yuntyu01/x-automation/internal/integration"
)

// Gemini calls the Google Gemini generateContent API using a project API key.
type Gemini struct{ client *OpenAI }

func NewGemini(key, baseURL, textModel, imageModel string, hc *http.Client) (*Gemini, error) {
	if baseURL == "" {
		baseURL = "https://generativelanguage.googleapis.com"
	}
	c, err := NewOpenAI(key, baseURL, textModel, imageModel, hc)
	if err != nil {
		return nil, err
	}
	c.googleAuth = true
	return &Gemini{client: c}, nil
}

func (g *Gemini) Name() string { return "gemini" }

type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text       string `json:"text"`
				Thought    bool   `json:"thought"`
				InlineData struct {
					MIMEType string `json:"mimeType"`
					Data     string `json:"data"`
				} `json:"inlineData"`
			} `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount int64 `json:"promptTokenCount"`
		TotalTokenCount  int64 `json:"totalTokenCount"`
	} `json:"usageMetadata"`
}

func (r geminiResponse) usage() Usage {
	out := r.UsageMetadata.TotalTokenCount - r.UsageMetadata.PromptTokenCount
	if out < 0 {
		out = 0
	}
	return Usage{InputTokens: r.UsageMetadata.PromptTokenCount, OutputTokens: out}
}

func (g *Gemini) generate(ctx context.Context, model, op string, body any) (geminiResponse, error) {
	var resp geminiResponse
	if model == "" {
		return resp, &integration.Error{Class: integration.Config, Provider: "AI", Op: op, Message: "Gemini 모델이 지정되지 않았어요"}
	}
	err := g.client.post(ctx, "/v1beta/models/"+url.PathEscape(model)+":generateContent", op, body, &resp)
	if err != nil {
		return resp, err
	}
	if len(resp.Candidates) == 0 {
		return resp, &integration.Error{Class: integration.Review, Provider: "AI", Op: op, Message: "Gemini가 결과를 반환하지 않았어요"}
	}
	reason := resp.Candidates[0].FinishReason
	if reason != "" && reason != "STOP" {
		return resp, &integration.Error{Class: integration.Review, Provider: "AI", Op: op, Message: "Gemini 생성이 완료되지 않았어요 (" + reason + ")"}
	}
	return resp, nil
}

func (g *Gemini) GenerateThread(ctx context.Context, req ThreadRequest) (ThreadDraft, Usage, error) {
	model := req.Model
	if model == "" {
		model = g.client.textModel
	}
	max := req.MaxOutput
	if max <= 0 {
		max = 2000
	}
	body := map[string]any{
		"contents":         []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": req.Prompt}}}},
		"generationConfig": map[string]any{"responseMimeType": "application/json", "responseJsonSchema": ThreadSchema, "maxOutputTokens": max},
	}
	if req.Instructions != "" {
		body["systemInstruction"] = map[string]any{"parts": []any{map[string]any{"text": req.Instructions}}}
	}
	resp, err := g.generate(ctx, model, "text", body)
	u := resp.usage()
	if err != nil {
		return ThreadDraft{}, u, err
	}
	var text strings.Builder
	for _, part := range resp.Candidates[0].Content.Parts {
		if !part.Thought {
			text.WriteString(part.Text)
		}
	}
	draft, err := ParseDraft(text.String())
	return draft, u, err
}

func (g *Gemini) GenerateImages(ctx context.Context, req ImageRequest) ([]Image, Usage, error) {
	model := req.Model
	if model == "" {
		model = g.client.imageModel
	}
	count := req.Count
	if count <= 0 {
		count = 1
	}
	ratio := "1:1"
	switch req.Size {
	case "", "1024x1024":
	case "1536x1024":
		ratio = "3:2"
	case "1024x1536":
		ratio = "2:3"
	default:
		return nil, Usage{}, &integration.Error{Class: integration.Config, Provider: "AI", Op: "image", Message: "Gemini 이미지 크기는 1024x1024, 1536x1024 또는 1024x1536으로 지정해 주세요"}
	}
	var images []Image
	var total Usage
	for i := 0; i < count; i++ {
		body := map[string]any{
			"contents":         []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": req.Prompt}}}},
			"generationConfig": map[string]any{"responseModalities": []string{"IMAGE"}, "imageConfig": map[string]any{"aspectRatio": ratio}},
		}
		resp, err := g.generate(ctx, model, "image", body)
		u := resp.usage()
		total.InputTokens += u.InputTokens
		total.OutputTokens += u.OutputTokens
		if err != nil {
			return nil, total, err
		}
		for _, part := range resp.Candidates[0].Content.Parts {
			if part.Thought || part.InlineData.Data == "" {
				continue
			}
			data, err := base64.StdEncoding.DecodeString(part.InlineData.Data)
			if err != nil || len(data) == 0 {
				return nil, total, &integration.Error{Class: integration.Retryable, Provider: "AI", Op: "image", Message: "Gemini 이미지 응답을 읽을 수 없어요"}
			}
			kind := http.DetectContentType(data)
			if !strings.HasPrefix(kind, "image/") {
				return nil, total, &integration.Error{Class: integration.Retryable, Provider: "AI", Op: "image", Message: "Gemini가 이미지가 아닌 데이터를 반환했어요"}
			}
			images = append(images, Image{Data: data, ContentType: kind})
			break
		}
		if len(images) != i+1 {
			return nil, total, &integration.Error{Class: integration.Review, Provider: "AI", Op: "image", Message: "Gemini가 이미지를 반환하지 않았어요"}
		}
		total.Images = len(images)
	}
	return images, total, nil
}
