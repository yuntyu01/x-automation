package thread

import (
	"encoding/json"
	"os"
	"testing"
	"unicode/utf16"
)

type weightedCase struct {
	Description string `json:"description"`
	Text        string `json:"text"`
	Expected    struct {
		WeightedLength int  `json:"weightedLength"`
		Valid          bool `json:"valid"`
		Permillage     int  `json:"permillage"`
		ValidRangeEnd  int  `json:"validRangeEnd"`
	} `json:"expected"`
}

type urlCase struct {
	Description string          `json:"description"`
	Text        string          `json:"text"`
	Expected    json.RawMessage `json:"expected"`
}

type conformance struct {
	WeightedV2  []weightedCase `json:"weighted_v2"`
	WeightedV3  []weightedCase `json:"weighted_v3"`
	Directional []weightedCase `json:"directional"`
	URLs        []urlCase      `json:"urls"`
	URLsIdx     []urlCase      `json:"urls_with_indices"`
	URLsDir     []urlCase      `json:"urls_with_directional_markers"`
	TcoParams   []urlCase      `json:"tco_urls_with_params"`
}

func load(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatal(err)
	}
}

// urlTexts는 UTF-16 위치를 가진 URL 추출 결과의 텍스트다.
func urlTexts(text string) []string {
	t := toU16(text)
	var out []string
	for _, e := range extractURLEntities(text) {
		s := t[e.Start:e.End].String()
		out = append(out, s)
	}
	return out
}

// 공식 twitter-text 적합성 사례 (testdata/twitter-text)
func TestTwitterTextConformance(t *testing.T) {
	var c conformance
	load(t, "../../testdata/twitter-text/conformance.json", &c)
	for _, set := range []struct {
		name  string
		cfg   Config
		cases []weightedCase
	}{{"v2", V2, c.WeightedV2}, {"v3", V3, c.WeightedV3}, {"directional", V3, c.Directional}} {
		for _, tc := range set.cases {
			p := Parse(tc.Text, set.cfg)
			e := tc.Expected
			if p.WeightedLength != e.WeightedLength || p.Valid != e.Valid || p.Permillage != e.Permillage || p.ValidRangeEnd != e.ValidRangeEnd {
				t.Errorf("%s %q: got %d/%v/%d/%d want %d/%v/%d/%d", set.name, tc.Description, p.WeightedLength, p.Valid, p.Permillage, p.ValidRangeEnd,
					e.WeightedLength, e.Valid, e.Permillage, e.ValidRangeEnd)
			}
		}
	}
	for _, set := range [][]urlCase{c.URLs, c.URLsDir, c.TcoParams} {
		for _, tc := range set {
			var raw []any
			if err := json.Unmarshal(tc.Expected, &raw); err != nil {
				t.Fatal(err)
			}
			var want []string
			for _, w := range raw {
				switch v := w.(type) {
				case string:
					want = append(want, v)
				case map[string]any:
					want = append(want, v["url"].(string))
				}
			}
			got := urlTexts(tc.Text)
			if len(got) != len(want) {
				t.Errorf("urls %q: got %q want %q", tc.Description, got, want)
				continue
			}
			for i := range got {
				if got[i] != want[i] {
					t.Errorf("urls %q: got %q want %q", tc.Description, got, want)
				}
			}
		}
	}
	for _, tc := range c.URLsIdx {
		var want []struct {
			URL     string `json:"url"`
			Indices []int  `json:"indices"`
		}
		if err := json.Unmarshal(tc.Expected, &want); err != nil {
			t.Fatal(err)
		}
		// 적합성 사례의 위치는 코드 포인트 기준이다
		got := extractURLEntities(tc.Text)
		cps := codePointIndex(tc.Text)
		if len(got) != len(want) {
			t.Errorf("urls_with_indices %q: got %v want %v", tc.Description, got, want)
			continue
		}
		for i := range got {
			if cps[got[i].Start] != want[i].Indices[0] || cps[got[i].End] != want[i].Indices[1] {
				t.Errorf("urls_with_indices %q: got %v want %v", tc.Description, got[i], want[i])
			}
		}
	}
}

// codePointIndex는 UTF-16 위치 → 코드 포인트 위치 표다.
func codePointIndex(s string) []int {
	var out []int
	n := 0
	for _, r := range s {
		out = append(out, n)
		if utf16.RuneLen(r) == 2 {
			out = append(out, n)
		}
		n++
	}
	return append(out, n)
}

type golden struct {
	TwitterText string `json:"twitter_text"`
	Cases       []struct {
		Text string  `json:"text"`
		V3   [4]any  `json:"v3"`
		V2   [4]any  `json:"v2"`
		URLs [][]int `json:"urls"`
	} `json:"cases"`
	Emoji [][]int `json:"emoji"`
}

func asInt(v any) int { return int(v.(float64)) }

func short(s string) string {
	if r := []rune(s); len(r) > 40 {
		return string(r[:40]) + "…"
	}
	return s
}

// 프론트가 쓰는 공식 라이브러리로 계산한 기대값과 같아야 한다 (testdata/thread/golden.json)
func TestMatchesOfficialLibrary(t *testing.T) {
	var g golden
	load(t, "../../testdata/thread/golden.json", &g)
	if len(g.Cases) < 100 || len(g.Emoji) < 1000 {
		t.Fatalf("golden fixture looks truncated: %d cases %d emoji", len(g.Cases), len(g.Emoji))
	}
	for _, c := range g.Cases {
		for _, set := range []struct {
			cfg  Config
			want [4]any
		}{{V3, c.V3}, {V2, c.V2}} {
			p := Parse(c.Text, set.cfg)
			if p.WeightedLength != asInt(set.want[0]) || p.Valid != set.want[1].(bool) || p.Permillage != asInt(set.want[2]) || p.ValidRangeEnd != asInt(set.want[3]) {
				t.Errorf("%q (emoji=%v): got %d/%v/%d/%d want %v", short(c.Text), set.cfg.EmojiParsing, p.WeightedLength, p.Valid, p.Permillage, p.ValidRangeEnd, set.want)
			}
		}
		got := extractURLEntities(Normalize(c.Text))
		if c.Text != Normalize(c.Text) {
			continue // 라이브러리의 extractUrlsWithIndices는 정규화 전 원문에 돌린 값이다
		}
		if len(got) != len(c.URLs) {
			t.Errorf("urls %q: got %v want %v", c.Text, got, c.URLs)
			continue
		}
		for i, u := range c.URLs {
			if got[i].Start != u[0] || got[i].End != u[1] || got[i].Length != u[2] {
				t.Errorf("urls %q: got %v want %v", c.Text, got[i], u)
			}
		}
	}
	first := func(s string) int {
		e := emojis16(toU16(s))
		if len(e) > 0 && e[0].Start == 0 {
			return e[0].End
		}
		return 0
	}
	for _, row := range g.Emoji {
		ch := string(rune(row[0]))
		got := []int{first(ch), first(ch + "️"), first(ch + "︎"), first(ch + "\U0001F3FD"), first(ch + "️⃣")}
		for i := range got {
			if got[i] != row[i+1] {
				t.Errorf("emoji U+%04X variant %d: got %d want %d", row[0], i, got[i], row[i+1])
			}
		}
	}
}

// 단위 가중치의 합은 전체 판정과 같아야 한다 (분할 근거가 실제 길이와 어긋나지 않게)
func TestUnitsSumToParsedLength(t *testing.T) {
	var g golden
	load(t, "../../testdata/thread/golden.json", &g)
	for _, c := range g.Cases {
		n := Normalize(c.Text)
		sum := 0
		prev := 0
		for _, u := range V3.units(n) {
			if u.start != prev || u.end <= u.start {
				t.Fatalf("%q: units not contiguous at %d (%d-%d)", c.Text, prev, u.start, u.end)
			}
			prev = u.end
			sum += u.weight
		}
		if prev != len(n) {
			t.Fatalf("%q: units end at %d of %d", c.Text, prev, len(n))
		}
		if sum/V3.Scale != Parse(n, V3).WeightedLength {
			t.Errorf("%q: units %d parse %d", c.Text, sum/V3.Scale, Parse(n, V3).WeightedLength)
		}
	}
}
