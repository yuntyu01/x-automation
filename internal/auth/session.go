package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/yuntyu01/x-automation/internal/clock"
	"github.com/yuntyu01/x-automation/internal/store"
)

const CookieName = "xa_session"

// 로그인 시도 제한 (15분 창). 아이디 기준 제한은 접속 IP별로 센다: 아이디만으로 세면
// 아무나 틀린 비밀번호를 열 번 보내 관리자 로그인을 계속 막을 수 있다
const (
	attemptWindow     = 15 * time.Minute
	maxFailuresPerIP  = 20
	maxFailuresPerUsr = 10
	lastSeenThrottle  = 5 * time.Minute
	// Argon2id 확인 한 번이 64MiB를 쓴다. 동시에 하나만 돌리고, 자리를 못 잡으면 이만큼 기다린 뒤 거절한다
	verifyWait = 5 * time.Second
)

var (
	ErrInvalidCredentials = errors.New("아이디 또는 비밀번호가 올바르지 않습니다")
	ErrNoSession          = errors.New("로그인이 필요합니다")
)

// RateLimitedError는 시도 제한에 걸렸을 때 다시 시도할 수 있는 시각을 알려 준다.
type RateLimitedError struct {
	RetryAfter time.Duration
	Busy       bool // 다른 로그인 확인이 끝나기를 기다리다 시간이 다 됨
}

func (e *RateLimitedError) Error() string {
	if e.Busy {
		return "로그인 요청이 몰려 있습니다. 잠시 후 다시 시도하세요"
	}
	mins := int(e.RetryAfter.Minutes()) + 1
	return fmt.Sprintf("로그인 시도가 너무 많습니다. %d분 후 다시 시도하세요", mins)
}

type Session struct {
	IDHash    []byte
	Username  string
	CSRFToken string
	CreatedAt time.Time
	ExpiresAt time.Time
	LastSeen  time.Time
}

type Config struct {
	Username     string
	PasswordHash string
	TTL          time.Duration
}

type Service struct {
	db     store.DB
	cfg    Config
	clock  clock.Clock
	dummy  string        // 없는 사용자에도 같은 시간 동안 해시 검증을 돌려 응답 시간 차이를 줄인다
	verify chan struct{} // 비밀번호 확인 자리 (한 번에 하나)
}

func NewService(db store.DB, cfg Config, c clock.Clock) *Service {
	if c == nil {
		c = clock.System{}
	}
	dummy, _ := HashPassword("dummy-password-for-timing", DefaultParams)
	return &Service{db: db, cfg: cfg, clock: c, dummy: dummy, verify: make(chan struct{}, 1)}
}

func randomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashID(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

// Login은 자격 증명을 확인하고 새 세션을 만든다. 성공·실패 모두 시도 기록을 남긴다.
// 반환하는 raw는 쿠키에만 싣는다 (DB에는 해시만 저장).
func (s *Service) Login(ctx context.Context, username, password, ip, ua string) (raw string, sess Session, err error) {
	// 자리를 잡은 뒤에 시도 제한을 확인한다. 동시에 몰려온 요청이 같은 실패 횟수를 보고 한꺼번에 통과하지 못하게 하고,
	// 해시 메모리가 겹쳐 컨테이너 한도(256Mi)를 넘지 않게 한다
	wait := time.NewTimer(verifyWait)
	defer wait.Stop()
	select {
	case s.verify <- struct{}{}:
		defer func() { <-s.verify }()
	case <-wait.C:
		return "", sess, &RateLimitedError{RetryAfter: verifyWait, Busy: true}
	case <-ctx.Done():
		return "", sess, ctx.Err()
	}
	now := s.clock.Now()
	if wait, err := s.blocked(ctx, ip, username, now); err != nil {
		return "", sess, err
	} else if wait > 0 {
		return "", sess, &RateLimitedError{RetryAfter: wait}
	}

	userOK := subtle.ConstantTimeCompare([]byte(username), []byte(s.cfg.Username)) == 1 && s.cfg.Username != ""
	hash := s.dummy
	if userOK {
		hash = s.cfg.PasswordHash
	}
	passOK, verr := VerifyPassword(password, hash)
	ok := userOK && passOK && verr == nil
	if _, err := s.db.Exec(ctx, `INSERT INTO login_attempts (ip, username, success, created_at) VALUES ($1, $2, $3, $4)`,
		ip, truncate(username, 64), ok, now); err != nil {
		return "", sess, err
	}
	if !ok {
		return "", sess, ErrInvalidCredentials
	}

	raw = randomToken(32)
	sess = Session{IDHash: hashID(raw), Username: s.cfg.Username, CSRFToken: randomToken(32),
		CreatedAt: now, ExpiresAt: now.Add(s.cfg.TTL), LastSeen: now}
	_, err = s.db.Exec(ctx, `INSERT INTO sessions (id_hash, username, csrf_token, created_at, expires_at, last_seen_at, user_agent, ip)
		VALUES ($1, $2, $3, $4, $5, $4, $6, $7)`, sess.IDHash, sess.Username, sess.CSRFToken, now, sess.ExpiresAt, truncate(ua, 300), ip)
	if err != nil {
		return "", Session{}, err
	}
	return raw, sess, nil
}

func (s *Service) blocked(ctx context.Context, ip, username string, now time.Time) (time.Duration, error) {
	since := now.Add(-attemptWindow)
	var ipFails, userFails int
	var ipOldest, userOldest *time.Time
	if err := s.db.QueryRow(ctx, `SELECT count(*), min(created_at) FROM login_attempts
		WHERE ip = $1 AND NOT success AND created_at > $2`, ip, since).Scan(&ipFails, &ipOldest); err != nil {
		return 0, err
	}
	if err := s.db.QueryRow(ctx, `SELECT count(*), min(created_at) FROM login_attempts
		WHERE username = $1 AND ip = $2 AND NOT success AND created_at > $3`, truncate(username, 64), ip, since).Scan(&userFails, &userOldest); err != nil {
		return 0, err
	}
	var wait time.Duration
	if ipFails >= maxFailuresPerIP && ipOldest != nil {
		wait = max(wait, ipOldest.Add(attemptWindow).Sub(now))
	}
	if userFails >= maxFailuresPerUsr && userOldest != nil {
		wait = max(wait, userOldest.Add(attemptWindow).Sub(now))
	}
	return wait, nil
}

// Authenticate는 쿠키의 세션 ID로 유효한 세션을 찾는다.
func (s *Service) Authenticate(ctx context.Context, raw string) (Session, error) {
	if raw == "" || len(raw) > 128 {
		return Session{}, ErrNoSession
	}
	var sess Session
	err := s.db.QueryRow(ctx, `SELECT id_hash, username, csrf_token, created_at, expires_at, last_seen_at FROM sessions
		WHERE id_hash = $1 AND revoked_at IS NULL AND expires_at > $2`, hashID(raw), s.clock.Now()).
		Scan(&sess.IDHash, &sess.Username, &sess.CSRFToken, &sess.CreatedAt, &sess.ExpiresAt, &sess.LastSeen)
	if store.IsNoRows(err) {
		return Session{}, ErrNoSession
	}
	if err != nil {
		return Session{}, err
	}
	// 관리자 아이디가 바뀌면 이전 세션은 무효다
	if sess.Username != s.cfg.Username {
		return Session{}, ErrNoSession
	}
	if now := s.clock.Now(); now.Sub(sess.LastSeen) > lastSeenThrottle {
		_, _ = s.db.Exec(ctx, `UPDATE sessions SET last_seen_at = $2 WHERE id_hash = $1`, sess.IDHash, now)
	}
	return sess, nil
}

// Logout은 세션을 즉시 무효화한다.
func (s *Service) Logout(ctx context.Context, raw string) error {
	_, err := s.db.Exec(ctx, `UPDATE sessions SET revoked_at = $2 WHERE id_hash = $1 AND revoked_at IS NULL`, hashID(raw), s.clock.Now())
	return err
}

// ValidCSRF는 상태 변경 요청의 토큰을 일정 시간 비교로 확인한다.
func ValidCSRF(sess Session, token string) bool {
	return token != "" && subtle.ConstantTimeCompare([]byte(sess.CSRFToken), []byte(token)) == 1
}

// Cleanup은 만료 세션과 오래된 로그인 기록을 지운다.
func (s *Service) Cleanup(ctx context.Context) error {
	now := s.clock.Now()
	if _, err := s.db.Exec(ctx, `DELETE FROM sessions WHERE expires_at < $1 OR revoked_at < $1 - interval '1 day'`, now); err != nil {
		return err
	}
	_, err := s.db.Exec(ctx, `DELETE FROM login_attempts WHERE created_at < $1`, now.Add(-7*24*time.Hour))
	return err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
