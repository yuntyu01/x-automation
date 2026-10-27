// Package xapi는 X API v2(OAuth 2.0 사용자 토큰) 호출을 추상화한다.
// 자기 글의 본문·답글 게시와 미디어 업로드만 다룬다. 타인 글 답글·좋아요·팔로우는 구현하지 않는다.
package xapi

import (
	"context"
	"time"
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
	Text      string
	MediaIDs  []string
	ReplyToID string
}

type Post struct {
	ID   string
	Text string
}

// Client는 X API 호출이다. 토큰 갱신·저장은 publish 패키지가 맡는다.
type Client interface {
	AuthorizeURL(state, codeChallenge string) string
	ExchangeCode(ctx context.Context, code, verifier string) (Token, error)
	Refresh(ctx context.Context, refreshToken string) (Token, error)
	Me(ctx context.Context, accessToken string) (Account, error)
	UploadImage(ctx context.Context, accessToken string, data []byte, contentType string) (string, error)
	CreatePost(ctx context.Context, accessToken string, req PostRequest) (Post, error)
	Name() string
}
