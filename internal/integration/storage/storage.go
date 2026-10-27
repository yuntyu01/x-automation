// Package storage는 이미지 원본·썸네일 저장소(R2 S3 호환)를 추상화한다.
package storage

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound는 객체가 없을 때. 삭제에서는 "이미 없음"으로 성공 처리한다.
var ErrNotFound = errors.New("object not found")

type Object struct {
	Key          string
	Size         int64
	LastModified time.Time
}

// Store는 앱 전용 접두사 안에서만 동작한다. 키는 접두사를 뺀 상대 경로다.
type Store interface {
	Put(ctx context.Context, key, contentType string, body []byte) error
	Get(ctx context.Context, key string) ([]byte, string, error)
	Delete(ctx context.Context, key string) error
	// List는 접두사 아래 객체를 페이지 단위로 돌려준다 (고아 파일 식별용).
	List(ctx context.Context, after string, limit int) ([]Object, string, error)
	// PublicURL은 공개 조회 주소다. 공개 주소가 없으면 빈 문자열.
	PublicURL(key string) string
	Name() string
}
