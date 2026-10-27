package schedule

import (
	"testing"
	"time"

	"github.com/yuntyu01/x-automation/internal/randx"
)

var seoul, _ = time.LoadLocation("Asia/Seoul")

func kst(y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, seoul)
}

func defaultRules() Rules {
	return Rules{DailyTarget: 20, BaseIntervalMin: 30, JitterMinMin: 0, JitterMaxMin: 5, EndMarginMin: 5}
}

var everyday = []int{0, 1, 2, 3, 4, 5, 6}

func hm(ts []Slot) []string {
	var out []string
	for _, s := range ts {
		out = append(out, s.At.In(seoul).Format("01-02 15:04"))
	}
	return out
}

// 지시서 예: 기본 30분 + 추가 지연 → 12:00 → 12:33 → 13:04 → 13:39 (간격 30~35분, ±가 아님)
func TestIntervalPlusPositiveJitter(t *testing.T) {
	w := []Window{{Label: "낮", Weekdays: everyday, StartMinute: 720, EndMinute: 1020, Enabled: true}}
	from := kst(2026, 3, 2, 0, 0)
	ivs := Intervals(w, seoul, from, from.AddDate(0, 0, 1))
	got := Generate(Params{Rules: defaultRules(), Loc: seoul, Intervals: ivs, Earliest: from, Until: from.AddDate(0, 0, 1),
		Rand: &randx.Fixed{Values: []int{3, 1, 5, 0}}, Limit: 4})
	want := []string{"03-02 12:00", "03-02 12:33", "03-02 13:04", "03-02 13:39"}
	if len(got) != 4 {
		t.Fatalf("got %v", hm(got))
	}
	for i := range want {
		if hm(got)[i] != want[i] {
			t.Fatalf("got %v want %v", hm(got), want)
		}
	}
	if got[0].JitterSec != 0 || got[1].JitterSec != 180 || got[2].JitterSec != 60 {
		t.Fatalf("stored jitter = %d %d %d", got[0].JitterSec, got[1].JitterSec, got[2].JitterSec)
	}
	for i := 1; i < len(got); i++ {
		gap := got[i].At.Sub(got[i-1].At)
		if gap < 30*time.Minute || gap > 35*time.Minute {
			t.Fatalf("gap %v outside 30~35분", gap)
		}
	}
}

// 시작 포함·끝 제외, 끝 직전 여유, 새벽·낮 두 구간, 하루 상한
func TestWindowsBoundariesAndDailyCap(t *testing.T) {
	w := []Window{
		{Label: "새벽", Weekdays: everyday, StartMinute: 0, EndMinute: 360, Enabled: true},
		{Label: "낮", Weekdays: everyday, StartMinute: 720, EndMinute: 1020, Enabled: true},
	}
	from := kst(2026, 3, 2, 0, 0)
	ivs := Intervals(w, seoul, from, from.AddDate(0, 0, 2))
	r := defaultRules()
	r.JitterMaxMin = 0
	got := Generate(Params{Rules: r, Loc: seoul, Intervals: ivs, Earliest: from, Until: from.AddDate(0, 0, 2), Rand: randx.Default{}})
	perDay := map[string]int{}
	for _, s := range got {
		perDay[s.LocalDate]++
		l := s.At.In(seoul)
		m := l.Hour()*60 + l.Minute()
		inDawn := m >= 0 && m < 360-5
		inDay := m >= 720 && m < 1020-5
		if !inDawn && !inDay {
			t.Fatalf("slot %s outside allowed windows (with end margin)", l)
		}
	}
	// 새벽 00:00~05:30 12개, 낮 12:00~16:30 10개 → 하루 22개 가능하지만 상한 20
	if perDay["2026-03-02"] != 20 || perDay["2026-03-03"] != 20 {
		t.Fatalf("per day = %v", perDay)
	}
	if hm(got)[0] != "03-02 00:00" || hm(got)[12] != "03-02 12:00" {
		t.Fatalf("first slots = %v", hm(got)[:14])
	}
	minN, maxN := Capacity(defaultRules(), w, seoul, from)
	if minN != 20 || maxN != 22 {
		t.Fatalf("capacity = %d~%d", minN, maxN)
	}
}

// 자정을 넘는 구간: 월 23:00~03:00은 월요일 밤~화요일 새벽. 수량은 실제 날짜로 센다
func TestOvernightWindowAndLocalDate(t *testing.T) {
	w := []Window{{Label: "밤", Weekdays: []int{1}, StartMinute: 1380, EndMinute: 180, Enabled: true}} // 월요일만
	from := kst(2026, 3, 1, 0, 0)                                                                     // 일요일
	ivs := Intervals(w, seoul, from, from.AddDate(0, 0, 7))
	if len(ivs) != 1 || !ivs[0].Start.Equal(kst(2026, 3, 2, 23, 0)) || !ivs[0].End.Equal(kst(2026, 3, 3, 3, 0)) {
		t.Fatalf("intervals = %+v", ivs)
	}
	r := defaultRules()
	r.JitterMaxMin = 0
	got := Generate(Params{Rules: r, Loc: seoul, Intervals: ivs, Earliest: from, Until: from.AddDate(0, 0, 7), Rand: randx.Default{}})
	if len(got) != 8 || got[0].LocalDate != "2026-03-02" || got[2].LocalDate != "2026-03-03" {
		t.Fatalf("overnight = %v %v", hm(got), got[2].LocalDate)
	}
	// 화요일 상한이 1이면 자정 이후에는 하나만
	r.DailyTarget = 1
	got = Generate(Params{Rules: r, Loc: seoul, Intervals: ivs, Earliest: from, Until: from.AddDate(0, 0, 7), Rand: randx.Default{}})
	if len(got) != 2 || hm(got)[1] != "03-03 00:00" {
		t.Fatalf("cap by real date = %v", hm(got))
	}
}

// 겹치거나 맞닿은 구간은 합쳐 중복 시각을 만들지 않는다
func TestOverlappingWindowsMerged(t *testing.T) {
	w := []Window{
		{Label: "A", Weekdays: everyday, StartMinute: 600, EndMinute: 720, Enabled: true},
		{Label: "B", Weekdays: everyday, StartMinute: 660, EndMinute: 780, Enabled: true},
		{Label: "C", Weekdays: everyday, StartMinute: 780, EndMinute: 840, Enabled: true},
		{Label: "끔", Weekdays: everyday, StartMinute: 900, EndMinute: 960, Enabled: false},
	}
	from := kst(2026, 3, 2, 0, 0)
	ivs := Intervals(w, seoul, from, from.AddDate(0, 0, 1))
	if len(ivs) != 1 || !ivs[0].Start.Equal(kst(2026, 3, 2, 10, 0)) || !ivs[0].End.Equal(kst(2026, 3, 2, 14, 0)) || len(ivs[0].Labels) != 3 {
		t.Fatalf("merged = %+v", ivs)
	}
	r := defaultRules()
	got := Generate(Params{Rules: r, Loc: seoul, Intervals: ivs, Earliest: from, Until: from.AddDate(0, 0, 1), Rand: randx.NewSeeded(1)})
	seen := map[time.Time]bool{}
	for _, s := range got {
		if seen[s.At] {
			t.Fatal("duplicate slot")
		}
		seen[s.At] = true
	}
}

// 시간 고정 예약과는 기본 간격 이상 떨어지고, 이미 있는 예약 수·쉬는 날·현재 시각을 반영한다
func TestPinnedExcludedAndEarliest(t *testing.T) {
	w := []Window{{Label: "낮", Weekdays: everyday, StartMinute: 720, EndMinute: 1020, Enabled: true}}
	from := kst(2026, 3, 2, 0, 0)
	ivs := Intervals(w, seoul, from, from.AddDate(0, 0, 3))
	r := defaultRules()
	r.JitterMaxMin = 0
	got := Generate(Params{Rules: r, Loc: seoul, Intervals: ivs, Rand: randx.Default{},
		Earliest: kst(2026, 3, 2, 13, 10), // 지금 13:10
		Until:    from.AddDate(0, 0, 3),
		Pinned:   []time.Time{kst(2026, 3, 2, 14, 0)},
		Counts:   map[string]int{"2026-03-02": 1}, // 고정 예약 1개
		Excluded: map[string]bool{"2026-03-03": true},
	})
	h := hm(got)
	if h[0] != "03-02 13:10" || h[1] != "03-02 14:30" {
		t.Fatalf("around pin = %v", h)
	}
	for _, s := range got {
		if s.LocalDate == "2026-03-03" {
			t.Fatal("excluded day got slots")
		}
		if d := s.At.Sub(kst(2026, 3, 2, 14, 0)); d > -30*time.Minute && d < 30*time.Minute {
			t.Fatalf("slot %s too close to pinned 14:00", s.At.In(seoul))
		}
	}
	if got[len(got)-1].LocalDate != "2026-03-04" {
		t.Fatal("generation should continue on the next allowed day")
	}
}

// 넣을 수 없는 수량을 억지로 채우지 않는다: 낮 1시간 구간에는 2개만
func TestDoesNotForceImpossibleCount(t *testing.T) {
	w := []Window{{Label: "짧음", Weekdays: everyday, StartMinute: 720, EndMinute: 780, Enabled: true}}
	from := kst(2026, 3, 2, 0, 0)
	ivs := Intervals(w, seoul, from, from.AddDate(0, 0, 1))
	got := Generate(Params{Rules: defaultRules(), Loc: seoul, Intervals: ivs, Earliest: from, Until: from.AddDate(0, 0, 1), Rand: randx.NewSeeded(3)})
	if len(got) != 2 {
		t.Fatalf("got %v", hm(got))
	}
	ok, _ := Allowed(kst(2026, 3, 2, 12, 56), defaultRules(), ivs, nil, seoul)
	if ok {
		t.Fatal("12:56 is within the end margin (13:00 - 5분)")
	}
	if ok, _ := Allowed(kst(2026, 3, 2, 12, 54), defaultRules(), ivs, nil, seoul); !ok {
		t.Fatal("12:54 should be allowed")
	}
}
