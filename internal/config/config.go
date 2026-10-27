// Package config는 환경변수를 읽고 검증한다.
// 앱 기동에 반드시 필요한 값(DB·관리자 인증·암호화 키)과,
// 비어 있어도 "연결 안 됨"으로 설정 화면까지 들어갈 수 있는 외부 연동 값을 구분한다.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Env string

const (
	EnvDevelopment Env = "development"
	EnvTest        Env = "test"
	EnvProduction  Env = "production"
)

type IntegrationMode string

const (
	ModeLive IntegrationMode = "live"
	ModeMock IntegrationMode = "mock"
)

type Config struct {
	Env         Env
	Port        string
	BaseURL     *url.URL
	LogLevel    string
	DatabaseURL string

	AdminUsername     string
	AdminPasswordHash string
	TokenKey          []byte // AES-256 키 (32바이트)
	SessionTTL        time.Duration

	PublishingEnabled bool
	IntegrationMode   IntegrationMode
	MigrateOnStart    bool
	TrustedProxies    []*net.IPNet
	DataDir           string // 목업 저장소 등 개발용 로컬 파일 위치

	AI AIConfig
	X  XConfig
	R2 R2Config
}

type AIConfig struct {
	APIKey     string
	TextModel  string
	ImageModel string
	BaseURL    string
}

// Configured는 실제 호출에 필요한 값이 모두 있는지 알려 준다.
func (c AIConfig) Configured() bool { return c.APIKey != "" }

type XConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	APIBaseURL   string
	AuthorizeURL string
	TokenURL     string
}

func (c XConfig) Configured() bool { return c.ClientID != "" && c.RedirectURI != "" }

type R2Config struct {
	AccountID       string
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
	Bucket          string
	Prefix          string
	PublicBaseURL   string
}

func (c R2Config) Configured() bool {
	return (c.AccountID != "" || c.Endpoint != "") && c.AccessKeyID != "" && c.SecretAccessKey != "" && c.Bucket != ""
}

// ResolvedEndpoint는 R2 S3 호환 엔드포인트를 돌려준다.
func (c R2Config) ResolvedEndpoint() string {
	if c.Endpoint != "" {
		return c.Endpoint
	}
	if c.AccountID == "" {
		return ""
	}
	return "https://" + c.AccountID + ".r2.cloudflarestorage.com"
}

// Problem은 사람이 읽을 수 있는 설정 오류 하나다.
type Problem struct {
	Var     string
	Message string
}

// Error는 여러 설정 오류를 한 번에 보여 준다. 하나씩 고치고 재시작하는 반복을 줄인다.
type Error struct{ Problems []Problem }

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString("설정 오류:")
	for _, p := range e.Problems {
		fmt.Fprintf(&b, "\n  - %s: %s", p.Var, p.Message)
	}
	return b.String()
}

// Lookup은 테스트에서 환경을 주입하기 위한 함수 타입이다.
type Lookup func(string) (string, bool)

func Load() (*Config, error) { return LoadFrom(os.LookupEnv) }

func LoadFrom(lookup Lookup) (*Config, error) {
	get := func(k string) string {
		v, _ := lookup(k)
		return strings.TrimSpace(v)
	}
	var probs []Problem
	add := func(v, msg string) { probs = append(probs, Problem{v, msg}) }

	c := &Config{}
	switch Env(get("APP_ENV")) {
	case EnvDevelopment, EnvTest, EnvProduction:
		c.Env = Env(get("APP_ENV"))
	case "":
		add("APP_ENV", "필수입니다. development, test, production 중 하나를 지정하세요")
	default:
		add("APP_ENV", "development, test, production 중 하나여야 합니다")
	}

	c.Port = get("PORT")
	if c.Port == "" {
		c.Port = "8080"
	}
	if n, err := strconv.Atoi(c.Port); err != nil || n <= 0 || n > 65535 {
		add("PORT", "1~65535 사이 숫자여야 합니다")
	}

	c.LogLevel = strings.ToLower(get("LOG_LEVEL"))
	switch c.LogLevel {
	case "":
		c.LogLevel = "info"
	case "debug", "info", "warn", "error":
	default:
		add("LOG_LEVEL", "debug, info, warn, error 중 하나여야 합니다")
	}

	base := get("APP_BASE_URL")
	if base == "" {
		if c.Env == EnvProduction {
			add("APP_BASE_URL", "운영에서는 필수입니다 (예: https://x.example.com)")
		} else {
			base = "http://localhost:" + c.Port
		}
	}
	if base != "" {
		u, err := url.Parse(base)
		switch {
		case err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https"):
			add("APP_BASE_URL", "http(s)://호스트 형식이어야 합니다")
		case c.Env == EnvProduction && u.Scheme != "https":
			add("APP_BASE_URL", "운영에서는 https 주소여야 합니다 (보안 쿠키·OAuth 콜백)")
		default:
			u.Path = strings.TrimRight(u.Path, "/")
			c.BaseURL = u
		}
	}

	c.DatabaseURL = resolveDatabaseURL(get)
	if c.DatabaseURL == "" {
		add("DATABASE_URL", "필수입니다 (postgres://사용자:비밀번호@호스트:5432/DB, 또는 DB_HOST·DB_NAME·DB_USER·DB_PASSWORD)")
	}

	c.AdminUsername = get("ADMIN_USERNAME")
	c.AdminPasswordHash = get("ADMIN_PASSWORD_HASH")
	if c.Env != EnvTest {
		if c.AdminUsername == "" {
			add("ADMIN_USERNAME", "필수입니다")
		}
		switch {
		case c.AdminPasswordHash == "":
			add("ADMIN_PASSWORD_HASH", "필수입니다. `go run ./cmd/admin hash-password`로 만드세요")
		case !strings.HasPrefix(c.AdminPasswordHash, "$argon2id$"):
			add("ADMIN_PASSWORD_HASH", "Argon2id 해시($argon2id$...)여야 합니다. 평문 비밀번호를 넣지 마세요")
		}
	}

	if k := get("TOKEN_ENCRYPTION_KEY"); k != "" {
		key, err := base64.StdEncoding.DecodeString(k)
		if err != nil || len(key) != 32 {
			add("TOKEN_ENCRYPTION_KEY", "base64로 인코딩한 32바이트 키여야 합니다. `go run ./cmd/admin gen-key`로 만드세요")
		} else {
			c.TokenKey = key
		}
	} else if c.Env != EnvTest {
		add("TOKEN_ENCRYPTION_KEY", "필수입니다. 한 번 만들어 계속 같은 값을 쓰세요 (`go run ./cmd/admin gen-key`)")
	}

	c.SessionTTL = 720 * time.Hour
	if v := get("SESSION_TTL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 10*time.Minute || d > 90*24*time.Hour {
			add("SESSION_TTL", "10m 이상 2160h 이하의 기간이어야 합니다 (예: 720h)")
		} else {
			c.SessionTTL = d
		}
	}

	c.PublishingEnabled = false
	if v := get("PUBLISHING_ENABLED"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			add("PUBLISHING_ENABLED", "true 또는 false여야 합니다")
		}
		c.PublishingEnabled = b
	}

	switch IntegrationMode(get("INTEGRATION_MODE")) {
	case "":
		if c.Env == EnvProduction {
			c.IntegrationMode = ModeLive
		} else {
			c.IntegrationMode = ModeMock
		}
	case ModeLive:
		c.IntegrationMode = ModeLive
	case ModeMock:
		c.IntegrationMode = ModeMock
	default:
		add("INTEGRATION_MODE", "live 또는 mock이어야 합니다")
	}
	if c.Env == EnvProduction && c.IntegrationMode == ModeMock {
		add("INTEGRATION_MODE", "운영에서는 mock을 쓸 수 없습니다. 키가 없으면 live로 두고 설정 화면에서 연결하세요")
	}
	if c.IntegrationMode == ModeMock && c.PublishingEnabled {
		add("PUBLISHING_ENABLED", "목업 모드에서는 true로 둘 수 없습니다 (실게시 스위치와 목업이 섞이지 않게)")
	}

	c.MigrateOnStart = true
	if v := get("MIGRATE_ON_START"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			add("MIGRATE_ON_START", "true 또는 false여야 합니다")
		}
		c.MigrateOnStart = b
	}

	if v := get("TRUSTED_PROXY_CIDRS"); v != "" {
		for _, s := range strings.Split(v, ",") {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			_, n, err := net.ParseCIDR(s)
			if err != nil {
				add("TRUSTED_PROXY_CIDRS", fmt.Sprintf("CIDR 형식이 아닙니다: %q", s))
				continue
			}
			c.TrustedProxies = append(c.TrustedProxies, n)
		}
	}

	c.DataDir = get("DATA_DIR")
	if c.DataDir == "" {
		c.DataDir = ".data"
	}

	c.AI = AIConfig{
		APIKey:     get("AI_API_KEY"),
		TextModel:  get("AI_TEXT_MODEL"),
		ImageModel: get("AI_IMAGE_MODEL"),
		BaseURL:    get("AI_BASE_URL"),
	}
	c.X = XConfig{
		ClientID:     get("X_CLIENT_ID"),
		ClientSecret: get("X_CLIENT_SECRET"),
		RedirectURI:  get("X_REDIRECT_URI"),
		APIBaseURL:   get("X_API_BASE_URL"),
		AuthorizeURL: get("X_AUTHORIZE_URL"),
		TokenURL:     get("X_TOKEN_URL"),
	}
	if c.X.RedirectURI == "" && c.BaseURL != nil {
		c.X.RedirectURI = c.BaseURL.String() + "/api/x/oauth/callback"
	}
	if c.X.ClientSecret != "" && c.X.ClientID == "" {
		add("X_CLIENT_ID", "X_CLIENT_SECRET만 있고 X_CLIENT_ID가 없습니다")
	}
	c.R2 = R2Config{
		AccountID:       get("R2_ACCOUNT_ID"),
		Endpoint:        get("R2_ENDPOINT"),
		AccessKeyID:     get("R2_ACCESS_KEY_ID"),
		SecretAccessKey: get("R2_SECRET_ACCESS_KEY"),
		Bucket:          get("R2_BUCKET"),
		Prefix:          get("R2_PREFIX"),
		PublicBaseURL:   strings.TrimRight(get("R2_PUBLIC_BASE_URL"), "/"),
	}
	// KoDeploy 오브젝트 스토리지를 켜면 S3_* 값이 주입된다 (재배포마다 키가 바뀌므로 환경에서만 읽는다).
	// R2_* 값이 하나도 없을 때만 대신 쓴다
	if c.R2.Bucket == "" && c.R2.AccessKeyID == "" && get("S3_BUCKET") != "" {
		c.R2.Endpoint = get("S3_ENDPOINT")
		c.R2.AccessKeyID = get("S3_ACCESS_KEY")
		c.R2.SecretAccessKey = get("S3_SECRET_KEY")
		c.R2.Bucket = get("S3_BUCKET")
		if c.R2.PublicBaseURL == "" {
			c.R2.PublicBaseURL = strings.TrimRight(get("S3_PUBLIC_BASE_URL"), "/")
		}
	}
	if c.R2.Prefix == "" {
		c.R2.Prefix = "x-automation/"
	}
	if !strings.HasSuffix(c.R2.Prefix, "/") {
		c.R2.Prefix += "/"
	}
	if strings.HasPrefix(c.R2.Prefix, "/") || strings.Contains(c.R2.Prefix, "..") {
		add("R2_PREFIX", "앞에 /가 없고 ..가 없는 경로여야 합니다 (예: x-automation/)")
	}
	r2Set := c.R2.AccessKeyID != "" || c.R2.SecretAccessKey != "" || c.R2.Bucket != ""
	if r2Set && !c.R2.Configured() {
		add("R2_*", "R2 값이 일부만 있습니다. R2_ACCOUNT_ID(또는 R2_ENDPOINT)·R2_ACCESS_KEY_ID·R2_SECRET_ACCESS_KEY·R2_BUCKET을 모두 넣거나 모두 비우세요")
	}
	if c.R2.PublicBaseURL != "" {
		if u, err := url.Parse(c.R2.PublicBaseURL); err != nil || u.Scheme != "https" || u.Host == "" {
			add("R2_PUBLIC_BASE_URL", "https://로 시작하는 공개 주소여야 합니다")
		}
	}

	if len(probs) > 0 {
		return nil, &Error{Problems: probs}
	}
	return c, nil
}

// resolveDatabaseURL은 pgx가 읽을 수 있는 연결 문자열을 만든다.
// KoDeploy 관리형 PostgreSQL은 DATABASE_URL을 SQLAlchemy 형식(postgresql+psycopg2://)으로 주입하고
// DB_HOST·DB_PORT·DB_NAME·DB_USER·DB_PASSWORD를 함께 준다.
func resolveDatabaseURL(get func(string) string) string {
	raw := get("DATABASE_URL")
	if raw != "" {
		if i := strings.Index(raw, "://"); i > 0 {
			scheme := raw[:i]
			if j := strings.Index(scheme, "+"); j > 0 {
				scheme = scheme[:j]
			}
			if scheme == "postgres" || scheme == "postgresql" {
				return scheme + raw[i:]
			}
		}
		return raw
	}
	host := get("DB_HOST")
	if host == "" {
		return ""
	}
	port := get("DB_PORT")
	if port == "" {
		port = "5432"
	}
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(get("DB_USER"), get("DB_PASSWORD")),
		Host:   net.JoinHostPort(host, port),
		Path:   "/" + get("DB_NAME"),
	}
	q := url.Values{}
	if s := get("DB_SSLMODE"); s != "" {
		q.Set("sslmode", s)
	} else {
		q.Set("sslmode", "disable") // 같은 클러스터 내부 연결 (KoDeploy 관리형 DB)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// SecureCookies는 쿠키에 Secure 속성을 붙일지 정한다. 로컬 HTTP 개발만 예외다.
func (c *Config) SecureCookies() bool {
	return c.Env == EnvProduction || (c.BaseURL != nil && c.BaseURL.Scheme == "https")
}

// AsError는 errors.As 도우미.
func AsError(err error) (*Error, bool) {
	var e *Error
	ok := errors.As(err, &e)
	return e, ok
}
