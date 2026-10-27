package auth

import (
	"strings"
	"testing"
)

func TestHashAndVerify(t *testing.T) {
	p := Params{Memory: 8 * 1024, Time: 1, Threads: 1, KeyLen: 32, SaltLen: 16}
	h, err := HashPassword("correct horse battery", p)
	if err != nil || !strings.HasPrefix(h, "$argon2id$v=19$m=8192,t=1,p=1$") {
		t.Fatalf("hash = %q %v", h, err)
	}
	if ok, err := VerifyPassword("correct horse battery", h); !ok || err != nil {
		t.Fatalf("verify good = %v %v", ok, err)
	}
	if ok, _ := VerifyPassword("wrong password!", h); ok {
		t.Fatal("wrong password accepted")
	}
	if _, err := VerifyPassword("x", "$2a$10$bcrypt"); err == nil {
		t.Fatal("non-argon2 hash must be rejected")
	}
	if _, err := HashPassword("short", p); err == nil {
		t.Fatal("short password must be rejected")
	}
}
