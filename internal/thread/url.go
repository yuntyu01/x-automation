package thread

import (
	"regexp"
	"strings"
)

// Span은 바이트 오프셋 구간이다.
type Span struct{ Start, End int }

// 프로토콜이 있는 URL: 앞 글자 제한 + http(s):// + 공백·꺾쇠가 아닌 문자들
var protoURL = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9@＠$#＃])(https?://[^\s<>"'\x{3000}]+)`)

// 프로토콜 없는 URL: 도메인.최상위도메인(+경로)
var bareURL = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9@＠$#＃.\-_/])((?:[a-z0-9](?:[a-z0-9\-]*[a-z0-9])?\.)+([a-z]{2,24}|xn--[a-z0-9\-]+)(?::\d{1,5})?(?:/[^\s<>"'\x{3000}]*)?)`)

// 흔히 쓰는 최상위 도메인. 08단계에서 twitter-text 목록 전체로 확장한다
var knownTLD = map[string]bool{}

func init() {
	for _, t := range strings.Fields(`com net org edu gov mil int info biz name pro mobi app dev io co ai me tv xyz site online store tech blog shop page link club live news art kr jp cn us uk de fr it es ru br in au ca nl se no fi dk pl ch at be cz gr hu ie il mx nz pt ro sg tr tw ua vn za hk id my ph th sa ae ly fm gg to`) {
		knownTLD[t] = true
	}
}

// ExtractURLs는 글자 수 계산에서 23자로 세는 URL 구간을 찾는다.
func ExtractURLs(text string) []Span {
	var out []Span
	for _, m := range protoURL.FindAllStringSubmatchIndex(text, -1) {
		s, e := m[2], m[3]
		e = trimURLEnd(text, s, e)
		out = append(out, Span{s, e})
	}
	for _, m := range bareURL.FindAllStringSubmatchIndex(text, -1) {
		s, e := m[2], m[3]
		tld := strings.ToLower(text[m[4]:m[5]])
		if !knownTLD[tld] && !strings.HasPrefix(tld, "xn--") {
			continue
		}
		e = trimURLEnd(text, s, e)
		if overlaps(out, s, e) {
			continue
		}
		out = append(out, Span{s, e})
	}
	sortSpans(out)
	return out
}

// trimURLEnd는 URL 끝의 문장 부호·닫는 괄호(짝이 없는 것)를 뺀다.
func trimURLEnd(text string, s, e int) int {
	for e > s {
		c := text[e-1]
		if strings.IndexByte(".,!?:;'\"*", c) >= 0 {
			e--
			continue
		}
		if c == ')' && strings.Count(text[s:e], "(") < strings.Count(text[s:e], ")") {
			e--
			continue
		}
		break
	}
	return e
}

func overlaps(spans []Span, s, e int) bool {
	for _, sp := range spans {
		if s < sp.End && e > sp.Start {
			return true
		}
	}
	return false
}

func sortSpans(s []Span) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j].Start < s[j-1].Start; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// HasURL은 게시 비용이 커지는 URL 포함 여부다 (X 요금: URL 포함 게시는 별도 단가).
func HasURL(text string) bool { return len(ExtractURLs(Normalize(text))) > 0 }
