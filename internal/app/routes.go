package app

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yuntyu01/x-automation/internal/auth"
	"github.com/yuntyu01/x-automation/internal/httpapi"
	"github.com/yuntyu01/x-automation/internal/settings"
)

// Version은 빌드 때 -ldflags로 넣는다.
var Version = "dev"

// services는 DB가 준비된 뒤 만드는 도메인 서비스 묶음이다.
type services struct {
	auth     *auth.Service
	settings *settings.Repo
}

func (a *App) newServices(pool *pgxpool.Pool) *services {
	return &services{
		auth: auth.NewService(pool, auth.Config{
			Username: a.cfg.AdminUsername, PasswordHash: a.cfg.AdminPasswordHash, TTL: a.cfg.SessionTTL,
		}, a.deps.Clock),
		settings: settings.NewRepo(pool),
	}
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
		Auth:     svc.auth,
		Settings: svc.settings,
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
