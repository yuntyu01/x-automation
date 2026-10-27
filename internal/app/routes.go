package app

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yuntyu01/x-automation/internal/auth"
	"github.com/yuntyu01/x-automation/internal/budget"
	"github.com/yuntyu01/x-automation/internal/config"
	"github.com/yuntyu01/x-automation/internal/connection"
	"github.com/yuntyu01/x-automation/internal/content"
	"github.com/yuntyu01/x-automation/internal/generation"
	"github.com/yuntyu01/x-automation/internal/httpapi"
	"github.com/yuntyu01/x-automation/internal/integration/ai"
	"github.com/yuntyu01/x-automation/internal/integration/storage"
	"github.com/yuntyu01/x-automation/internal/jobs"
	"github.com/yuntyu01/x-automation/internal/media"
	"github.com/yuntyu01/x-automation/internal/notification"
	"github.com/yuntyu01/x-automation/internal/prompt"
	"github.com/yuntyu01/x-automation/internal/schedule"
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
	budget        *budget.Service
	queue         *jobs.Queue
	generation    *generation.Service
	planner       *generation.Planner
	schedule      *schedule.Service
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
	s.budget = budget.NewService(pool, a.deps.Clock, setRepo)
	s.queue = jobs.NewQueue(pool, a.deps.Clock)
	s.generation = generation.NewService(generation.Deps{Pool: pool, Clock: a.deps.Clock, Queue: s.queue, Provider: a.newAIProvider(),
		Settings: setRepo, Prompts: s.prompts, Contents: s.contents, Media: s.media, Budget: s.budget, Notify: s.notifications, Log: a.log})
	s.planner = generation.NewPlanner(pool, a.deps.Clock, setRepo, s.generation, s.notifications, a.log)
	s.schedule = schedule.NewService(pool, a.deps.Clock, a.deps.Rand, setRepo, s.notifications)
	s.generation.SetAssigner(s.schedule) // 예약에 묶인 생성·"생성 후 빈 예약에 배치"
	return s
}

// newAIProvider는 AI 공급자를 고른다. 목업은 결정적 대역, 실제 연동은 OpenAI(키가 없으면 설정 오류를 돌려주는 대역).
func (a *App) newAIProvider() ai.Provider {
	if a.cfg.IntegrationMode == config.ModeMock {
		return &ai.Mock{}
	}
	if !a.cfg.AI.Configured() {
		return ai.Unconfigured{}
	}
	p, err := ai.NewOpenAI(a.cfg.AI.APIKey, a.cfg.AI.BaseURL, a.cfg.AI.TextModel, a.cfg.AI.ImageModel, nil)
	if err != nil {
		a.log.Error("ai provider unavailable", "err", err.Error())
		return ai.Unconfigured{}
	}
	return p
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
		Generation:    svc.generation,
		Planner:       svc.planner,
		Budget:        svc.budget,
		Schedule:      svc.schedule,
	}
	return srv.Routes()
}

// startWorkers는 주기 작업과 작업 큐 워커를 시작한다. 단계마다 워커가 추가된다.
func (a *App) startWorkers(ctx context.Context, _ *pgxpool.Pool) {
	svc := a.svc
	a.every(ctx, "auth-cleanup", time.Hour, func(ctx context.Context) error { return svc.auth.Cleanup(ctx) })

	concurrency := 2
	if st, err := svc.settings.Get(ctx); err == nil {
		concurrency = st.Policy.Generation.MaxConcurrent
	}
	gw := &jobs.Worker{Queue: svc.queue, Concurrency: concurrency, Lease: 2 * time.Minute, Poll: 5 * time.Second, Log: a.log,
		Handlers: map[string]jobs.Handler{generation.JobKind: svc.generation.HandleItem}}
	svc.generation.SetKick(gw.Kick)
	a.workersWG.Add(1)
	go func() {
		defer a.workersWG.Done()
		gw.Run(ctx)
	}()
	// 시간표(예약 채우기·배치·지난 예약 정리)를 먼저 맞춘 뒤 생성 부족분을 판단한다
	a.every(ctx, "schedule-and-planner", time.Minute, func(ctx context.Context) error {
		if err := svc.schedule.Tick(ctx); err != nil {
			return err
		}
		return svc.planner.Tick(ctx)
	})
	// lease가 끝난 작업 정리: 생성은 다시 해도 외부 중복이 없어 다시 대기시킨다 (게시 작업은 10단계에서 결과 확인 필요로 분류)
	a.every(ctx, "jobs-recover", time.Minute, func(ctx context.Context) error {
		_, _, err := svc.queue.RecoverExpired(ctx, recoverPolicy)
		if err != nil {
			return err
		}
		_, err = svc.budget.ReleaseStale(ctx, 6*time.Hour)
		return err
	})
}

func recoverPolicy(kind string) jobs.RecoverPolicy {
	if kind == generation.JobKind {
		return jobs.RecoverRequeue
	}
	return jobs.RecoverUnknown
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
