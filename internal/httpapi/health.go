package httpapi

import (
	"context"
	"net/http"
	"time"
)

// ReadyChecker는 DB 연결과 마이그레이션 적용 여부를 확인한다.
type ReadyChecker func(ctx context.Context) bool

// Healthz는 프로세스 생존만 확인한다. 외부 API 장애로 실패하지 않는다 (재시작 루프 방지).
func Healthz(w http.ResponseWriter, _ *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Readyz는 DB·마이그레이션 준비를 확인한다. 공개 응답에 상세 설정을 싣지 않는다.
func Readyz(check ReadyChecker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if check == nil || !check(ctx) {
			WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	}
}
