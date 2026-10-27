package thread

import (
	"math/rand/v2"
	"regexp"
	"strings"
	"testing"
)

var numberSuffixRe = regexp.MustCompile(`\n\(\d+/\d+\)$`)

func noSpace(s string) string { return strings.Join(strings.Fields(s), "") }

// 무작위 원문(한글·영문·이모지·URL·줄바꿈·공백)으로 분할 불변식을 확인한다 (완료 기준: 누락·중복 없음, 초과 부분은 게시 불가)
func TestSplitInvariantsOnRandomText(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 8))
	pieces := []string{
		"그날 밤 나는 오래된 편지를 다시 꺼내 읽었다. ", "창밖으로 비가 내리고 있었다.", "Hello world! ", "It was late.",
		"https://example.com/very/long/path?x=1&y=가", " naver.com ", "t.co/abc", "👨‍👩‍👧‍👦", "🇰🇷", "😀", "👍🏽", "1️⃣", "🫠",
		"\n", "\n\n", " ", "\t", "　", "가나다라마바사아자차카타파하", "“인용”이다.", "…", "a", "é", "\r\n",
	}
	for i := 0; i < 150; i++ {
		var b strings.Builder
		for j, n := 0, rng.IntN(160)+1; j < n; j++ {
			b.WriteString(pieces[rng.IntN(len(pieces))])
		}
		raw := b.String()
		norm := NormalizeForPost(raw)
		if norm == "" {
			continue
		}
		var entities []string
		for _, e := range extractURLEntities(norm) {
			entities = append(entities, toU16(norm)[e.Start:e.End].String())
		}
		for _, e := range emojis16(toU16(norm)) {
			entities = append(entities, toU16(norm)[e.Start:e.End].String())
		}
		for _, mode := range []Mode{ModeTwoParts, ModeOverflow} {
			for _, numbering := range []bool{false, true} {
				r := Split(raw, Options{Mode: mode, Numbering: numbering})
				var joined strings.Builder
				for _, p := range r.Parts {
					body := p.Text
					if numbering && len(r.Parts) > 1 {
						if !numberSuffixRe.MatchString(body) {
							t.Fatalf("case %d: numbering missing in %q", i, body)
						}
						body = numberSuffixRe.ReplaceAllString(body, "")
					}
					joined.WriteString(body)
					if got := Parse(p.Text, V3).WeightedLength; got != p.Weighted {
						t.Fatalf("case %d: part weight %d but parse says %d", i, p.Weighted, got)
					}
					if p.Weighted > 280 || !p.Valid {
						t.Fatalf("case %d mode %s numbering %v: part %d invalid (%d): %q", i, mode, numbering, p.Index, p.Weighted, p.Text)
					}
				}
				if noSpace(joined.String()) != noSpace(norm) {
					t.Fatalf("case %d mode %s: content changed\nwant %q\ngot  %q", i, mode, norm, joined.String())
				}
				// URL·이모지는 한 부분 안에 통째로 있어야 한다 (쪼개면 길이·표시가 달라진다)
				for _, e := range entities {
					found := false
					for _, p := range r.Parts {
						if strings.Contains(p.Text, e) {
							found = true
							break
						}
					}
					if !found {
						t.Fatalf("case %d mode %s: entity %q was cut", i, mode, e)
					}
				}
				if !r.Publishable {
					t.Fatalf("case %d: should be publishable: %v", i, r.Problems)
				}
			}
		}
	}
}
