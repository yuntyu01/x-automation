package connection

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yuntyu01/x-automation/internal/clock"
	"github.com/yuntyu01/x-automation/internal/integration"
	"github.com/yuntyu01/x-automation/internal/integration/xapi"
	"github.com/yuntyu01/x-automation/internal/notification"
	"github.com/yuntyu01/x-automation/internal/secretbox"
	"github.com/yuntyu01/x-automation/internal/store"
)

// X 계정 연결: OAuth 2.0 Authorization Code + PKCE.
//   - state·code_verifier는 서버에 암호화해 두고, 연결을 시작한 로그인 세션에서만 완료할 수 있다 (10분, 1회용)
//   - 토큰은 TOKEN_ENCRYPTION_KEY(AES-256-GCM)로 암호화해 DB에 둔다. 로그·응답에 내보내지 않는다
//   - 갱신 토큰은 1회용이라 갱신을 잠금으로 직렬화하고 새 토큰을 바로 저장한다
//   - 최초 한 계정만 연결한다. 다른 계정으로 바꾸려면 먼저 해제한다

const refreshLock = 7243100421

var (
	ErrNotConnected = errors.New("연결된 X 계정이 없어요")
	ErrOtherAccount = errors.New("이미 다른 X 계정이 연결되어 있어요. 먼저 연결을 해제하세요")
)

// FlowError는 연결 과정에서 사용자에게 보여 줄 오류다.
type FlowError struct{ Message string }

func (e *FlowError) Error() string { return e.Message }

// RequiredScopes는 게시에 꼭 필요한 권한이다 (media.write는 이미지가 있을 때만 필요).
var RequiredScopes = []string{"tweet.read", "tweet.write", "users.read", "offline.access"}

type Service struct {
	pool   *pgxpool.Pool
	repo   *Repo
	client xapi.Client
	box    *secretbox.Box
	clock  clock.Clock
	notify *notification.Service
}

func NewService(pool *pgxpool.Pool, client xapi.Client, box *secretbox.Box, c clock.Clock, n *notification.Service) *Service {
	if c == nil {
		c = clock.System{}
	}
	return &Service{pool: pool, repo: NewRepo(pool), client: client, box: box, clock: c, notify: n}
}

func (s *Service) Client() xapi.Client { return s.client }

func randomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// Begin은 인가 주소를 만든다.
func (s *Service) Begin(ctx context.Context, sessionHash []byte) (string, error) {
	state := randomString(32)
	verifier := randomString(48) // 64자 (RFC 7636: 43~128자)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	enc, err := s.box.Seal([]byte(verifier), "oauth:"+state)
	if err != nil {
		return "", err
	}
	now := s.clock.Now()
	if _, err := s.pool.Exec(ctx, `DELETE FROM oauth_states WHERE expires_at < $1`, now.Add(-time.Hour)); err != nil {
		return "", err
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO oauth_states (state, session_hash, code_verifier_enc, expires_at) VALUES ($1, $2, $3, $4)`,
		state, sessionHash, enc, now.Add(10*time.Minute)); err != nil {
		return "", err
	}
	u := s.client.AuthorizeURL(state, challenge)
	if u == "" {
		return "", integration.ErrNotConfigured("x")
	}
	return u, nil
}

// Complete는 콜백을 처리한다: state 확인 → 코드 교환 → 계정 확인 → 토큰 암호화 저장.
func (s *Service) Complete(ctx context.Context, sessionHash []byte, state, code, errParam string) (View, error) {
	if errParam != "" {
		return View{}, &FlowError{"X에서 연결을 취소했거나 허용하지 않았어요"}
	}
	if state == "" || code == "" {
		return View{}, &FlowError{"연결 응답이 올바르지 않아요. 다시 시도하세요"}
	}
	var hash, enc []byte
	err := s.pool.QueryRow(ctx, `UPDATE oauth_states SET used_at = $2 WHERE state = $1 AND used_at IS NULL AND expires_at > $2
		RETURNING session_hash, code_verifier_enc`, state, s.clock.Now()).Scan(&hash, &enc)
	if store.IsNoRows(err) {
		return View{}, &FlowError{"연결 요청이 만료됐거나 이미 쓰였어요. 다시 시도하세요"}
	}
	if err != nil {
		return View{}, err
	}
	if !hmac.Equal(hash, sessionHash) {
		return View{}, &FlowError{"다른 로그인 세션에서 시작한 연결이에요. 이 화면에서 다시 시도하세요"}
	}
	verifier, err := s.box.Open(enc, "oauth:"+state)
	if err != nil {
		return View{}, err
	}
	tok, err := s.client.ExchangeCode(ctx, code, string(verifier))
	if err != nil {
		return View{}, err
	}
	acct, err := s.client.Me(ctx, tok.AccessToken)
	if err != nil {
		return View{}, err
	}
	cur, err := s.repo.View(ctx)
	if err != nil {
		return View{}, err
	}
	if cur.AccountID != "" && cur.AccountID != acct.ID && cur.Status != Disconnected {
		return View{}, ErrOtherAccount
	}
	status, detail := Connected, ""
	var missing []string
	for _, sc := range RequiredScopes {
		if !contains(tok.Scopes, sc) {
			missing = append(missing, sc)
		}
	}
	if len(missing) > 0 {
		status, detail = Errored, "필요한 권한이 빠졌어요: "+strings.Join(missing, ", ")+". 다시 연결해 주세요"
	} else if !contains(tok.Scopes, "media.write") {
		detail = "이미지 업로드 권한(media.write)이 없어 이미지가 붙은 글은 게시할 수 없어요"
	}
	caps := map[string]any{
		"subscription_type": acct.SubscriptionType,
		// 장문 게시는 구독 등급에 따라 다르고 API 문서에 허용 등급이 없다. None이면 확실히 불가로 본다
		"long_post_possible": acct.SubscriptionType != "" && acct.SubscriptionType != "None",
		"media_write":        contains(tok.Scopes, "media.write"),
	}
	if err := s.saveTokens(ctx, s.pool, acct, tok, status, detail, caps, true); err != nil {
		return View{}, err
	}
	if s.notify != nil {
		_ = s.notify.Resolve(ctx, "x:auth")
	}
	return s.repo.View(ctx)
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func (s *Service) saveTokens(ctx context.Context, db store.DB, acct xapi.Account, tok xapi.Token, status Status, detail string, caps map[string]any, fresh bool) error {
	accEnc, err := s.box.Seal([]byte(tok.AccessToken), "x:access")
	if err != nil {
		return err
	}
	refEnc, err := s.box.Seal([]byte(tok.RefreshToken), "x:refresh")
	if err != nil {
		return err
	}
	if !fresh {
		_, err = db.Exec(ctx, `UPDATE connections SET access_token_enc = $1, refresh_token_enc = $2, token_expires_at = $3,
			scopes = CASE WHEN cardinality($4::text[]) > 0 THEN $4 ELSE scopes END, updated_at = now() WHERE provider = 'x'`,
			accEnc, refEnc, tok.ExpiresAt, tok.Scopes)
		return err
	}
	_, err = db.Exec(ctx, `INSERT INTO connections (provider, account_id, username, display_name, access_token_enc, refresh_token_enc,
		token_expires_at, scopes, status, status_detail, capabilities, connected_at, updated_at)
		VALUES ('x', $1, $2, $3, $4, $5, $6, $7, $8, NULLIF($9, ''), $10, now(), now())
		ON CONFLICT (provider) DO UPDATE SET account_id = EXCLUDED.account_id, username = EXCLUDED.username, display_name = EXCLUDED.display_name,
		access_token_enc = EXCLUDED.access_token_enc, refresh_token_enc = EXCLUDED.refresh_token_enc, token_expires_at = EXCLUDED.token_expires_at,
		scopes = EXCLUDED.scopes, status = EXCLUDED.status, status_detail = EXCLUDED.status_detail, capabilities = EXCLUDED.capabilities,
		connected_at = now(), updated_at = now()`,
		acct.ID, acct.Username, acct.Name, accEnc, refEnc, tok.ExpiresAt, tok.Scopes, string(status), detail, caps)
	return err
}

// Credentials는 게시에 쓸 접근 토큰과 계정 정보다.
type Credentials struct {
	AccessToken string
	Username    string
	MediaWrite  bool
}

// Token은 유효한 접근 토큰을 돌려준다. 만료 2분 전이면(또는 force) 잠금 안에서 갱신하고 새 토큰을 바로 저장한다.
func (s *Service) Token(ctx context.Context, force bool) (Credentials, error) {
	var cred Credentials
	var refreshErr error // 만료 상태는 저장(커밋)하고 오류는 돌려준다
	err := store.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, refreshLock); err != nil {
			return err
		}
		var accEnc, refEnc []byte
		var exp *time.Time
		var status Status
		var scopes []string
		err := tx.QueryRow(ctx, `SELECT access_token_enc, refresh_token_enc, token_expires_at, status, COALESCE(username, ''), scopes
			FROM connections WHERE provider = 'x' FOR UPDATE`).Scan(&accEnc, &refEnc, &exp, &status, &cred.Username, &scopes)
		if store.IsNoRows(err) || (err == nil && (status == Disconnected || accEnc == nil)) {
			return ErrNotConnected
		}
		if err != nil {
			return err
		}
		if status == Expired {
			return &integration.Error{Class: integration.Config, Provider: "x", Op: "token", Message: "X 연결이 만료됐어요. 다시 연결해 주세요"}
		}
		cred.MediaWrite = contains(scopes, "media.write")
		if !force && exp != nil && exp.After(s.clock.Now().Add(2*time.Minute)) {
			acc, err := s.box.Open(accEnc, "x:access")
			cred.AccessToken = string(acc)
			return err
		}
		ref, err := s.box.Open(refEnc, "x:refresh")
		if err != nil {
			return err
		}
		tok, err := s.client.Refresh(ctx, string(ref))
		if err != nil {
			if integration.ClassOf(err) == integration.Config {
				// 갱신 토큰이 무효: 사람이 다시 연결해야 한다 (무한 재시도하지 않는다)
				_, _ = tx.Exec(ctx, `UPDATE connections SET status = 'expired', status_detail = $1, updated_at = now() WHERE provider = 'x'`,
					"토큰 갱신에 실패했어요. 다시 연결해 주세요")
				if s.notify != nil {
					_ = s.notify.Notify(ctx, notification.New{Severity: notification.Critical, Category: "x", GroupKey: "x:auth",
						Title: "X 다시 연결이 필요해요", Body: "X 토큰 갱신에 실패해 게시를 멈췄어요. 설정 → 연결에서 다시 연결해 주세요", Link: "/settings/connections"})
				}
				refreshErr = err
				return nil
			}
			return err
		}
		if err := s.saveTokens(ctx, tx, xapi.Account{}, tok, Connected, "", nil, false); err != nil {
			return err
		}
		cred.AccessToken = tok.AccessToken
		return nil
	})
	if err == nil && refreshErr != nil {
		return Credentials{}, refreshErr
	}
	return cred, err
}

// Disconnect는 토큰을 해제(가능하면)하고 지운다.
func (s *Service) Disconnect(ctx context.Context) error {
	var refEnc, accEnc []byte
	err := s.pool.QueryRow(ctx, `SELECT access_token_enc, refresh_token_enc FROM connections WHERE provider = 'x'`).Scan(&accEnc, &refEnc)
	if err != nil && !store.IsNoRows(err) {
		return err
	}
	for _, x := range []struct {
		enc []byte
		aad string
	}{{refEnc, "x:refresh"}, {accEnc, "x:access"}} {
		if x.enc == nil {
			continue
		}
		if t, err := s.box.Open(x.enc, x.aad); err == nil {
			rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			_ = s.client.Revoke(rctx, string(t)) // 실패해도 앱에서는 지운다
			cancel()
		}
	}
	_, err = s.pool.Exec(ctx, `UPDATE connections SET access_token_enc = NULL, refresh_token_enc = NULL, token_expires_at = NULL, scopes = '{}',
		status = 'disconnected', status_detail = NULL, account_id = NULL, username = NULL, display_name = NULL, capabilities = '{}', updated_at = now()
		WHERE provider = 'x'`)
	return err
}

// Check는 연결이 살아 있는지 계정 정보를 다시 읽어 확인한다 (게시하지 않는다).
func (s *Service) Check(ctx context.Context) (View, error) {
	cred, err := s.Token(ctx, false)
	if err != nil {
		return View{}, err
	}
	acct, err := s.client.Me(ctx, cred.AccessToken)
	if integration.ClassOf(err) == integration.Config {
		if cred, err = s.Token(ctx, true); err == nil {
			acct, err = s.client.Me(ctx, cred.AccessToken)
		}
	}
	if err != nil {
		return View{}, err
	}
	_, err = s.pool.Exec(ctx, `UPDATE connections SET username = $1, display_name = $2,
		capabilities = capabilities || jsonb_build_object('subscription_type', $3::text, 'long_post_possible', $3::text NOT IN ('', 'None')),
		updated_at = now() WHERE provider = 'x'`, acct.Username, acct.Name, acct.SubscriptionType)
	if err != nil {
		return View{}, err
	}
	return s.repo.View(ctx)
}
