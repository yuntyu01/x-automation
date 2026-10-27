package storage

import (
	"context"

	"github.com/yuntyu01/x-automation/internal/integration"
)

// Unconfigured는 실제 연동 모드에서 R2 값이 없을 때 쓴다. 로컬 저장으로 몰래 바꾸지 않고 설정 오류를 돌려준다.
type Unconfigured struct{}

func (Unconfigured) Name() string { return "unconfigured" }
func (Unconfigured) Put(context.Context, string, string, []byte) error {
	return integration.ErrNotConfigured("R2")
}
func (Unconfigured) Get(context.Context, string) ([]byte, string, error) {
	return nil, "", integration.ErrNotConfigured("R2")
}
func (Unconfigured) Delete(context.Context, string) error { return integration.ErrNotConfigured("R2") }
func (Unconfigured) List(context.Context, string, int) ([]Object, string, error) {
	return nil, "", integration.ErrNotConfigured("R2")
}
func (Unconfigured) PublicURL(string) string { return "" }
