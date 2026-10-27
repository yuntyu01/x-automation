// Package thread는 X 가중 글자 수 계산과 본문·자기 답글 분할 규칙을 담당한다.
//
// 가중 글자 수는 twitter-text config v3을 따른다 (docs/INTEGRATIONS.md).
//   - 텍스트를 NFC로 정규화한 뒤 코드 포인트 단위로 센다
//   - U+0000–U+10FF, U+2000–U+200D, U+2010–U+201F, U+2032–U+2037은 1, 나머지(한글·CJK 등)는 2
//   - 이모지 시퀀스(ZWJ·피부색·국기·키캡 포함)는 하나당 2
//   - 인식된 URL은 길이와 관계없이 23
//
// 프론트(web/src/lib/thread)는 공식 twitter-text 라이브러리를 쓰고, 두 구현은 같은 fixture로 대조한다.
package thread

import (
	"unicode/utf8"

	"github.com/rivo/uniseg"
	"golang.org/x/text/unicode/norm"
)

type weightRange struct{ start, end, weight int }

// Config는 twitter-text 설정이다.
type Config struct {
	MaxWeighted   int
	Scale         int
	DefaultWeight int
	URLLength     int
	Ranges        []weightRange
	EmojiParsing  bool
}

// V3은 twitter-text config/v3.json 값이다.
var V3 = Config{
	MaxWeighted:   280,
	Scale:         100,
	DefaultWeight: 200,
	URLLength:     23,
	Ranges: []weightRange{
		{0, 4351, 100},
		{8192, 8205, 100},
		{8208, 8223, 100},
		{8242, 8247, 100},
	},
	EmojiParsing: true,
}

// LongPostLimit는 장문 게시 한도다 (계정·API 능력이 확인된 경우만 쓴다).
const LongPostLimit = 25000

func (c Config) runeWeight(r rune) int {
	cp := int(r)
	for _, rg := range c.Ranges {
		if cp >= rg.start && cp <= rg.end {
			return rg.weight
		}
	}
	return c.DefaultWeight
}

// invalidRune은 게시할 수 없는 문자다 (twitter-text invalid_chars).
func invalidRune(r rune) bool {
	return r == 0xFFFE || r == 0xFEFF || r == 0xFFFF
}

// Parsed는 한 게시물의 길이 판정 결과다.
type Parsed struct {
	WeightedLength int  `json:"weighted_length"`
	Valid          bool `json:"valid"`
	Permillage     int  `json:"permillage"`
	Limit          int  `json:"limit"`
}

// unit은 길이를 셀 때 쪼개지지 않는 단위(문자소 또는 URL 전체)다. 바이트 오프셋 기준.
type unit struct {
	start, end int
	weight     int // scale 곱한 값
	url        bool
	invalid    bool
}

// units는 NFC 정규화된 text를 길이 단위로 나눈다.
func (c Config) units(text string) []unit {
	urls := ExtractURLs(text)
	var out []unit
	ui := 0
	g := uniseg.NewGraphemes(text)
	for g.Next() {
		start, end := g.Positions()
		if ui < len(urls) && start >= urls[ui].Start && start < urls[ui].End {
			if start == urls[ui].Start {
				out = append(out, unit{start: urls[ui].Start, end: urls[ui].End, weight: c.URLLength * c.Scale, url: true})
			} else {
				// URL 안쪽 문자소는 URL 단위에 이미 포함됐다. 경계가 문자소 중간이면 끝을 늘린다
				if end > out[len(out)-1].end {
					out[len(out)-1].end = end
				}
			}
			if end >= urls[ui].End {
				ui++
			}
			continue
		}
		cluster := text[start:end]
		w, inv := c.clusterWeight(cluster)
		out = append(out, unit{start: start, end: end, weight: w, invalid: inv})
	}
	return out
}

func (c Config) clusterWeight(cluster string) (int, bool) {
	inv := false
	for _, r := range cluster {
		if invalidRune(r) {
			inv = true
		}
	}
	if c.EmojiParsing && isEmojiCluster(cluster) {
		return c.DefaultWeight, inv
	}
	w := 0
	for _, r := range cluster {
		w += c.runeWeight(r)
	}
	return w, inv
}

// isEmojiCluster는 문자소가 이모지 표현인지 판정한다.
// 그림 문자(Extended_Pictographic)이면서 텍스트 표현 강제(U+FE0E)가 없거나, 국기(지역 표시 문자 쌍)·키캡이면 이모지다.
func isEmojiCluster(cluster string) bool {
	first, size := utf8.DecodeRuneInString(cluster)
	if first == utf8.RuneError && size <= 1 {
		return false
	}
	rest := cluster[size:]
	// 국기: 지역 표시 문자 두 개
	if first >= 0x1F1E6 && first <= 0x1F1FF {
		second, _ := utf8.DecodeRuneInString(rest)
		return second >= 0x1F1E6 && second <= 0x1F1FF
	}
	// 키캡: [0-9#*] FE0F? 20E3
	if (first >= '0' && first <= '9') || first == '#' || first == '*' {
		for _, r := range rest {
			if r == 0x20E3 {
				return true
			}
		}
		return false
	}
	hasVS16 := false
	for _, r := range rest {
		if r == 0xFE0E {
			return false
		}
		if r == 0xFE0F {
			hasVS16 = true
		}
	}
	if !isPictographic(first) {
		return false
	}
	// 기본이 텍스트 표현인 그림 문자(©, ®, ™, ↔ 등 BMP 기호)는 FE0F가 있을 때만 이모지로 본다
	if first < 0x2000 || (first < 0x1F000 && !defaultEmojiPresentation(first)) {
		return hasVS16 || len(rest) > 0 && containsZWJ(rest)
	}
	return true
}

func containsZWJ(s string) bool {
	for _, r := range s {
		if r == 0x200D {
			return true
		}
	}
	return false
}

// isPictographic은 Extended_Pictographic의 주요 범위다.
func isPictographic(r rune) bool {
	switch {
	case r == 0x00A9 || r == 0x00AE || r == 0x203C || r == 0x2049 || r == 0x2122 || r == 0x2139:
		return true
	case r >= 0x2194 && r <= 0x21AA:
		return true
	case r >= 0x231A && r <= 0x23FF:
		return true
	case r == 0x24C2 || (r >= 0x25AA && r <= 0x25FE):
		return true
	case r >= 0x2600 && r <= 0x27BF:
		return true
	case r >= 0x2934 && r <= 0x2935, r >= 0x2B05 && r <= 0x2B55, r == 0x3030, r == 0x303D, r == 0x3297, r == 0x3299:
		return true
	case r >= 0x1F000 && r <= 0x1FAFF:
		return true
	}
	return false
}

// defaultEmojiPresentation은 FE0F 없이도 이모지로 표시되는 BMP 기호다 (Emoji_Presentation=Yes의 일부).
func defaultEmojiPresentation(r rune) bool {
	switch r {
	case 0x231A, 0x231B, 0x23E9, 0x23EA, 0x23EB, 0x23EC, 0x23F0, 0x23F3, 0x25FD, 0x25FE,
		0x2614, 0x2615, 0x2648, 0x2649, 0x264A, 0x264B, 0x264C, 0x264D, 0x264E, 0x264F, 0x2650, 0x2651, 0x2652, 0x2653,
		0x267F, 0x2693, 0x26A1, 0x26AA, 0x26AB, 0x26BD, 0x26BE, 0x26C4, 0x26C5, 0x26CE, 0x26D4, 0x26EA, 0x26F2, 0x26F3,
		0x26F5, 0x26FA, 0x26FD, 0x2705, 0x270A, 0x270B, 0x2728, 0x274C, 0x274E, 0x2753, 0x2754, 0x2755, 0x2757,
		0x2795, 0x2796, 0x2797, 0x27B0, 0x27BF, 0x2B1B, 0x2B1C, 0x2B50, 0x2B55:
		return true
	}
	return false
}

// Normalize는 길이 계산·게시 전에 쓰는 NFC 정규화다.
func Normalize(s string) string { return norm.NFC.String(s) }

// Parse는 twitter-text parseTweet과 같은 판정을 한다.
func Parse(text string, c Config) Parsed {
	return ParseLimit(text, c, c.MaxWeighted)
}

// ParseLimit은 한도를 바꿔 판정한다 (장문 게시).
func ParseLimit(text string, c Config, limit int) Parsed {
	t := Normalize(text)
	total := 0
	invalid := false
	for _, u := range c.units(t) {
		total += u.weight
		if u.invalid {
			invalid = true
		}
	}
	weighted := total / c.Scale
	p := Parsed{WeightedLength: weighted, Limit: limit}
	p.Permillage = weighted * 1000 / max(limit, 1)
	p.Valid = t != "" && !invalid && weighted <= limit && hasVisible(t)
	return p
}

func hasVisible(s string) bool {
	for _, r := range s {
		if r != ' ' && r != '\n' && r != '\t' && r != '\r' && r != 0x3000 {
			return true
		}
	}
	return false
}

// WeightedLength는 가중 글자 수만 돌려준다.
func WeightedLength(text string) int { return Parse(text, V3).WeightedLength }
