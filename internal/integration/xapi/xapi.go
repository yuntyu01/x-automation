// Package xapi는 X API v2(OAuth 2.0 사용자 토큰) 호출을 추상화한다.
// 자기 글의 본문·답글 게시와 미디어 업로드만 다룬다. 타인 글 답글·좋아요·팔로우는 구현하지 않는다.
package xapi

import (
	"context"
	"time"

	"github.com/yuntyu01/x-automation/internal/integration"
)

type Token struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	Scopes       []string
}

type Account struct {
	ID               string `json:"id"`
	Username         string `json:"username"`
	Name             string `json:"name"`
	SubscriptionType string `json:"subscription_type"`
}

type PostRequest struct {
	Text       string
	MediaIDs   []string
	ReplyToID  string
	MadeWithAI bool // AI 생성 이미지가 붙은 게시 공개 표시
}

type Post struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// Client는 X API 호출이다. 토큰 갱신·저장은 publish 패키지가 맡는다.
type Client interface {
	AuthorizeURL(state, codeChallenge string) string
	ExchangeCode(ctx context.Context, code, verifier string) (Token, error)
	Refresh(ctx context.Context, refreshToken string) (Token, error)
	Me(ctx context.Context, accessToken string) (Account, error)
	UploadImage(ctx context.Context, accessToken string, data []byte, contentType string) (string, error)
	CreatePost(ctx context.Context, accessToken string, req PostRequest) (Post, error)
	Revoke(ctx context.Context, token string) error
	Name() string
}

// Unconfigured는 실제 연동 모드인데 X 앱 값(X_CLIENT_ID 등)이 없을 때 쓴다. 성공한 척하지 않고 설정 오류를 돌려준다.
type Unconfigured struct{}

func errUnconfigured() error { return integration.ErrNotConfigured("x") }

func (Unconfigured) AuthorizeURL(string, string) string { return "" }
func (Unconfigured) ExchangeCode(context.Context, string, string) (Token, error) {
	return Token{}, errUnconfigured()
}
func (Unconfigured) Refresh(context.Context, string) (Token, error) {
	return Token{}, errUnconfigured()
}
func (Unconfigured) Me(context.Context, string) (Account, error) { return Account{}, errUnconfigured() }
func (Unconfigured) UploadImage(context.Context, string, []byte, string) (string, error) {
	return "", errUnconfigured()
}
func (Unconfigured) CreatePost(context.Context, string, PostRequest) (Post, error) {
	return Post{}, errUnconfigured()
}
func (Unconfigured) Revoke(context.Context, string) error { return errUnconfigured() }
func (Unconfigured) Name() string                         { return "unconfigured" }
