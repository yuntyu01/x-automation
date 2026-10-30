// Package httpapi는 라우팅·요청 검증·일관된 JSON 오류 응답을 담당한다.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// Error는 API 오류 응답의 본문이다. 메시지는 한국어로 사용자에게 바로 보여 줄 수 있게 쓴다.
type Error struct {
	Status    int               `json:"-"`
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	Fields    map[string]string `json:"fields,omitempty"`
	Details   map[string]any    `json:"details,omitempty"` // 확인이 필요한 경고 목록 등
	RequestID string            `json:"request_id,omitempty"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func NewError(status int, code, msg string) *Error {
	return &Error{Status: status, Code: code, Message: msg}
}

// FieldError는 입력 옆에 표시할 필드 오류를 담는다.
func FieldError(fields map[string]string) *Error {
	return &Error{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Message: "입력값을 확인해 주세요", Fields: fields}
}

var (
	ErrUnauthorized = NewError(http.StatusUnauthorized, "unauthorized", "로그인이 필요합니다")
	ErrForbidden    = NewError(http.StatusForbidden, "forbidden", "이 작업을 할 권한이 없습니다")
	ErrNotFound     = NewError(http.StatusNotFound, "not_found", "요청한 항목을 찾을 수 없습니다")
	ErrNotReady     = NewError(http.StatusServiceUnavailable, "not_ready", "서버가 데이터베이스를 준비하는 중입니다. 잠시 후 다시 시도해 주세요")
	ErrTooLarge     = NewError(http.StatusRequestEntityTooLarge, "too_large", "요청이 너무 큽니다")
	ErrBadJSON      = NewError(http.StatusBadRequest, "bad_json", "요청 형식이 올바르지 않습니다")
	ErrConflict     = NewError(http.StatusConflict, "conflict", "다른 곳에서 먼저 변경되었습니다. 새로고침 후 다시 시도해 주세요")
)

type ctxKey int

const requestIDKey ctxKey = 1

func RequestID(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey).(string)
	return v
}

// WriteJSON은 민감한 응답이 캐시되지 않게 하고 JSON을 쓴다.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(v)
}

// WriteError는 오류를 일관된 형식으로 쓴다. 모르는 오류는 내부 정보를 숨기고 기록만 한다.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	var e *Error
	if !errors.As(err, &e) && errors.Is(err, context.Canceled) && r.Context().Err() != nil {
		// 브라우저가 화면을 옮겨 요청을 끊었다. 서버 오류가 아니니 오류 로그를 남기지 않는다 (응답은 아무도 받지 않는다)
		e = NewError(499, "canceled", "요청이 취소됐어요")
	}
	if e == nil && !errors.As(err, &e) {
		slog.ErrorContext(r.Context(), "unhandled error", "err", err.Error(), "path", r.URL.Path, "request_id", RequestID(r.Context()))
		e = NewError(http.StatusInternalServerError, "internal", "일시적인 오류가 발생했습니다. 잠시 후 다시 시도해 주세요")
	}
	out := *e
	out.RequestID = RequestID(r.Context())
	if out.Status == 0 {
		out.Status = http.StatusBadRequest
	}
	WriteJSON(w, out.Status, map[string]any{"error": out})
}

const defaultBodyLimit = 1 << 20

// DecodeJSON은 크기를 제한하고 모르는 필드를 거부하며 본문을 읽는다.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	return DecodeJSONLimit(w, r, dst, defaultBodyLimit)
}

func DecodeJSONLimit(w http.ResponseWriter, r *http.Request, dst any, limit int64) error {
	ct := r.Header.Get("Content-Type")
	if ct != "" && !strings.HasPrefix(ct, "application/json") {
		return NewError(http.StatusUnsupportedMediaType, "unsupported_media_type", "JSON으로 보내 주세요")
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return ErrTooLarge
		}
		if errors.Is(err, io.EOF) {
			return ErrBadJSON
		}
		e := *ErrBadJSON
		e.Message = "요청 형식이 올바르지 않습니다: " + err.Error()
		return &e
	}
	if dec.More() {
		return ErrBadJSON
	}
	return nil
}
