package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"regexp"
	"runtime/debug"
	"time"
)

var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{8,64}$`)

func newRequestID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// Base는 요청 ID·보안 헤더·패닉 복구·접근 로그를 붙인다.
func Base(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if !validRequestID.MatchString(id) {
			id = newRequestID()
		}
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		r = r.WithContext(ctx)
		h := w.Header()
		h.Set("X-Request-Id", id)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")

		rec := &statusRecorder{ResponseWriter: w, status: 200}
		start := time.Now()
		defer func() {
			if p := recover(); p != nil {
				slog.ErrorContext(ctx, "panic", "panic", p, "stack", string(debug.Stack()), "request_id", id)
				WriteError(rec, r, NewError(http.StatusInternalServerError, "internal", "일시적인 오류가 발생했습니다"))
			}
			if r.URL.Path != "/healthz" {
				slog.InfoContext(ctx, "http", "method", r.Method, "path", r.URL.Path, "status", rec.status,
					"ms", time.Since(start).Milliseconds(), "request_id", id)
			}
		}()
		next.ServeHTTP(rec, r)
	})
}
