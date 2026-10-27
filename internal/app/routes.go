package app

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yuntyu01/x-automation/internal/httpapi"
)

// buildAPI는 /api/ 아래 라우트를 구성한다. 단계마다 기능 라우트가 추가된다.
func (a *App) buildAPI(_ *pgxpool.Pool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		httpapi.WriteError(w, r, httpapi.ErrNotFound)
	})
	return mux
}

// startWorkers는 예약·생성·정리 워커를 시작한다. 단계마다 워커가 추가된다.
func (a *App) startWorkers(_ any, _ *pgxpool.Pool) {}
