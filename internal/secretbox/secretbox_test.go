package secretbox

import (
	"bytes"
	"testing"
)

func TestSealOpenAndAADBinding(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	b, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := b.Seal([]byte("refresh-token"), "x:refresh")
	if err != nil {
		t.Fatal(err)
	}
	pt, err := b.Open(ct, "x:refresh")
	if err != nil || string(pt) != "refresh-token" {
		t.Fatalf("open = %q %v", pt, err)
	}
	if _, err := b.Open(ct, "x:access"); err == nil {
		t.Fatal("aad mismatch must fail")
	}
	other, _ := New(bytes.Repeat([]byte{8}, 32))
	if _, err := other.Open(ct, "x:refresh"); err == nil {
		t.Fatal("wrong key must fail")
	}
}
