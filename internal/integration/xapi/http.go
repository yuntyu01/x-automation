package xapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/yuntyu01/x-automation/internal/integration"
)

// 실제 X API v2 클라이언트. 엔드포인트·형식은 docs/INTEGRATIONS.md (공식 OpenAPI 명세에서 확인).
//
// 오류 분류 (지시서 12.2·12.3)
//   - 게시 요청을 보낸 뒤 응답이 끊기거나 5xx: 실제로 올라갔는지 모른다 → Unknown (자동 재게시 금지)
//   - 연결 자체가 안 됨(요청 전 실패), 429: 재시도 가능 (429는 x-rate-limit-reset을 따른다)
//   - 401: 토큰 문제 → 설정 필요 (한 번 갱신 후에도 401이면 다시 연결)
//   - 400·403: 내용·권한 문제 → 검토 필요 (무한 재시도 안 함)

const (
	DefaultAPIBase      = "https://api.x.com"
	DefaultAuthorizeURL = "https://x.com/i/oauth2/authorize"
	DefaultTokenURL     = "https://api.x.com/2/oauth2/token"
	DefaultRevokeURL    = "https://api.x.com/2/oauth2/revoke"
)

// Scopes는 앱이 요청하는 권한이다 (미디어 업로드는 media.write, 갱신 토큰은 offline.access).
var Scopes = []string{"tweet.read", "tweet.write", "users.read", "media.write", "offline.access"}

type HTTPConfig struct {
	ClientID, ClientSecret, RedirectURI string
	APIBase, AuthorizeURL, TokenURL     string
	HTTP                                *http.Client
}

type HTTP struct{ c HTTPConfig }

func NewHTTP(c HTTPConfig) *HTTP {
	if c.APIBase == "" {
		c.APIBase = DefaultAPIBase
	}
	if c.AuthorizeURL == "" {
		c.AuthorizeURL = DefaultAuthorizeURL
	}
	if c.TokenURL == "" {
		c.TokenURL = DefaultTokenURL
	}
	if c.HTTP == nil {
		c.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	c.APIBase = strings.TrimRight(c.APIBase, "/")
	return &HTTP{c: c}
}

func (h *HTTP) Name() string { return "x" }

func (h *HTTP) AuthorizeURL(state, challenge string) string {
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {h.c.ClientID},
		"redirect_uri":          {h.c.RedirectURI},
		"scope":                 {strings.Join(Scopes, " ")},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	return h.c.AuthorizeURL + "?" + q.Encode()
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
}

func (h *HTTP) token(ctx context.Context, form url.Values, op string) (Token, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.c.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if h.c.ClientSecret != "" {
		req.SetBasicAuth(url.QueryEscape(h.c.ClientID), url.QueryEscape(h.c.ClientSecret)) // 기밀 클라이언트
	} else {
		form.Set("client_id", h.c.ClientID)
	}
	res, err := h.c.HTTP.Do(req)
	if err != nil {
		return Token{}, &integration.Error{Class: integration.Retryable, Provider: "x", Op: op, Message: "X에 연결할 수 없어요", Err: err}
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		var e struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		_ = json.Unmarshal(body, &e)
		class := integration.Config
		if res.StatusCode >= 500 || res.StatusCode == 429 {
			class = integration.Retryable
		}
		msg := "X 인증에 실패했어요. 다시 연결해 주세요"
		if e.Error == "invalid_grant" {
			msg = "X 연결이 만료됐거나 해제됐어요. 다시 연결해 주세요"
		}
		return Token{}, &integration.Error{Class: class, Provider: "x", Op: op, HTTPStatus: res.StatusCode, Message: msg}
	}
	var t tokenResponse
	if err := json.Unmarshal(body, &t); err != nil || t.AccessToken == "" {
		return Token{}, &integration.Error{Class: integration.Config, Provider: "x", Op: op, Message: "X 토큰 응답을 읽을 수 없어요"}
	}
	return Token{AccessToken: t.AccessToken, RefreshToken: t.RefreshToken, ExpiresAt: time.Now().Add(time.Duration(t.ExpiresIn) * time.Second),
		Scopes: strings.Fields(t.Scope)}, nil
}

func (h *HTTP) ExchangeCode(ctx context.Context, code, verifier string) (Token, error) {
	return h.token(ctx, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {h.c.RedirectURI},
		"code_verifier": {verifier}}, "oauth.exchange")
}

func (h *HTTP) Refresh(ctx context.Context, refreshToken string) (Token, error) {
	return h.token(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}}, "oauth.refresh")
}

// Revoke는 토큰을 해제한다 (연결 해제 때. 실패해도 앱에서는 지운다).
func (h *HTTP) Revoke(ctx context.Context, token string) error {
	u := strings.Replace(h.c.TokenURL, "/oauth2/token", "/oauth2/revoke", 1)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(url.Values{"token": {token}}.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if h.c.ClientSecret != "" {
		req.SetBasicAuth(url.QueryEscape(h.c.ClientID), url.QueryEscape(h.c.ClientSecret))
	}
	res, err := h.c.HTTP.Do(req)
	if err != nil {
		return err
	}
	res.Body.Close()
	return nil
}

// problem은 X 오류 응답(Problem JSON 또는 errors 배열)이다.
type problem struct {
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Type   string `json:"type"`
	Errors []struct {
		Message string `json:"message"`
		Detail  string `json:"detail"`
	} `json:"errors"`
}

func (p problem) text() string {
	for _, s := range []string{p.Detail, p.Title} {
		if s != "" {
			return s
		}
	}
	for _, e := range p.Errors {
		if e.Detail != "" {
			return e.Detail
		}
		if e.Message != "" {
			return e.Message
		}
	}
	return ""
}

// mayHaveBeenSent는 요청이 서버에 닿았을 수 있는지다. 연결 거부·DNS 실패처럼 보내기 전에 실패한 경우만 false.
// 시간 초과·연결 끊김·취소는 서버가 처리했을 수 있으므로 true (게시 요청이면 결과 불명확).
func mayHaveBeenSent(err error) bool {
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" {
		return false
	}
	var dns *net.DNSError
	return !errors.As(err, &dns)
}

func (h *HTTP) do(ctx context.Context, req *http.Request, op string, sideEffect bool, out any) error {
	res, err := h.c.HTTP.Do(req.WithContext(ctx))
	if err != nil {
		if sideEffect && mayHaveBeenSent(err) {
			return &integration.Error{Class: integration.Unknown, Provider: "x", Op: op,
				Message: "요청을 보낸 뒤 응답을 받지 못했어요. X에 실제로 올라갔는지 확인이 필요해요", Err: err}
		}
		return &integration.Error{Class: integration.Retryable, Provider: "x", Op: op, Message: "X에 연결할 수 없어요", Err: err}
	}
	defer res.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		if readErr != nil || json.Unmarshal(body, out) != nil {
			if sideEffect {
				return &integration.Error{Class: integration.Unknown, Provider: "x", Op: op, HTTPStatus: res.StatusCode,
					Message: "성공 응답을 읽지 못했어요. X에 올라갔는지 확인이 필요해요"}
			}
			return &integration.Error{Class: integration.Retryable, Provider: "x", Op: op, HTTPStatus: res.StatusCode, Message: "X 응답을 읽지 못했어요"}
		}
		return nil
	}
	var p problem
	_ = json.Unmarshal(body, &p)
	e := &integration.Error{Provider: "x", Op: op, HTTPStatus: res.StatusCode, RequestID: res.Header.Get("x-transaction-id")}
	detail := p.text()
	switch {
	case res.StatusCode == http.StatusTooManyRequests:
		e.Class, e.Message = integration.Retryable, "X 호출 한도에 걸렸어요. 한도가 풀리면 이어서 해요"
		if reset, err := strconv.ParseInt(res.Header.Get("x-rate-limit-reset"), 10, 64); err == nil {
			e.RetryAfter = time.Until(time.Unix(reset, 0))
		}
	case res.StatusCode == http.StatusUnauthorized:
		e.Class, e.Message = integration.Config, "X 인증이 만료됐거나 권한이 없어요. 다시 연결해 주세요"
	case res.StatusCode >= 500:
		if sideEffect {
			e.Class, e.Message = integration.Unknown, "X 서버 오류로 결과가 불명확해요. 실제로 올라갔는지 확인이 필요해요"
		} else {
			e.Class, e.Message = integration.Retryable, "X 서버 오류예요. 잠시 뒤 다시 시도해요"
		}
	case res.StatusCode == http.StatusForbidden:
		e.Class, e.Message = integration.Review, "X가 이 요청을 허용하지 않았어요"
	default:
		e.Class, e.Message = integration.Review, "X가 요청을 거부했어요"
	}
	if detail != "" && e.Class != integration.Retryable {
		e.Message += ": " + truncate(detail, 300)
	}
	return e
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func (h *HTTP) Me(ctx context.Context, accessToken string) (Account, error) {
	req, _ := http.NewRequest(http.MethodGet, h.c.APIBase+"/2/users/me?user.fields=subscription_type", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	var out struct {
		Data Account `json:"data"`
	}
	if err := h.do(ctx, req, "users.me", false, &out); err != nil {
		return Account{}, err
	}
	return out.Data, nil
}

// UploadImage는 간단 업로드(multipart media + media_category=tweet_image)다. 두 번 올려도 게시가 생기지 않으므로 재시도해도 된다.
func (h *HTTP) UploadImage(ctx context.Context, accessToken string, data []byte, contentType string) (string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("media_category", "tweet_image")
	fw, err := w.CreateFormFile("media", "image")
	if err != nil {
		return "", err
	}
	_, _ = fw.Write(data)
	_ = w.Close()
	req, _ := http.NewRequest(http.MethodPost, h.c.APIBase+"/2/media/upload", &buf)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", w.FormDataContentType())
	var out struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := h.do(ctx, req, "media.upload", false, &out); err != nil {
		return "", err
	}
	if out.Data.ID == "" {
		return "", &integration.Error{Class: integration.Retryable, Provider: "x", Op: "media.upload", Message: "미디어 ID를 받지 못했어요"}
	}
	return out.Data.ID, nil
}

// CreatePost는 게시물 하나를 만든다. 멱등성 키가 없으므로 결과가 불명확하면 Unknown으로 돌려준다.
func (h *HTTP) CreatePost(ctx context.Context, accessToken string, p PostRequest) (Post, error) {
	body := map[string]any{"text": p.Text}
	if len(p.MediaIDs) > 0 {
		body["media"] = map[string]any{"media_ids": p.MediaIDs}
	}
	if p.ReplyToID != "" {
		body["reply"] = map[string]any{"in_reply_to_tweet_id": p.ReplyToID}
	}
	if p.MadeWithAI {
		body["made_with_ai"] = true
	}
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, h.c.APIBase+"/2/tweets", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	var out struct {
		Data Post `json:"data"`
	}
	if err := h.do(ctx, req, "tweets.create", true, &out); err != nil {
		return Post{}, err
	}
	if out.Data.ID == "" {
		return Post{}, &integration.Error{Class: integration.Unknown, Provider: "x", Op: "tweets.create", Message: "게시 ID 없이 성공 응답이 왔어요. 확인이 필요해요"}
	}
	return out.Data, nil
}

// PostURL은 게시물 주소다.
func PostURL(username, id string) string {
	if username == "" {
		return fmt.Sprintf("https://x.com/i/web/status/%s", id)
	}
	return fmt.Sprintf("https://x.com/%s/status/%s", username, id)
}
