// Package integration은 외부 연동(X·AI·R2) 공통 오류 분류를 정의한다.
// 실제 어댑터와 테스트 대역은 하위 패키지(xapi·ai·storage)에 있다.
package integration

import (
	"errors"
	"fmt"
	"time"
)

// Class는 실패를 어떻게 다룰지 정한다 (지시서 12.3).
type Class string

const (
	Retryable Class = "retryable" // 일시 오류. 제한된 backoff로 다시 시도
	Config    Class = "config"    // 인증 만료·권한 부족·설정 누락. 사람이 고칠 때까지 재시도하지 않음
	Review    Class = "review"    // 잘못된 콘텐츠 (길이 초과·거부). 수정 필요
	Unknown   Class = "unknown"   // 요청은 보냈지만 결과가 불명확. 자동 재시도 금지
)

// Error는 분류·사용자용 메시지·재시도 대기 시간을 담는다.
type Error struct {
	Class      Class
	Provider   string
	Op         string
	HTTPStatus int
	Message    string // 사용자에게 보여 줄 수 있는 한국어 설명 (비밀 제거)
	RetryAfter time.Duration
	RequestID  string
	Err        error
}

func (e *Error) Error() string {
	s := fmt.Sprintf("%s %s: %s", e.Provider, e.Op, e.Message)
	if e.HTTPStatus != 0 {
		s += fmt.Sprintf(" (HTTP %d)", e.HTTPStatus)
	}
	return s
}

func (e *Error) Unwrap() error { return e.Err }

// ClassOf는 오류 분류를 돌려준다. 분류되지 않은 오류는 재시도 가능으로 본다.
func ClassOf(err error) Class {
	var e *Error
	if errors.As(err, &e) {
		return e.Class
	}
	return Retryable
}

// ErrNotConfigured는 연동 값이 없을 때 쓴다. 목업으로 바꿔 성공한 척하지 않는다.
func ErrNotConfigured(provider string) *Error {
	return &Error{Class: Config, Provider: provider, Op: "configure", Message: provider + " 연결 설정이 없습니다. 설정 화면을 확인하세요"}
}
