// Package secretbox는 DB에 저장하는 비밀(X 갱신 토큰 등)을 AES-256-GCM으로 암호화한다.
// 키는 TOKEN_ENCRYPTION_KEY이며 재배포마다 바꾸면 기존 값을 읽을 수 없다.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
)

type Box struct{ aead cipher.AEAD }

func New(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, errors.New("암호화 키는 32바이트여야 합니다")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal은 nonce를 앞에 붙인 암호문을 돌려준다. aad는 용도 구분(예: "x:refresh")으로 다른 칸에 옮겨 쓰는 것을 막는다.
func (b *Box) Seal(plain []byte, aad string) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return b.aead.Seal(nonce, nonce, plain, []byte(aad)), nil
}

var ErrDecrypt = errors.New("저장된 비밀을 풀 수 없습니다 (TOKEN_ENCRYPTION_KEY가 바뀌었을 수 있습니다)")

func (b *Box) Open(sealed []byte, aad string) ([]byte, error) {
	n := b.aead.NonceSize()
	if len(sealed) < n {
		return nil, ErrDecrypt
	}
	out, err := b.aead.Open(nil, sealed[:n], sealed[n:], []byte(aad))
	if err != nil {
		return nil, ErrDecrypt
	}
	return out, nil
}
