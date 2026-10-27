package thread

import (
	"strconv"
	"strings"
	"testing"
	"unicode"
)

func TestWeightedBasics(t *testing.T) {
	cases := []struct {
		text string
		want int
	}{
		{"Hi http://test.co", 26},
		{"http://test.co", 23},
		{"가나다", 6},
		{"abc", 3},
		{"👨‍👩‍👧‍👦", 2},
		{"🇰🇷", 2},
		{"👍🏽", 2},
		{"1️⃣", 2},
		{"ABC‪￿ABC￾", 12},
		{"see example.com/path now", 4 + 23 + 4},
	}
	for _, c := range cases {
		if got := WeightedLength(c.text); got != c.want {
			t.Errorf("%q = %d, want %d", c.text, got, c.want)
		}
	}
	if Parse("ABC￿", V3).Valid {
		t.Error("invalid char must make text invalid")
	}
	ko140 := strings.Repeat("가", 140)
	if p := Parse(ko140, V3); !p.Valid || p.WeightedLength != 280 {
		t.Errorf("140 hangul = %+v", p)
	}
	if p := Parse(ko140+"가", V3); p.Valid {
		t.Errorf("141 hangul must be invalid: %+v", p)
	}
	// NFD로 들어온 한글도 NFC로 정규화해 센다
	if got := WeightedLength("가"); got != 2 {
		t.Errorf("NFD hangul = %d", got)
	}
}

func visible(s string) string {
	var b strings.Builder
	for _, r := range s {
		if !unicode.IsSpace(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func joined(r Result) string {
	var b strings.Builder
	for _, p := range r.Parts {
		b.WriteString(p.Text)
	}
	return b.String()
}

func story(paragraphs int) string {
	var ps []string
	for i := 0; i < paragraphs; i++ {
		ps = append(ps, "그날 밤 나는 오래된 편지를 다시 꺼내 읽었다. 글씨는 바래 있었지만 마음은 그대로였다. 창밖으로 비가 내리고 있었다.")
	}
	return strings.Join(ps, "\n\n")
}

func TestTwoPartsSplitsAtParagraphNearHalf(t *testing.T) {
	raw := story(2)
	r := Split(raw, Options{Mode: ModeTwoParts})
	if len(r.Parts) != 2 || !r.Publishable || r.Note != "본문 + 답글 1개" {
		t.Fatalf("parts=%d publishable=%v note=%q", len(r.Parts), r.Publishable, r.Note)
	}
	if visible(joined(r)) != visible(raw) {
		t.Fatal("split lost or duplicated content")
	}
	if strings.Contains(r.Parts[0].Text, "\n\n") {
		t.Fatalf("should split at paragraph: %q", r.Parts[0].Text)
	}
}

func TestLongTextGetsExtraRepliesWithReason(t *testing.T) {
	raw := story(6)
	r := Split(raw, Options{Mode: ModeTwoParts})
	if r.Replies < 2 || !strings.Contains(r.Note, "분량 초과로 실제 답글") {
		t.Fatalf("replies=%d note=%q", r.Replies, r.Note)
	}
	for _, p := range r.Parts {
		if !p.Valid || p.Weighted > 280 {
			t.Fatalf("part over limit: %+v", p)
		}
	}
	if visible(joined(r)) != visible(raw) {
		t.Fatal("content changed")
	}
	limited := Split(raw, Options{Mode: ModeTwoParts, MaxReplies: 1})
	if limited.Publishable || len(limited.Problems) == 0 {
		t.Fatal("max replies exceeded must not be publishable")
	}
}

func TestOverflowFillsFirstPost(t *testing.T) {
	raw := story(3)
	r := Split(raw, Options{Mode: ModeOverflow})
	if len(r.Parts) < 2 || r.Parts[0].Weighted < 200 {
		t.Fatalf("overflow first part too short: %+v", r.Parts[0])
	}
	short := Split("짧은 명언 한 줄.", Options{Mode: ModeOverflow})
	if len(short.Parts) != 1 || short.Replies != 0 {
		t.Fatalf("short overflow = %+v", short)
	}
}

func TestShortTextWithoutBoundaryStaysSingle(t *testing.T) {
	r := Split("늦었다고 생각할 때가 가장 빠르다", Options{Mode: ModeTwoParts})
	if len(r.Parts) != 1 || !strings.Contains(r.Note, "본문 하나") {
		t.Fatalf("got %+v", r)
	}
}

func TestNoBoundaryLongTextUsesGraphemes(t *testing.T) {
	raw := strings.Repeat("가", 400)
	r := Split(raw, Options{Mode: ModeOverflow})
	if len(r.Parts) != 3 || !r.Publishable || joined(r) != raw {
		t.Fatalf("parts=%d %v", len(r.Parts), r.Publishable)
	}
}

func TestURLAndEmojiAreNotCut(t *testing.T) {
	raw := strings.Repeat("ab ", 85) + "https://example.com/very/long/path?x=1 " + strings.Repeat("👨‍👩‍👧 ", 40)
	r := Split(raw, Options{Mode: ModeOverflow})
	for _, p := range r.Parts {
		if strings.Contains(p.Text, "https://") && !strings.Contains(p.Text, "https://example.com/very/long/path?x=1") {
			t.Fatalf("url was cut: %q", p.Text)
		}
		if strings.HasPrefix(p.Text, "‍") || strings.HasSuffix(p.Text, "‍") {
			t.Fatalf("emoji sequence cut: %q", p.Text)
		}
	}
	if !r.HasURL {
		t.Fatal("has_url should be true")
	}
}

func TestManualModeAndNumbering(t *testing.T) {
	raw := "첫 번째 부분\n---\n두 번째 부분\n  ---  \n세 번째"
	r := Split(raw, Options{Mode: ModeManual, Numbering: true})
	if len(r.Parts) != 3 || r.Parts[0].Text != "첫 번째 부분\n(1/3)" || r.Parts[2].Text != "세 번째\n(3/3)" {
		t.Fatalf("manual = %+v", r.Parts)
	}
	// 번호도 길이에 포함
	if r.Parts[0].Weighted != WeightedLength("첫 번째 부분\n(1/3)") {
		t.Fatal("numbering not counted")
	}
	over := Split("짧음\n---\n"+strings.Repeat("나", 141), Options{Mode: ModeManual})
	if over.Publishable || over.Parts[1].Valid || over.Parts[1].Problem == "" {
		t.Fatalf("manual over-limit part must be flagged: %+v", over.Parts[1])
	}
	empty := Split("본문\n---\n", Options{Mode: ModeManual})
	if empty.Publishable {
		t.Fatal("empty manual part must block publishing")
	}
}

func TestNumberedSplitStaysWithinLimit(t *testing.T) {
	raw := strings.Repeat("가", 1200)
	r := Split(raw, Options{Mode: ModeOverflow, Numbering: true})
	for _, p := range r.Parts {
		if p.Weighted > 280 || !p.Valid {
			t.Fatalf("numbered part over limit: %d", p.Weighted)
		}
	}
	if !strings.HasSuffix(r.Parts[len(r.Parts)-1].Text, "/"+itoa(len(r.Parts))+")") {
		t.Fatalf("last suffix wrong: %q", r.Parts[len(r.Parts)-1].Text)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestToManualRoundTrip(t *testing.T) {
	raw := story(3)
	m := ToManual(raw, Options{Mode: ModeTwoParts})
	r1 := Split(raw, Options{Mode: ModeTwoParts})
	r2 := Split(m, Options{Mode: ModeManual})
	if len(r1.Parts) != len(r2.Parts) {
		t.Fatalf("round trip parts %d vs %d", len(r1.Parts), len(r2.Parts))
	}
	for i := range r1.Parts {
		if r1.Parts[i].Text != r2.Parts[i].Text {
			t.Fatalf("part %d differs", i)
		}
	}
}
