package thread

import (
	"strings"
	"testing"
	"time"
)

// 긴 글·악의적 입력에서도 길이 계산이 오래 걸리지 않아야 한다 (요청마다 분할 미리보기를 계산한다)
func TestParseIsFastOnLongAndAdversarialInput(t *testing.T) {
	inputs := map[string]string{
		"long-korean": strings.Repeat("그날 밤 나는 오래된 편지를 다시 꺼내 읽었다. ", 400),
		"dots":        strings.Repeat("a.", 5000),
		"domain-like": strings.Repeat("abc-", 3000) + ".com",
		"urls":        strings.Repeat("https://example.com/path?x=1 ", 300),
		"emoji":       strings.Repeat("👨‍👩‍👧‍👦🏽", 1000),
		"parens":      "https://example.com/" + strings.Repeat("(", 2000) + strings.Repeat(")", 2000),
		"subdomains":  "http://" + strings.Repeat("a-", 2000) + "b.com",
	}
	for name, in := range inputs {
		start := time.Now()
		_ = Parse(in, V3)
		r := Split(in, Options{Mode: ModeTwoParts})
		if d := time.Since(start); d > 2*time.Second && !raceEnabled {
			t.Errorf("%s took %v (%d parts)", name, d, len(r.Parts))
		} else {
			t.Logf("%s %v parts=%d", name, d, len(r.Parts))
		}
	}
}
