package app

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yuntyu01/x-automation/internal/auth"
	"github.com/yuntyu01/x-automation/internal/config"
	"github.com/yuntyu01/x-automation/internal/connection"
	"github.com/yuntyu01/x-automation/internal/content"
	"github.com/yuntyu01/x-automation/internal/httpapi"
	"github.com/yuntyu01/x-automation/internal/integration/storage"
	"github.com/yuntyu01/x-automation/internal/media"
	"github.com/yuntyu01/x-automation/internal/notification"
	"github.com/yuntyu01/x-automation/internal/prompt"
	"github.com/yuntyu01/x-automation/internal/settings"
)

// Version은 빌드 때 -ldflags로 넣는다.
var Version = "dev"

// services는 DB가 준비된 뒤 만드는 도메인 서비스 묶음이다.
type services struct {
	auth          *auth.Service
	settings      *settings.Repo
	notifications *notification.Service
	connections   *connection.Repo
	contents      *content.Service
	prompts       *prompt.Service
	media         *media.Service
	store         storage.Store
}

func (a *App) newServices(pool *pgxpool.Pool) *services {
	s := &services{
		auth: auth.NewService(pool, auth.Config{
			Username: a.cfg.AdminUsername, PasswordHash: a.cfg.AdminPasswordHash, TTL: a.cfg.SessionTTL,
		}, a.deps.Clock),
		settings:      settings.NewRepo(pool),
		notifications: notification.NewService(pool),
		connections:   connection.NewRepo(pool),
		prompts:       prompt.NewService(pool),
	}
	setRepo := settings.NewRepo(pool)
	s.contents = content.NewService(pool, a.deps.Clock, func(ctx context.Context) bool {
		st, err := setRepo.Get(ctx)
		return err == nil && st.Policy.Thread.LongPostEnabled
	})
	s.store = a.newStore()
	s.media = media.NewService(pool, s.store, a.deps.Clock, func(ctx context.Context) int {
		st, err := setRepo.Get(ctx)
		if err != nil {
			return 14
		}
		return st.Policy.Retention.Days
	})
	s.contents.SetMediaLoader(s.media.ContentLoader())
	return s
}

// newStore는 이미지 저장소를 고른다. 목업은 로컬 파일, 실제 연동은 R2(값이 없으면 설정 오류를 돌려주는 대역).
func (a *App) newStore() storage.Store {
	if a.cfg.IntegrationMode == config.ModeMock {
		l, err := storage.NewLocal(filepath.Join(a.cfg.DataDir, "media"), a.cfg.BaseURL.String()+"/media")
		if err == nil {
			a.local = l
			return l
		}
		a.log.Error("local media store unavailable", "err", err.Error())
		return storage.Unconfigured{}
	}
	if !a.cfg.R2.Configured() {
		return storage.Unconfigured{}
	}
	r2, err := storage.NewR2(storage.R2Config{Endpoint: a.cfg.R2.ResolvedEndpoint(), AccessKeyID: a.cfg.R2.AccessKeyID,
		SecretAccessKey: a.cfg.R2.SecretAccessKey, Bucket: a.cfg.R2.Bucket, Prefix: a.cfg.R2.Prefix, PublicBaseURL: a.cfg.R2.PublicBaseURL})
	if err != nil {
		a.log.Error("r2 store unavailable", "err", err.Error())
		return storage.Unconfigured{}
	}
	return r2
}

// buildAPI는 /api/ 아래 라우트를 구성한다.
func (a *App) buildAPI(pool *pgxpool.Pool) http.Handler {
	svc := a.newServices(pool)
	a.svc = svc
	srv := &httpapi.Server{
		BaseURL:        a.cfg.BaseURL,
		SecureCookies:  a.cfg.SecureCookies(),
		TrustedProxies: a.cfg.TrustedProxies,
		Clock:          a.deps.Clock,
		Info: httpapi.RuntimeInfo{
			Env: string(a.cfg.Env), IntegrationMode: string(a.cfg.IntegrationMode),
			PublishingEnabled: a.cfg.PublishingEnabled, Version: Version,
		},
		Auth:          svc.auth,
		Settings:      svc.settings,
		Notifications: svc.notifications,
		Connections:   svc.connections,
		Integrations:  a.integrationStatus,
		Contents:      svc.contents,
		Prompts:       svc.prompts,
		Media:         svc.media,
	}
	return srv.Routes()
}

// startWorkers는 주기 작업을 시작한다. 단계마다 워커가 추가된다.
func (a *App) startWorkers(ctx context.Context, _ *pgxpool.Pool) {
	a.every(ctx, "auth-cleanup", time.Hour, func(ctx context.Context) error { return a.svc.auth.Cleanup(ctx) })
}

// every는 간단한 주기 실행기다. 종료 신호를 받으면 진행 중인 실행이 끝난 뒤 멈춘다.
func (a *App) every(ctx context.Context, name string, d time.Duration, fn func(context.Context) error) {
	a.workersWG.Add(1)
	go func() {
		defer a.workersWG.Done()
		t := time.NewTicker(d)
		defer t.Stop()
		for {
			if err := fn(ctx); err != nil && ctx.Err() == nil {
				a.log.Warn("periodic task failed", "task", name, "err", err.Error())
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

// integrationStatus는 환경변수로 정해진 연동 설정을 요약한다 (비밀값 제외).
func (a *App) integrationStatus() httpapi.IntegrationStatus {
	var st httpapi.IntegrationStatus
	c := a.cfg
	st.Mode = string(c.IntegrationMode)
	st.PublishingEnabled = c.PublishingEnabled
	mock := c.IntegrationMode == config.ModeMock
	st.X.Configured = mock || c.X.Configured()
	st.AI.Configured = mock || c.AI.Configured()
	st.AI.Provider = "openai"
	if mock {
		st.AI.Provider = "mock"
	}
	st.AI.TextModel, st.AI.ImageModel = c.AI.TextModel, c.AI.ImageModel
	st.R2.Configured = mock || c.R2.Configured()
	st.R2.Bucket, st.R2.Prefix, st.R2.PublicBaseURL = c.R2.Bucket, c.R2.Prefix, c.R2.PublicBaseURL
	st.R2.DevURL = strings.Contains(c.R2.PublicBaseURL, ".r2.dev")
	return st
}
