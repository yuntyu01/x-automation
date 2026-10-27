// Package logx는 구조화 로그를 만들고 비밀로 보이는 값을 가린다.
package logx

import (
	"io"
	"log/slog"
	"regexp"
	"strings"
)

var sensitiveKey = regexp.MustCompile(`(?i)(password|passwd|secret|token|authorization|cookie|api[_-]?key|credential|code_verifier)`)

// Bearer 토큰·쿼리스트링 비밀처럼 문자열 안에 섞인 값을 가린다
var inlineSecret = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]{8,}|((?:access_token|refresh_token|client_secret|code|api_key)=)[^&\s"]+`)

// Redact는 문자열 안의 토큰 모양 값을 가린다. 외부 응답 본문을 오류에 담을 때 쓴다.
func Redact(s string) string {
	return inlineSecret.ReplaceAllStringFunc(s, func(m string) string {
		if i := strings.IndexAny(m, " ="); i >= 0 {
			return m[:i+1] + "[가림]"
		}
		return "[가림]"
	})
}

func New(w io.Writer, level string) *slog.Logger {
	var lv slog.Level
	switch level {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: lv,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if sensitiveKey.MatchString(a.Key) {
				return slog.String(a.Key, "[가림]")
			}
			if a.Value.Kind() == slog.KindString {
				return slog.String(a.Key, Redact(a.Value.String()))
			}
			return a
		},
	}))
}
