// Package thread는 X 가중 글자 수 계산과 본문·자기 답글 분할 규칙을 담당한다.
//
// 가중 글자 수는 공식 twitter-text(config v3)와 같다. 프론트(web/src/lib/thread)는 공식 JavaScript 라이브러리를 쓰고,
// 서버는 같은 정규식(twittertext_gen.go, 라이브러리에서 생성)을 UTF-16 단위로 실행하는 이식본(twittertext.go)을 쓴다.
// 두 구현은 testdata/thread/golden.json(라이브러리로 계산한 기대값)과 공식 적합성 사례로 대조한다.
//   - NFC 정규화 후 UTF-16 단위로 센다
//   - U+0000–U+10FF, U+2000–U+200D, U+2010–U+201F, U+2032–U+2037은 1, 나머지(한글·CJK·보충 평면)는 2
//   - twemoji-parser가 인식한 이모지는 하나당 2 (라이브러리가 모르는 최신 이모지 시퀀스는 코드 포인트마다 세어 더 길게 나온다)
//   - 인식된 URL은 길이와 관계없이 23
package thread

import (
	"github.com/rivo/uniseg"
	"golang.org/x/text/unicode/norm"
)

type weightRange struct{ start, end, weight int }

// Config는 twitter-text 설정이다. V2·V3 값은 twittertext_gen.go에 있다.
type Config struct {
	MaxWeighted   int
	Scale         int
	DefaultWeight int
	URLLength     int
	Ranges        []weightRange
	EmojiParsing  bool
}

// LongPostLimit는 장문 게시 한도다 (계정·API 능력이 확인된 경우만 쓴다).
const LongPostLimit = 25000

// Normalize는 길이 계산·게시 전에 쓰는 NFC 정규화다 (JavaScript String.prototype.normalize()와 같다).
func Normalize(s string) string { return norm.NFC.String(s) }

// unit은 분할할 때 쪼개지지 않는 단위다 (문자소, URL 전체, 이모지 전체). 바이트 위치.
type unit struct {
	start, end int
	weight     int // scale 곱한 값
	url        bool
}

// units는 정규화된 text를 parseTweet과 같은 순서로 셈한 조각으로 나누고, 문자소 경계에서만 끊기도록 합친다.
// 단위 가중치의 합은 Parse(text)의 가중 길이와 같다.
func (c Config) units(text string) []unit {
	t := toU16(text)
	idx := u16ToByte(text)
	urls := map[int]entity{}
	for _, e := range extractURLEntities(text) {
		urls[e.Start] = e
	}
	emojis := map[int]entity{}
	if c.EmojiParsing {
		for _, e := range emojis16(t) {
			emojis[e.Start] = e
		}
	}
	boundary := map[int]bool{len(text): true}
	g := uniseg.NewGraphemes(text)
	for g.Next() {
		s, _ := g.Positions()
		boundary[s] = true
	}
	var out []unit
	cur := unit{start: -1}
	flush := func(end int) {
		if cur.start >= 0 && end > cur.start {
			cur.end = end
			out = append(out, cur)
		}
		cur = unit{start: -1}
	}
	for i := 0; i < len(t); i++ {
		start := i
		w, isURL := 0, false
		if u, ok := urls[i]; ok {
			w, isURL = c.URLLength*c.Scale, true
			i += u.Length - 1
		} else if e, ok := emojis[i]; c.EmojiParsing && ok {
			w = c.DefaultWeight
			i += e.Length - 1
		} else {
			if i < len(t)-1 && t[i] >= 0xD800 && t[i] <= 0xDBFF && t[i+1] >= 0xDC00 && t[i+1] <= 0xDFFF {
				i++
			}
			w = c.charWeight(t[min(i, len(t)-1)])
		}
		if cur.start < 0 {
			cur.start = idx[min(start, len(t))]
		}
		cur.weight += w
		cur.url = cur.url || isURL
		end := idx[min(i+1, len(t))]
		if boundary[end] {
			flush(end)
		}
	}
	flush(len(text))
	return out
}
