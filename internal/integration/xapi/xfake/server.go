// Package xfake는 테스트용 가짜 X API 서버다. 실제 HTTP 클라이언트(xapi.HTTP)를 그대로 써서
// 성공·거부·한도·응답 없음(보낸 뒤 끊김)을 재현한다. 실제 공개 게시를 검증 기본값으로 쓰지 않기 위해서다.
package xfake

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Post struct {
	ID       string
	Text     string
	ReplyTo  string
	MediaIDs []string
	AI       bool
}

// Server는 가짜 X다. 필드를 바꿔 다음 요청의 동작을 정한다.
type Server struct {
	*httptest.Server

	mu    sync.Mutex
	seq   int
	posts []Post
	media int

	// 동작 조절
	RejectReplies int           // 답글 요청에 이 상태 코드로 거부 (예: 403). 0이면 정상
	FailNextPosts []int         // 다음 게시 요청들에 차례로 돌려줄 상태 코드 (예: 503, 429)
	HangAfterPost time.Duration // 게시를 기록한 뒤 응답 전에 기다린다 (클라이언트 시간 초과 재현)
	Refreshes     int
	validTokens   map[string]bool
	usedRefresh   map[string]bool
}

func New() *Server {
	s := &Server{validTokens: map[string]bool{}, usedRefresh: map[string]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /2/oauth2/token", s.token)
	mux.HandleFunc("GET /2/users/me", s.auth(s.me))
	mux.HandleFunc("POST /2/media/upload", s.auth(s.upload))
	mux.HandleFunc("POST /2/tweets", s.auth(s.tweet))
	s.Server = httptest.NewServer(mux)
	return s
}

// Configure는 동작 조절 값을 잠금 안에서 바꾼다. 처리 중인 요청이 같은 값을 읽으므로 테스트는 필드를 직접 쓰지 않는다
func (s *Server) Configure(f func(*Server)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(s)
}

// RefreshCount는 지금까지 처리한 토큰 갱신 횟수다
func (s *Server) RefreshCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Refreshes
}

func (s *Server) Posts() []Post {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Post(nil), s.posts...)
}

// Expire는 모든 접근 토큰을 무효로 만든다 (만료 재현).
func (s *Server) Expire() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.validTokens = map[string]bool{}
}

func problem(w http.ResponseWriter, status int, title, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"title": title, "detail": detail, "status": status, "type": "about:blank"})
}

func (s *Server) issue(w http.ResponseWriter) {
	s.seq++
	acc, ref := fmt.Sprintf("acc-%d", s.seq), fmt.Sprintf("ref-%d", s.seq)
	s.validTokens[acc] = true
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": acc, "refresh_token": ref, "expires_in": 7200, "token_type": "bearer",
		"scope": "tweet.read tweet.write users.read media.write offline.access"})
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, _, ok := r.BasicAuth(); !ok {
		problem(w, 401, "Unauthorized", "client authentication required")
		return
	}
	switch r.Form.Get("grant_type") {
	case "authorization_code":
		if r.Form.Get("code") != "good-code" || r.Form.Get("code_verifier") == "" {
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
	case "refresh_token":
		rt := r.Form.Get("refresh_token")
		if !strings.HasPrefix(rt, "ref-") || s.usedRefresh[rt] { // 갱신 토큰은 1회용
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		s.usedRefresh[rt] = true
		s.Refreshes++
	default:
		w.WriteHeader(400)
		return
	}
	s.issue(w)
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		s.mu.Lock()
		ok := s.validTokens[tok]
		s.mu.Unlock()
		if !ok {
			problem(w, 401, "Unauthorized", "Unauthorized")
			return
		}
		next(w, r)
	}
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": "42", "username": "tester", "name": "테스트", "subscription_type": "None"}})
}

func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(10 << 20); err != nil || r.FormValue("media_category") != "tweet_image" {
		problem(w, 400, "Invalid Request", "media and media_category required")
		return
	}
	f, _, err := r.FormFile("media")
	if err != nil {
		problem(w, 400, "Invalid Request", "media required")
		return
	}
	n, _ := io.Copy(io.Discard, f)
	if n > 5<<20 {
		problem(w, 400, "Invalid Request", "media too large")
		return
	}
	s.mu.Lock()
	s.media++
	id := fmt.Sprintf("77%016d", s.media)
	s.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": id, "media_key": "3_" + id, "expires_after_secs": 86400}})
}

func (s *Server) tweet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text  string `json:"text"`
		Media *struct {
			MediaIDs []string `json:"media_ids"`
		} `json:"media"`
		Reply *struct {
			InReplyTo string `json:"in_reply_to_tweet_id"`
		} `json:"reply"`
		MadeWithAI bool `json:"made_with_ai"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields() // 실제 API도 모르는 필드를 거부한다
	if err := dec.Decode(&body); err != nil {
		problem(w, 400, "Invalid Request", err.Error())
		return
	}
	s.mu.Lock()
	if len(s.FailNextPosts) > 0 {
		st := s.FailNextPosts[0]
		s.FailNextPosts = s.FailNextPosts[1:]
		s.mu.Unlock()
		if st == 429 {
			w.Header().Set("x-rate-limit-reset", strconv.FormatInt(time.Now().Add(time.Second).Unix(), 10))
		}
		problem(w, st, http.StatusText(st), "injected")
		return
	}
	if body.Reply != nil && s.RejectReplies != 0 {
		st := s.RejectReplies
		s.mu.Unlock()
		problem(w, st, "Forbidden", "You are not permitted to reply to this conversation")
		return
	}
	if body.Reply != nil {
		found := false
		for _, p := range s.posts {
			if p.ID == body.Reply.InReplyTo {
				found = true
			}
		}
		if !found {
			s.mu.Unlock()
			problem(w, 400, "Invalid Request", "in_reply_to_tweet_id not found")
			return
		}
	}
	s.seq++
	p := Post{ID: fmt.Sprintf("18%017d", s.seq), Text: body.Text, AI: body.MadeWithAI}
	if body.Reply != nil {
		p.ReplyTo = body.Reply.InReplyTo
	}
	if body.Media != nil {
		p.MediaIDs = body.Media.MediaIDs
	}
	s.posts = append(s.posts, p)
	hang := s.HangAfterPost
	s.mu.Unlock()
	if hang > 0 {
		time.Sleep(hang) // 게시는 됐지만 응답이 늦는다
	}
	w.WriteHeader(201)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": p.ID, "text": p.Text}})
}
