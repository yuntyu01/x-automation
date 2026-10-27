package thread

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

type Mode string

const (
	ModeTwoParts Mode = "two_parts" // 절반 부근 문단·문장 경계에서 본문 + 답글 하나 (넘치면 추가 답글)
	ModeOverflow Mode = "overflow"  // 본문을 최대한 채우고 넘는 부분만 답글로
	ModeManual   Mode = "manual"    // 사용자가 구분선(---)으로 직접 나눔
)

func (m Mode) Valid() bool { return m == ModeTwoParts || m == ModeOverflow || m == ModeManual }

// ManualSeparator는 직접 나누기 모드에서 원문에 들어가는 구분선이다. 한 줄에 ---만 있으면 경계다.
const ManualSeparator = "\n---\n"

var manualSplitRe = regexp.MustCompile(`(?m)^[ \t]*-{3,}[ \t]*$`)

type Options struct {
	Mode       Mode `json:"mode"`
	Numbering  bool `json:"numbering"`   // 끝에 (1/N) 표기. 길이 계산에 포함
	MaxReplies int  `json:"max_replies"` // 0이면 제한 없음. 넘으면 게시 불가(수정 대기)
	Limit      int  `json:"limit"`       // 0이면 280
}

type Part struct {
	Index    int    `json:"index"`
	Text     string `json:"text"`
	Weighted int    `json:"weighted_length"`
	Valid    bool   `json:"valid"`
	Problem  string `json:"problem,omitempty"`
}

type Result struct {
	Parts            []Part   `json:"parts"`
	Mode             Mode     `json:"mode"`
	Limit            int      `json:"limit"`
	RequestedReplies int      `json:"requested_replies"` // 모드가 목표로 한 답글 수
	Replies          int      `json:"replies"`           // 실제 답글 수
	Note             string   `json:"note"`              // "기본 답글 1개 → 분량 초과로 실제 답글 3개" 같은 설명
	Publishable      bool     `json:"publishable"`
	Problems         []string `json:"problems"`
	HasURL           bool     `json:"has_url"`
	PostCount        int      `json:"post_count"` // 실제 X 게시물 수 (본문 + 답글)
}

// NormalizeForPost는 게시용 정규화다. 원문은 따로 보존하고 이 결과로만 나누고 센다.
//   - CRLF·CR → LF, NFC 정규화
//   - 줄 끝 공백 제거, 빈 줄 3개 이상 → 2개
//   - 앞뒤 공백 제거
func NormalizeForPost(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = Normalize(s)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	s = strings.Join(lines, "\n")
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(s)
}

func numberSuffix(i, n int) string { return fmt.Sprintf("\n(%d/%d)", i, n) }

// Split은 원문을 본문·답글로 나눈다. 원문의 글자를 빼거나 더하지 않는다 (번호 표기 제외).
func Split(raw string, o Options) Result {
	if o.Limit <= 0 {
		o.Limit = V3.MaxWeighted
	}
	if !o.Mode.Valid() {
		o.Mode = ModeTwoParts
	}
	res := Result{Mode: o.Mode, Limit: o.Limit, Problems: []string{}}
	var texts []string
	switch o.Mode {
	case ModeManual:
		res.RequestedReplies = -1
		for _, p := range manualSplitRe.Split(strings.ReplaceAll(raw, "\r\n", "\n"), -1) {
			texts = append(texts, NormalizeForPost(p))
		}
	default:
		text := NormalizeForPost(raw)
		if text == "" {
			res.Problems = append(res.Problems, "내용이 비어 있어요")
			res.Parts = []Part{}
			return res
		}
		texts = autoSplit(text, o)
		if o.Mode == ModeTwoParts {
			res.RequestedReplies = 1
		} else {
			res.RequestedReplies = 0
		}
	}

	n := len(texts)
	for i, t := range texts {
		final := t
		if o.Numbering && n > 1 {
			final = t + numberSuffix(i+1, n)
		}
		p := ParseLimit(final, V3, o.Limit)
		part := Part{Index: i, Text: final, Weighted: p.WeightedLength, Valid: p.Valid}
		switch {
		case t == "":
			part.Valid = false
			part.Problem = "빈 부분이에요"
		case p.WeightedLength > o.Limit:
			part.Problem = fmt.Sprintf("%d자로 한도 %d자를 넘어요", p.WeightedLength, o.Limit)
		case !p.Valid:
			part.Problem = "게시할 수 없는 문자가 있어요"
		}
		res.Parts = append(res.Parts, part)
	}
	res.Replies = max(len(res.Parts)-1, 0)
	res.PostCount = len(res.Parts)
	res.HasURL = HasURL(raw)

	switch o.Mode {
	case ModeTwoParts:
		switch {
		case res.Replies == 0:
			res.Note = "나눌 문단·문장 경계가 없어 본문 하나로 게시해요"
		case res.Replies == 1:
			res.Note = "본문 + 답글 1개"
		default:
			res.Note = fmt.Sprintf("기본 답글 1개 → 분량 초과로 실제 답글 %d개", res.Replies)
		}
	case ModeOverflow:
		if res.Replies == 0 {
			res.Note = "본문 하나에 모두 들어가요"
		} else {
			res.Note = fmt.Sprintf("한도를 넘는 부분을 답글 %d개로 이어요", res.Replies)
		}
	case ModeManual:
		res.Note = fmt.Sprintf("직접 나눈 대로 본문 + 답글 %d개", res.Replies)
	}

	res.Publishable = len(res.Parts) > 0
	for _, p := range res.Parts {
		if !p.Valid {
			res.Publishable = false
		}
	}
	if !res.Publishable && len(res.Parts) > 0 {
		res.Problems = append(res.Problems, "길이를 넘거나 비어 있는 부분이 있어요")
	}
	if o.MaxReplies > 0 && res.Replies > o.MaxReplies {
		res.Publishable = false
		res.Problems = append(res.Problems, fmt.Sprintf("답글이 최대 %d개를 넘어요 (필요 %d개). 글을 줄이거나 직접 나눠 주세요", o.MaxReplies, res.Replies))
	}
	return res
}

// 경계 우선순위
const (
	bGrapheme  = 0
	bSpace     = 1
	bNewline   = 2
	bSentence  = 3
	bParagraph = 4
)

type layout struct {
	text   string
	units  []unit
	prefix []int // prefix[i] = units[0:i]의 가중 합 (scale 곱)
	breakP []int // breakP[i] = units[i] 앞에서 끊을 때의 우선순위 (-1이면 끊을 수 없음)
}

var sentenceEnd = map[rune]bool{'.': true, '!': true, '?': true, '。': true, '！': true, '？': true, '…': true}
var closers = map[rune]bool{'"': true, '\'': true, ')': true, ']': true, '」': true, '』': true, '”': true, '’': true, '》': true, '〉': true}

func newLayout(text string) *layout {
	l := &layout{text: text, units: V3.units(text)}
	l.prefix = make([]int, len(l.units)+1)
	for i, u := range l.units {
		l.prefix[i+1] = l.prefix[i] + u.weight
	}
	l.breakP = make([]int, len(l.units)+1)
	for i := range l.breakP {
		l.breakP[i] = l.breakAt(i)
	}
	return l
}

// breakAt은 units[i] 앞(= units[i-1] 뒤)에서 끊을 수 있는지와 그 우선순위다.
// 공백·줄바꿈은 앞쪽 부분 끝에 붙여 두고 뒤쪽 부분을 공백 아닌 글자로 시작하게 한다.
func (l *layout) breakAt(i int) int {
	if i <= 0 || i >= len(l.units) {
		return -1
	}
	cur := l.text[l.units[i].start:l.units[i].end]
	prev := l.text[l.units[i-1].start:l.units[i-1].end]
	if isSpace(cur) {
		return -1 // 공백 앞에서는 끊지 않는다 (공백 뒤에서 끊는다)
	}
	if prev == "\n" {
		if i >= 2 && l.text[l.units[i-2].start:l.units[i-2].end] == "\n" {
			return bParagraph
		}
		// 문장 끝 + 줄바꿈도 문장 경계로 본다
		if i >= 2 && endsSentence(l.text[:l.units[i-2].end]) {
			return bSentence
		}
		return bNewline
	}
	if isSpace(prev) {
		if i >= 2 && endsSentence(l.text[:l.units[i-1].start]) {
			return bSentence
		}
		return bSpace
	}
	return bGrapheme
}

func isSpace(s string) bool { return s == " " || s == "\t" || s == "\n" || s == "　" }

func endsSentence(s string) bool {
	for len(s) > 0 {
		r, size := utf8.DecodeLastRuneInString(s)
		if closers[r] {
			s = s[:len(s)-size]
			continue
		}
		return sentenceEnd[r]
	}
	return false
}

// weight는 units[a:b]를 잘라 앞뒤 공백을 뗀 텍스트의 가중 길이(scale 곱)를 근사한다.
func (l *layout) weight(a, b int) int {
	for a < b && isSpace(l.text[l.units[a].start:l.units[a].end]) {
		a++
	}
	for b > a && isSpace(l.text[l.units[b-1].start:l.units[b-1].end]) {
		b--
	}
	return l.prefix[b] - l.prefix[a]
}

func (l *layout) slice(a, b int) string {
	if a >= b {
		return ""
	}
	return strings.TrimSpace(l.text[l.units[a].start:l.units[b-1].end])
}

func autoSplit(text string, o Options) []string {
	reserve := 0
	var parts []string
	// 번호 표기는 전체 개수에 따라 길이가 바뀌므로 자리를 예약하고 개수가 안정될 때까지 다시 나눈다
	for iter := 0; iter < 4; iter++ {
		limit := (o.Limit - reserve) * V3.Scale
		l := newLayout(text)
		if o.Mode == ModeTwoParts {
			parts = l.twoParts(limit)
		} else {
			parts = l.overflow(0, len(l.units), limit)
		}
		parts = refit(parts, limit)
		if !o.Numbering || len(parts) <= 1 {
			return parts
		}
		need := utf8.RuneCountInString(numberSuffix(len(parts), len(parts)))
		if need <= reserve {
			return parts
		}
		reserve = need
	}
	return parts
}

// refit은 자른 부분을 단독으로 다시 세어 한도를 넘으면 그 부분만 다시 나눈다.
// 단위 가중치는 전체 문맥에서 센 값이라, 잘린 뒤 URL 인식이 달라지면(앞 글자·TLD 뒤 글자 조건) 길이가 달라질 수 있다.
// X는 게시물마다 따로 세므로 부분 단독 판정이 기준이다.
func refit(parts []string, limit int) []string {
	for iter := 0; iter < 3; iter++ {
		changed := false
		var out []string
		for _, p := range parts {
			if Parse(p, V3).WeightedLength*V3.Scale <= limit {
				out = append(out, p)
				continue
			}
			l := newLayout(p)
			sub := l.overflow(0, len(l.units), limit)
			if len(sub) <= 1 {
				out = append(out, p) // 더 나눌 수 없으면 그대로 두고 문제로 표시한다
				continue
			}
			out = append(out, sub...)
			changed = true
		}
		parts = out
		if !changed {
			break
		}
	}
	return parts
}

// twoParts는 절반 부근의 문단(없으면 문장) 경계에서 나눈다. 각 쪽이 한도를 넘으면 그쪽만 더 나눈다.
func (l *layout) twoParts(limit int) []string {
	n := len(l.units)
	total := l.weight(0, n)
	half := total / 2
	best, bestScore := -1, 0
	for _, prio := range []int{bParagraph, bSentence} {
		for i := 1; i < n; i++ {
			if l.breakP[i] < prio {
				continue
			}
			d := l.weight(0, i) - half
			if d < 0 {
				d = -d
			}
			// 가운데에서 전체의 35% 이상 벗어나면 이 우선순위에서는 고르지 않는다
			if d > total*35/100 {
				continue
			}
			if best < 0 || d < bestScore {
				best, bestScore = i, d
			}
		}
		if best >= 0 {
			break
		}
	}
	if best < 0 {
		if total <= limit {
			return []string{l.slice(0, n)}
		}
		return l.overflow(0, n, limit)
	}
	var out []string
	for _, r := range [][2]int{{0, best}, {best, n}} {
		if l.weight(r[0], r[1]) <= limit {
			out = append(out, l.slice(r[0], r[1]))
		} else {
			out = append(out, l.overflow(r[0], r[1], limit)...)
		}
	}
	return out
}

// overflow는 앞에서부터 한도까지 채우고, 그 안에서 가장 좋은 경계를 골라 나눈다.
func (l *layout) overflow(a, b, limit int) []string {
	var out []string
	for a < b {
		if l.weight(a, b) <= limit {
			out = append(out, l.slice(a, b))
			break
		}
		// 한도 안에 들어가는 가장 먼 끝
		far := a + 1
		for far < b && l.weight(a, far+1) <= limit {
			far++
		}
		cut := -1
		for prio := bParagraph; prio >= bGrapheme && cut < 0; prio-- {
			for i := far; i > a; i-- {
				// 너무 앞에서 끊어 한 부분이 지나치게 짧아지지 않게 한도의 40% 이상 채운 경계만 본다 (문자소 경계는 예외)
				if l.breakP[i] >= prio && (prio == bGrapheme || l.weight(a, i)*10 >= limit*4) {
					cut = i
					break
				}
			}
		}
		if cut <= a {
			cut = far // 끊을 경계가 없으면 문자소 단위로 자른다 (URL·이모지는 쪼개지지 않는 단위)
		}
		out = append(out, l.slice(a, cut))
		a = cut
		for a < b && isSpace(l.text[l.units[a].start:l.units[a].end]) {
			a++
		}
	}
	return out
}

// JoinManual은 부분 목록을 직접 나누기 원문으로 합친다 (편집기의 부분 편집 → 원문).
func JoinManual(parts []string) string {
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	return strings.Join(parts, ManualSeparator)
}

// ToManual은 현재 자동 분할 결과를 직접 나누기 원문으로 바꾼다 (분할 위치를 손으로 고칠 때).
func ToManual(raw string, o Options) string {
	o.Numbering = false
	r := Split(raw, o)
	texts := make([]string, len(r.Parts))
	for i, p := range r.Parts {
		texts[i] = p.Text
	}
	return JoinManual(texts)
}
