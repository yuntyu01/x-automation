package thread

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// URL 추출: twitter-text extractUrlsWithIndices를 Go 표준 regexp(RE2, 입력 길이에 선형)로 옮긴 것이다.
//
// 정규식 조각은 twittertext_gen.go(라이브러리에서 생성, 구조 검사 포함)에 있고 여기서 같은 구조로 조립한다.
// RE2에 없는 기능 두 가지만 같은 결과가 나오게 바꾼다.
//
//  1. TLD 뒤 lookahead (?=[^0-9a-zA-Z@+-]|$)
//     TLD 바로 뒤 글자에만 거는 조건이다. 포트(:)·경로(/)·쿼리(?)가 이어지면 그 첫 글자가 조건을 만족하므로,
//     꼬리를 "포트… | 경로… | 쿼리 | (조건 글자 1개 또는 끝)"의 대안으로 펼친다. 이 순서는 원래의 탐욕적 선택
//     (포트→경로→쿼리를 할 수 있으면 한다)과 같은 우선순위다. 마지막 대안이 먹은 글자 1개는 URL 끝에서 뺀다.
//  2. 이어서 찾기 (JavaScript lastIndex)
//     앞 글자 조건의 ^는 문자열 처음에만 맞는다. 두 번째부터는 ^를 뺀 정규식으로 이어서 찾는다.
//
// RE2는 백트래킹 구현이 골랐을 일치를 고르므로(regexp 패키지 문서) 나머지 우선순위는 그대로다.
// TLD 목록은 같은 언어의 접두사 트리로 바꾼다 (factorTLDs). 결과는 공식 적합성 사례와 golden.json으로 대조한다.

var (
	urlRe, urlReNoCaret *regexp.Regexp
	asciiDomainRe       = mustRE2(asciiDomainPattern(), "i")
	tcoRe               = mustRE2(jsToRE2(srcValidTcoURL), flagsValidTcoURL)
	invalidNoProtoPrev  = mustRE2(jsToRE2(srcInvalidURLWithoutProtocolPrecedingChars), flagsInvalidURLWithoutProtocolPrecedingChars)
	invalidCharsRe      = mustRE2(jsToRE2(srcInvalidChars), flagsInvalidChars)
)

// tldLookahead는 TLD 그룹 끝의 lookahead다.
const tldLookahead = `(?=[^0-9a-zA-Z@+-]|$)`

// tldBoundary는 lookahead를 대신해 글자 1개를 먹는 검사다 (먹은 글자는 결과에서 뺀다).
const tldBoundary = `[^0-9a-zA-Z@+\-]|$`

func init() {
	pre := jsToRE2(srcValidURLPrecedingChars)
	if !strings.HasSuffix(pre, "|^)") {
		panic("validUrlPrecedingChars shape changed")
	}
	preNoCaret := strings.TrimSuffix(pre, "|^)") + ")"
	sub, name := jsToRE2(srcValidSubdomain), jsToRE2(srcValidDomainName)
	tlds := "(?:" + factorTLDs(srcValidGTLD) + "|" + factorTLDs(srcValidCCTLD) + ")"
	puny := jsToRE2(srcValidPunycode)
	port := jsToRE2(srcValidPortNumber)
	path := "/(?:" + jsToRE2(srcValidURLPath) + ")*"
	query := `\?` + jsToRE2(srcValidURLQueryChars) + "*" + jsToRE2(srcValidURLQueryEndingChars)
	checked := `(?::` + port + `(?P<path1>` + path + `)?(?:` + query + `)?` +
		`|(?P<path2>` + path + `)(?:` + query + `)?` +
		`|` + query +
		`|(?P<la>` + tldBoundary + `))`
	tail := `(?::` + port + `)?(?P<path3>` + path + `)?(?:` + query + `)?`
	url := `(?P<url>(?P<proto>https?://)?(?P<dom>` + sub + `*` + name + `)(?:(?P<tld>` + tlds + `)` + checked + `|(?P<puny>` + puny + `)` + tail + `))`
	urlRe = mustRE2(`(?P<pre>`+pre+`)`+url, "i")
	urlReNoCaret = mustRE2(`(?P<pre>`+preNoCaret+`)`+url, "i")
}

// asciiDomainPattern은 validAsciiDomain의 TLD lookahead를 먹는 검사로 바꾼 것이다. 끝은 tld 그룹 끝(퓨니코드면 일치 끝).
func asciiDomainPattern() string {
	src := jsToRE2(srcValidASCIIDomain)
	g, c := jsToRE2(srcValidGTLD), jsToRE2(srcValidCCTLD)
	if strings.Count(src, g) != 1 || strings.Count(src, c) != 1 {
		panic("validAsciiDomain shape changed")
	}
	src = strings.Replace(src, g, `(?P<tldg>`+factorTLDs(srcValidGTLD)+`)(?:`+tldBoundary+`)`, 1)
	return strings.Replace(src, c, `(?P<tldc>`+factorTLDs(srcValidCCTLD)+`)(?:`+tldBoundary+`)`, 1)
}

var jsUnicodeEsc = regexp.MustCompile(`\\u([0-9a-fA-F]{4})`)

// jsToRE2는 JavaScript 정규식 문법을 RE2 문법으로 바꾼다 (\uXXXX → \x{XXXX}). 조각에 다른 JS 전용 문법은 없다.
func jsToRE2(src string) string { return jsUnicodeEsc.ReplaceAllString(src, `\x{$1}`) }

func mustRE2(src, flags string) *regexp.Regexp {
	if strings.Contains(flags, "i") {
		src = "(?i)" + src
	}
	return regexp.MustCompile(src)
}

// tldGroup은 twitter-text의 TLD 목록 모양이다: (?:(?:삼성|닷컴|…|onion)(?=[^0-9a-zA-Z@+-]|$))
var tldGroup = regexp.MustCompile(`^\(\?:\(\?:([^()]+)\)` + regexp.QuoteMeta(tldLookahead) + `\)$`)

// factorTLDs는 TLD 대안 목록(1,500여 개)을 lookahead 없이 같은 언어의 접두사 트리 정규식으로 바꾼다.
//
// 순서를 바꿔도 결과가 같은 이유: 한 목록의 두 TLD가 같은 위치에서 함께 맞으려면 한쪽이 다른 쪽의 접두사여야 하고,
// 짧은 쪽 뒤 글자가 영숫자면 TLD 뒤 조건에서 떨어진다. 그렇지 않은 쌍(嘉里/嘉里大酒店 등)은 원래 목록에서 긴 쪽이
// 앞에 있어, 긴 쪽을 먼저 시도하는 탐욕적 선택 그룹 (?:…)?과 같다. 아니면 멈춘다 (라이브러리 갱신 시 확인).
func factorTLDs(src string) string {
	m := tldGroup.FindStringSubmatch(src)
	if m == nil {
		panic("TLD list shape changed")
	}
	list := strings.Split(m[1], "|")
	pos := map[string]int{}
	for i, w := range list {
		pos[w] = i
	}
	for _, a := range list {
		for _, b := range list {
			if a != b && strings.HasPrefix(b, a) {
				next, _ := utf8.DecodeRuneInString(b[len(a):])
				if !strings.ContainsRune("0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ@+-", next) && pos[b] > pos[a] {
					panic("TLD order would change the match: " + a + " / " + b)
				}
			}
		}
	}
	return trieRegex(list)
}

type trieNode struct {
	end  bool
	kids map[rune]*trieNode
	keys []rune
}

func trieRegex(words []string) string {
	root := &trieNode{kids: map[rune]*trieNode{}}
	for _, w := range words {
		n := root
		for _, r := range w {
			k, ok := n.kids[r]
			if !ok {
				k = &trieNode{kids: map[rune]*trieNode{}}
				n.kids[r] = k
				n.keys = append(n.keys, r)
			}
			n = k
		}
		n.end = true
	}
	var emit func(n *trieNode) string
	emit = func(n *trieNode) string {
		if len(n.keys) == 0 {
			return ""
		}
		alts := make([]string, len(n.keys))
		for i, r := range n.keys {
			alts[i] = regexp.QuoteMeta(string(r)) + emit(n.kids[r])
		}
		body := strings.Join(alts, "|")
		if n.end {
			return "(?:" + body + ")?" // 더 긴 TLD를 먼저 시도하고 실패하면 여기서 끝낸다
		}
		if len(alts) == 1 {
			return body
		}
		return "(?:" + body + ")"
	}
	return "(?:" + emit(root) + ")"
}

// entity는 UTF-16 위치 [Start, End)와 텍스트 길이다. URL은 길이가 위치 차이와 다를 수 있다 (프로토콜 없는 비ASCII 도메인).
type entity struct {
	Start, End int
	Length     int
}

const (
	maxURLLength     = 4096
	maxTcoSlugLength = 40
	defaultProtocol  = "https://"
)

// len16은 UTF-16 길이다 (JavaScript string.length).
func len16(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// byteToU16는 바이트 위치 → UTF-16 위치 표다 (글자 시작 위치와 끝만 의미 있다).
func byteToU16(s string) []int {
	out := make([]int, len(s)+1)
	n := 0
	for b, r := range s {
		out[b] = n
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	out[len(s)] = n
	return out
}

func span(re *regexp.Regexp, m []int, name string, off int) (int, int) {
	i := re.SubexpIndex(name)
	if m[2*i] < 0 {
		return -1, -1
	}
	return m[2*i] + off, m[2*i+1] + off
}

// extractURLEntities는 twitter-text extractUrlsWithIndices(기본 옵션: 프로토콜 없는 URL도 추출)와 같다. 위치는 UTF-16.
func extractURLEntities(text string) []entity {
	if text == "" || !strings.Contains(text, ".") {
		return nil
	}
	u := byteToU16(text)
	var out []entity
	for pos := 0; pos <= len(text); {
		re := urlRe
		if pos > 0 {
			re = urlReNoCaret
		}
		m := re.FindStringSubmatchIndex(text[pos:])
		if m == nil {
			break
		}
		off := pos
		g := func(name string) (int, int) { return span(re, m, name, off) }
		preS, preE := g("pre")
		urlS, urlE := g("url")
		if laS, _ := g("la"); laS >= 0 {
			urlE = laS // TLD 뒤 검사로 먹은 글자는 URL이 아니다
		}
		pos = urlE // JavaScript lastIndex
		protoS, protoE := g("proto")
		domS, _ := g("dom")
		_, domE := g("tld")
		if domE < 0 {
			_, domE = g("puny")
		}
		hasPath := false
		for _, p := range []string{"path1", "path2", "path3"} {
			if s, _ := g(p); s >= 0 {
				hasPath = true
			}
		}
		url, domain := text[urlS:urlE], text[domS:domE]
		proto := text[max(protoS, 0):max(protoE, 0)]
		protoLen := len16(proto)
		if protoLen == 0 {
			protoLen = len(defaultProtocol)
		}
		if !validURL(len16(url), protoLen, domain) {
			continue
		}
		start, end := u[urlS], u[urlE]
		if proto == "" {
			if invalidNoProtoPrev.MatchString(text[preS:preE]) {
				continue
			}
			var last *entity
			asciiEnd := 0 // domain 안의 바이트 위치
			for dpos := 0; dpos <= len(domain); {
				dm := asciiDomainRe.FindStringSubmatchIndex(domain[dpos:])
				if dm == nil {
					break
				}
				mEnd := dm[1] + dpos
				for _, g := range []string{"tldg", "tldc"} {
					if s, e := span(asciiDomainRe, dm, g, dpos); s >= 0 {
						mEnd = e
					}
				}
				ascii := domain[dm[0]+dpos : mEnd]
				dpos = mEnd
				i := strings.Index(domain[asciiEnd:], ascii)
				if i < 0 {
					break
				}
				aStart := asciiEnd + i
				asciiEnd = aStart + len(ascii)
				out = append(out, entity{Start: start + len16(domain[:aStart]), End: start + len16(domain[:asciiEnd]), Length: len16(ascii)})
				last = &out[len(out)-1]
			}
			if last == nil {
				continue
			}
			if hasPath {
				// url.replace(domain, asciiDomain): URL 맨 앞의 도메인만 바뀐다
				last.Length = len16(url) - len16(domain) + last.Length
				last.End = end
			}
			continue
		}
		if tm := tcoRe.FindStringSubmatch(url); tm != nil {
			if len16(tm[1]) > maxTcoSlugLength {
				continue
			}
			end = start + len16(tm[0])
			url = tm[0]
		}
		out = append(out, entity{Start: start, End: end, Length: len16(url)})
	}
	return out
}

// validURL은 twitter-text isValidUrl + idna.toAscii다 (도메인 라벨 1~63자, 전체 4096자 이하).
func validURL(urlLen, protoLen int, domain string) bool {
	if strings.HasPrefix(domain, "xn--") && !asciiDomainRe.MatchString(domain) {
		return false
	}
	for _, label := range strings.Split(domain, ".") {
		n := punycodeASCIILen(label)
		if n < 1 || n > 63 {
			return false
		}
	}
	return protoLen+urlLen <= maxURLLength
}
