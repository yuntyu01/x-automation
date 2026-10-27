package xapi

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/yuntyu01/x-automation/internal/integration"
)

// Memory는 목업 모드의 X 대역이다. 실제로 아무것도 게시하지 않고 메모리에만 남긴다 (화면에 "목업" 표시).
// 인가 단계도 X로 가지 않고 앱의 콜백 주소로 바로 돌아온다.
type Memory struct {
	RedirectURI string

	mu    sync.Mutex
	seq   int64
	posts []MemPost
	Fail  func(op string, req PostRequest) error // 테스트에서 실패를 주입한다
}

type MemPost struct {
	ID        string
	Text      string
	ReplyTo   string
	MediaIDs  []string
	CreatedAt time.Time
}

func (m *Memory) Name() string { return "mock" }

func (m *Memory) next() string {
	m.seq++
	return fmt.Sprintf("19%017d", m.seq)
}

func (m *Memory) AuthorizeURL(state, _ string) string {
	return m.RedirectURI + "?" + url.Values{"state": {state}, "code": {"mock-code"}}.Encode()
}

func (m *Memory) token() Token {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.next()
	return Token{AccessToken: "mock-access-" + id, RefreshToken: "mock-refresh-" + id, ExpiresAt: time.Now().Add(2 * time.Hour), Scopes: Scopes}
}

func (m *Memory) ExchangeCode(context.Context, string, string) (Token, error) { return m.token(), nil }
func (m *Memory) Refresh(context.Context, string) (Token, error)              { return m.token(), nil }
func (m *Memory) Revoke(context.Context, string) error                        { return nil }

func (m *Memory) Me(context.Context, string) (Account, error) {
	return Account{ID: "1000000000000000001", Username: "mock_account", Name: "목업 계정", SubscriptionType: "None"}, nil
}

func (m *Memory) UploadImage(_ context.Context, _ string, data []byte, _ string) (string, error) {
	if m.Fail != nil {
		if err := m.Fail("media", PostRequest{}); err != nil {
			return "", err
		}
	}
	if len(data) == 0 {
		return "", &integration.Error{Class: integration.Review, Provider: "x", Op: "media.upload", Message: "빈 파일이에요"}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return "media-" + m.next(), nil
}

func (m *Memory) CreatePost(_ context.Context, _ string, req PostRequest) (Post, error) {
	if m.Fail != nil {
		if err := m.Fail("post", req); err != nil {
			return Post{}, err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if req.ReplyToID != "" {
		found := false
		for _, p := range m.posts {
			if p.ID == req.ReplyToID {
				found = true
			}
		}
		if !found {
			return Post{}, &integration.Error{Class: integration.Review, Provider: "x", Op: "tweets.create", HTTPStatus: 400, Message: "답글 대상 게시물이 없어요"}
		}
	}
	id := m.next()
	m.posts = append(m.posts, MemPost{ID: id, Text: req.Text, ReplyTo: req.ReplyToID, MediaIDs: req.MediaIDs, CreatedAt: time.Now()})
	return Post{ID: id, Text: req.Text}, nil
}

// Posts는 지금까지 "게시"한 목록이다 (테스트·확인용).
func (m *Memory) Posts() []MemPost {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]MemPost(nil), m.posts...)
}
