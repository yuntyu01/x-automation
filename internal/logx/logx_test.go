package logx

import (
	"bytes"
	"strings"
	"testing"
)

func TestSensitiveValuesAreMasked(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, "info")
	l.Info("call", "access_token", "abc", "err", "upstream said Bearer abcdefghijkl and refresh_token=xyz123")
	out := buf.String()
	for _, leak := range []string{"abcdefghijkl", "xyz123", `"access_token":"abc"`} {
		if strings.Contains(out, leak) {
			t.Fatalf("leaked %q in %s", leak, out)
		}
	}
}
