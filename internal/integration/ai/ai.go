// Package ai는 텍스트·이미지 생성 공급자를 추상화한다.
// 모델 출력은 구조화 스키마로 검증하고 텍스트로만 취급한다 (명령으로 실행하지 않는다).
package ai

import "context"

// Usage는 공급자가 돌려준 사용량이다. 비용은 요율표로 따로 추정한다.
type Usage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	Images       int   `json:"images"`
}

type ThreadRequest struct {
	Model        string
	Instructions string // 시스템 지시 (출력 형식·금지 사항)
	Prompt       string // 사용자 프롬프트 (변수 치환 완료)
	MaxOutput    int
}

// ThreadDraft는 생성된 묶음 초안이다. 분할은 앱의 분할 규칙이 맡는다.
type ThreadDraft struct {
	Title       string   `json:"title"`
	Body        string   `json:"body"`
	ImagePrompt string   `json:"image_prompt"`
	Tags        []string `json:"tags"`
}

type ImageRequest struct {
	Model   string
	Prompt  string
	Size    string
	Quality string
	Count   int
}

type Image struct {
	Data        []byte
	ContentType string
}

type Provider interface {
	GenerateThread(ctx context.Context, req ThreadRequest) (ThreadDraft, Usage, error)
	GenerateImages(ctx context.Context, req ImageRequest) ([]Image, Usage, error)
	Name() string
}
