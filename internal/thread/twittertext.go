package thread

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/dlclark/regexp2"
)

// 공식 twitter-text(JavaScript)의 parseTweet·twemoji-parser를 그대로 옮긴 것이다 (URL 추출은 url.go).
// JavaScript 문자열은 UTF-16이므로 위치·길이는 UTF-16 단위로 센다. 정규식 원문은 twittertext_gen.go(공식 라이브러리에서 생성)에 있다.

// 이모지 정규식(twemoji-parser)은 lookahead가 있어 regexp2로 실행한다. 고정된 시퀀스의 대안이라 역추적이 깊지 않다.
var reEmoji = compileJS(codePointPattern(srcEmoji), flagsEmoji) // 코드 포인트 배열 위에서 실행

func compileJS(src, flags string) *regexp2.Regexp {
	opt := regexp2.RegexOptions(regexp2.ECMAScript)
	if strings.Contains(flags, "i") {
		opt |= regexp2.IgnoreCase
	}
	r := regexp2.MustCompile(src, opt)
	r.MatchTimeout = 2 * time.Second // 비정상 입력에서 역추적이 길어져도 요청을 붙잡지 않는다
	return r
}

// codePointPattern은 JavaScript(UTF-16) 정규식의 서로게이트 표기를 코드 포인트 문자로 바꾼다.
//
//	\uD83C[\uDFFB-\uDFFF]  →  [🏻-🏿]      \uD83D\uDE00  →  😀
//
// twemoji 정규식은 이렇게 바꿔 코드 포인트 배열 위에서 실행한다. UTF-16 단위 그대로 실행하면 regexp2의 최적화가
// "\uD83C[..]|[..]" 뒤의 선택 그룹을 건너뛰고(피부색·ZWJ 시퀀스를 놓친다), 이스케이프로 쓰면 문자 집합 안에서
// 쌍을 잘못 합치는 경우가 있어 문자 그대로 넣는다. 결과는 golden.json의 이모지 표로 전부 대조한다.
var (
	reHighClass = regexp.MustCompile(`\\u([dD][89abAB][0-9a-fA-F]{2})\[((?:\\u[dD][c-fC-F][0-9a-fA-F]{2}(?:-\\u[dD][c-fC-F][0-9a-fA-F]{2})?)+)\]`)
	reLowItem   = regexp.MustCompile(`\\u([dD][c-fC-F][0-9a-fA-F]{2})(?:-\\u([dD][c-fC-F][0-9a-fA-F]{2}))?`)
	rePair      = regexp.MustCompile(`\\u([dD][89abAB][0-9a-fA-F]{2})\\u([dD][c-fC-F][0-9a-fA-F]{2})`)
	reSurrogate = regexp.MustCompile(`\\u[dD][89a-fA-F][0-9a-fA-F]{2}`)
)

func hex16(s string) rune {
	v := 0
	for _, c := range strings.ToLower(s) {
		v = v*16 + strings.IndexRune("0123456789abcdef", c)
	}
	return rune(v)
}

func pairChar(hi, lo string) string { return string(utf16.DecodeRune(hex16(hi), hex16(lo))) }

func codePointPattern(src string) string {
	out := reHighClass.ReplaceAllStringFunc(src, func(m string) string {
		sub := reHighClass.FindStringSubmatch(m)
		var b strings.Builder
		b.WriteByte('[')
		for _, it := range reLowItem.FindAllStringSubmatch(sub[2], -1) {
			b.WriteString(pairChar(sub[1], it[1]))
			if it[2] != "" {
				b.WriteString("-" + pairChar(sub[1], it[2]))
			}
		}
		b.WriteByte(']')
		return b.String()
	})
	out = rePair.ReplaceAllStringFunc(out, func(m string) string {
		sub := rePair.FindStringSubmatch(m)
		return pairChar(sub[1], sub[2])
	})
	if left := reSurrogate.FindString(out); left != "" {
		panic("twemoji pattern has an unpaired surrogate escape: " + left)
	}
	return out
}

// u16은 JavaScript 문자열처럼 UTF-16 코드 단위를 하나씩 담은 배열이다 (서로게이트도 각각 한 칸).
type u16 []rune

func toU16(s string) u16 {
	units := utf16.Encode([]rune(s))
	out := make(u16, len(units))
	for i, c := range units {
		out[i] = rune(c)
	}
	return out
}

func (t u16) String() string {
	units := make([]uint16, len(t))
	for i, c := range t {
		units[i] = uint16(c)
	}
	return string(utf16.Decode(units))
}

// emojis16은 twemoji-parser parse와 같다. 위치는 UTF-16 단위로 돌려준다.
func emojis16(t u16) []entity {
	cps := []rune(t.String())
	pos := make([]int, len(cps)+1) // 코드 포인트 위치 → UTF-16 위치
	for i, r := range cps {
		pos[i+1] = pos[i] + utf16.RuneLen(r)
	}
	var out []entity
	m, err := reEmoji.FindRunesMatch(cps)
	for ; err == nil && m != nil; m, err = reEmoji.FindNextMatch(m) {
		s, e := pos[m.Index], pos[m.Index+m.Length]
		out = append(out, entity{Start: s, End: e, Length: e - s})
	}
	return out
}

// Parsed는 한 게시물의 길이 판정 결과다 (twitter-text parseTweet).
type Parsed struct {
	WeightedLength int  `json:"weighted_length"`
	Valid          bool `json:"valid"`
	Permillage     int  `json:"permillage"`
	Limit          int  `json:"limit"`
	ValidRangeEnd  int  `json:"-"` // UTF-16 위치 (원문 기준)
}

func (c Config) charWeight(unit rune) int {
	for _, r := range c.Ranges {
		if int(unit) >= r.start && int(unit) <= r.end {
			return r.weight
		}
	}
	return c.DefaultWeight
}

// parseTweet은 twitter-text parseTweet을 그대로 옮긴 것이다. limit은 maxWeightedTweetLength다.
func (c Config) parseTweet(text string, limit int) Parsed {
	normalized := Normalize(text)
	t := toU16(normalized)
	urls := map[int]entity{}
	for _, e := range extractURLEntities(normalized) {
		urls[e.Start] = e
	}
	emojis := map[int]entity{}
	if c.EmojiParsing {
		for _, e := range emojis16(t) {
			emojis[e.Start] = e
		}
	}
	weighted, validIndex, valid := 0, 0, true
	for i := 0; i < len(t); i++ {
		if u, ok := urls[i]; ok {
			weighted += c.URLLength * c.Scale
			i += u.Length - 1
		} else if e, ok := emojis[i]; c.EmojiParsing && ok {
			weighted += c.DefaultWeight
			i += e.Length - 1
		} else {
			if i < len(t)-1 && t[i] >= 0xD800 && t[i] <= 0xDBFF && t[i+1] >= 0xDC00 && t[i+1] <= 0xDFFF {
				i++
			}
			weighted += c.charWeight(t[min(i, len(t)-1)])
		}
		if valid && i < len(t) {
			valid = !invalidCharsRe.MatchString(string(t[i])) // 홀로 남은 서로게이트는 U+FFFD가 되어 맞지 않는다 (JS와 같다)
		}
		if valid && weighted <= limit*c.Scale {
			validIndex = i
		}
	}
	w := weighted / c.Scale
	valid = valid && w > 0 && w <= limit
	p := Parsed{WeightedLength: w, Valid: valid, Limit: limit, Permillage: w * 1000 / max(limit, 1)}
	p.ValidRangeEnd = validIndex + len(toU16(text)) - len(t)
	return p
}

// Parse는 twitter-text parseTweet과 같은 판정을 한다.
func Parse(text string, c Config) Parsed { return c.parseTweet(text, c.MaxWeighted) }

// ParseLimit은 한도를 바꿔 판정한다 (장문 게시).
func ParseLimit(text string, c Config, limit int) Parsed { return c.parseTweet(text, limit) }

// WeightedLength는 가중 글자 수만 돌려준다.
func WeightedLength(text string) int { return Parse(text, V3).WeightedLength }

// Span은 바이트 위치 [Start, End)다.
type Span struct{ Start, End int }

// ExtractURLs는 X가 링크로 인식하는 URL의 바이트 위치다 (text는 정규화된 상태여야 한다).
func ExtractURLs(text string) []Span {
	idx := u16ToByte(text)
	var out []Span
	for _, e := range extractURLEntities(text) {
		out = append(out, Span{idx[e.Start], idx[e.End]})
	}
	return out
}

// HasURL은 링크가 있는지다 (X 게시 단가가 달라질 수 있어 화면에 알린다).
func HasURL(text string) bool { return len(ExtractURLs(Normalize(text))) > 0 }

// u16ToByte는 UTF-16 위치 → 바이트 위치 표다 (길이 len(u16)+1). 서로게이트 중간 위치는 그 문자의 시작으로 본다.
func u16ToByte(s string) []int {
	out := make([]int, 0, len(s)+1)
	for b, r := range s {
		out = append(out, b)
		if r >= 0x10000 {
			out = append(out, b)
		}
	}
	return append(out, len(s))
}

// ── punycode (RFC 3492) ───────────────────────────────────────────────

// punycodeASCIILen은 JavaScript punycode.toASCII(label) 결과의 길이다 (idna.toAscii의 라벨 길이 검사용).
func punycodeASCIILen(label string) int {
	prefix := 0
	if at := strings.Index(label, "@"); at >= 0 {
		// punycode.js mapDomain: '@' 앞은 그대로, 뒤만 도메인으로 본다
		parts := strings.Split(label, "@")
		prefix = len(toU16(parts[0])) + 1
		label = parts[1]
	}
	label = strings.NewReplacer("。", ".", "．", ".", "｡", ".").Replace(label)
	n := prefix
	for i, l := range strings.Split(label, ".") {
		if i > 0 {
			n++
		}
		ascii := true
		for j := 0; j < len(l); j++ {
			if l[j] >= utf8.RuneSelf {
				ascii = false
				break
			}
		}
		if ascii {
			n += len(l)
		} else {
			n += 4 + len(punycodeEncode([]rune(l)))
		}
	}
	return n
}

func punycodeEncode(input []rune) string {
	const (
		base, tMin, tMax, skew, damp = 36, 1, 26, 38, 700
		initialBias, initialN        = 72, 128
	)
	adapt := func(delta, numPoints int, first bool) int {
		if first {
			delta /= damp
		} else {
			delta /= 2
		}
		delta += delta / numPoints
		k := 0
		for delta > ((base-tMin)*tMax)/2 {
			delta /= base - tMin
			k += base
		}
		return k + (base-tMin+1)*delta/(delta+skew)
	}
	digit := func(d int) byte {
		if d < 26 {
			return byte('a' + d)
		}
		return byte('0' + d - 26)
	}
	var out []byte
	for _, r := range input {
		if r < 0x80 {
			out = append(out, byte(r))
		}
	}
	b := len(out)
	h := b
	if b > 0 {
		out = append(out, '-')
	}
	n, delta, bias := initialN, 0, initialBias
	for h < len(input) {
		m := int(^uint(0) >> 1)
		for _, r := range input {
			if int(r) >= n && int(r) < m {
				m = int(r)
			}
		}
		delta += (m - n) * (h + 1)
		n = m
		for _, r := range input {
			if int(r) < n {
				delta++
			}
			if int(r) == n {
				q := delta
				for k := base; ; k += base {
					t := k - bias
					if t < tMin {
						t = tMin
					} else if t > tMax {
						t = tMax
					}
					if q < t {
						break
					}
					out = append(out, digit(t+(q-t)%(base-t)))
					q = (q - t) / (base - t)
				}
				out = append(out, digit(q))
				bias = adapt(delta, h+1, h == b)
				delta = 0
				h++
			}
		}
		delta++
		n++
	}
	return string(out)
}
