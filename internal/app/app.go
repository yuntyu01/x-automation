// Package app은 설정·DB·연동·워커를 묶어 서버를 구성한다.
package app

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yuntyu01/x-automation/internal/clock"
	"github.com/yuntyu01/x-automation/internal/config"
	"github.com/yuntyu01/x-automation/internal/httpapi"
	"github.com/yuntyu01/x-automation/internal/integration/storage"
	"github.com/yuntyu01/x-automation/internal/integration/xapi"
	"github.com/yuntyu01/x-automation/internal/randx"
	"github.com/yuntyu01/x-automation/internal/store"
	"github.com/yuntyu01/x-automation/internal/web"
	"github.com/yuntyu01/x-automation/migrations"
)

// Deps는 테스트에서 바꿔 끼울 수 있는 의존성이다.
type Deps struct {
	Clock clock.Clock
	Rand  randx.Source
	// XClient는 테스트에서 가짜 X 서버에 붙인 클라이언트를 넣을 때 쓴다. 비우면 설정으로 고른다.
	XClient xapi.Client
}

type App struct {
	cfg  *config.Config
	log  *slog.Logger
	deps Deps

	pool  atomic.Pointer[pgxpool.Pool]
	api   atomic.Pointer[http.Handler]
	ready atomic.Bool

	svc       *services
	local     *storage.Local // 목업 모드 공개 이미지 경로(/media/)용
	workersWG sync.WaitGroup
	stopWork  context.CancelFunc
}

func New(cfg *config.Config, log *slog.Logger, deps Deps) *App {
	if deps.Clock == nil {
		deps.Clock = clock.System{}
	}
	if deps.Rand == nil {
		deps.Rand = randx.Default{}
	}
	return &App{cfg: cfg, log: log, deps: deps}
}

// Handler는 최상위 HTTP 핸들러다. API는 DB가 준비될 때까지 503을 돌려준다.
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", httpapi.Healthz)
	mux.HandleFunc("GET /readyz", httpapi.Readyz(a.checkReady))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		h := a.api.Load()
		if h == nil {
			httpapi.WriteError(w, r, httpapi.ErrNotReady)
			return
		}
		(*h).ServeHTTP(w, r)
	})
	// 목업 저장소의 공개 조회 경로. R2 공개 버킷과 같은 성격이라 로그인 없이 열린다
	mux.HandleFunc("GET /media/{key...}", a.serveLocalMedia)
	mux.Handle("/", web.Handler())
	return httpapi.Base(mux)
}

func (a *App) checkReady(ctx context.Context) bool {
	p := a.pool.Load()
	if p == nil || !a.ready.Load() {
		return false
	}
	if err := p.Ping(ctx); err != nil {
		return false
	}
	ok, _ := store.SchemaReady(ctx, p, migrations.FS)
	return ok
}

// Start는 DB 연결·마이그레이션을 백그라운드에서 재시도하고, 준비되면 API와 워커를 연다.
// DB가 잠시 없어도 프로세스를 죽이지 않는다 (재시작 루프 대신 /readyz 503).
func (a *App) Start(ctx context.Context) {
	go func() {
		backoff := time.Second
		for {
			if ctx.Err() != nil {
				return
			}
			pool, err := store.Open(ctx, a.cfg.DatabaseURL)
			if err != nil {
				a.log.Warn("db not reachable, retrying", "err", err.Error(), "retry_in", backoff.String())
				if !sleep(ctx, backoff) {
					return
				}
				backoff = min(backoff*2, 30*time.Second)
				continue
			}
			if a.cfg.MigrateOnStart {
				applied, err := store.Migrate(ctx, pool, migrations.FS)
				if err != nil {
					a.log.Error("migration failed; server stays not ready", "err", err.Error())
					pool.Close()
					if !sleep(ctx, time.Minute) {
						return
					}
					continue
				}
				if len(applied) > 0 {
					a.log.Info("migrations applied", "files", applied)
				}
			}
			if ok, _ := store.SchemaReady(ctx, pool, migrations.FS); !ok {
				a.log.Error("schema is behind this build; run migrations (MIGRATE_ON_START=false?)")
				pool.Close()
				if !sleep(ctx, time.Minute) {
					return
				}
				continue
			}
			a.pool.Store(pool)
			if err := a.open(ctx, pool); err != nil {
				a.log.Error("app init failed", "err", err.Error())
				a.pool.Store(nil)
				pool.Close()
				if !sleep(ctx, 30*time.Second) {
					return
				}
				continue
			}
			a.ready.Store(true)
			a.log.Info("app ready", "env", string(a.cfg.Env), "integration_mode", string(a.cfg.IntegrationMode),
				"publishing_enabled", a.cfg.PublishingEnabled)
			return
		}
	}()
}

// open은 서비스·API 라우터·워커를 만든다.
func (a *App) open(ctx context.Context, pool *pgxpool.Pool) error {
	api := a.buildAPI(pool)
	a.api.Store(&api)
	wctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	a.stopWork = cancel
	a.startWorkers(wctx, pool)
	return nil
}

// Shutdown은 새 작업 선점을 멈추고 진행 중인 작업이 결과를 저장할 시간을 준다.
func (a *App) Shutdown(ctx context.Context) {
	a.ready.Store(false)
	if a.stopWork != nil {
		a.stopWork()
	}
	done := make(chan struct{})
	go func() { a.workersWG.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		a.log.Warn("workers did not stop before deadline")
	}
	if p := a.pool.Load(); p != nil {
		p.Close()
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// OpenWithPool은 이미 마이그레이션된 풀로 바로 준비 상태를 만든다 (통합 테스트용).
func (a *App) OpenWithPool(ctx context.Context, pool *pgxpool.Pool) error {
	a.pool.Store(pool)
	if err := a.open(ctx, pool); err != nil {
		return err
	}
	a.ready.Store(true)
	return nil
}

func (a *App) serveLocalMedia(w http.ResponseWriter, r *http.Request) {
	if a.local == nil {
		http.NotFound(w, r)
		return
	}
	p, err := a.local.Path(r.PathValue("key"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// 디렉터리 목록은 보여 주지 않는다
	if st, err := os.Stat(p); err != nil || !st.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeFile(w, r, p)
}
