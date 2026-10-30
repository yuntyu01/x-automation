// Package schedule은 허용 시간 구간·하루 묶음 수·간격 규칙으로 예약 시각을 만들고, 예약에 콘텐츠를 배치한다.
//
// 규칙 (docs/BUILD_INSTRUCTIONS.md 6절)
//   - 구간은 시작 포함·끝 제외. 끝이 시작보다 이르면 다음 날로 이어진다 (월 23:00~03:00 = 월요일 밤~화요일 새벽)
//   - 겹치거나 맞닿은 구간은 합쳐서 계산한다 (중복 시각을 만들지 않는다)
//   - 다음 예약 = 이전 예약 + 기본 간격 + 추가 지연(0 이상). 구간의 첫 예약은 구간 시작 (이전 예약과 기본 간격 이상)
//   - 지연값은 예약을 만들 때 한 번 뽑아 저장한다. 다시 계산해도 다시 뽑지 않는다
//   - 하루 묶음 수는 예약 시각의 현지 날짜 기준 상한이다. 넣을 수 없으면 억지로 채우지 않는다
//   - 구간 끝 직전(여유 시간 + 답글 간격)에는 새 묶음을 시작하지 않는다
package schedule

import (
	"sort"
	"time"

	"github.com/yuntyu01/x-automation/internal/randx"
)

const dateLayout = "2006-01-02"

// Rules는 시간표 규칙이다.
type Rules struct {
	DailyTarget       int  `json:"daily_target"`
	BaseIntervalMin   int  `json:"base_interval_min"`
	JitterMinMin      int  `json:"jitter_min_min"`
	JitterMaxMin      int  `json:"jitter_max_min"`
	ReplyGapSec       int  `json:"reply_gap_sec"`
	EndMarginMin      int  `json:"end_margin_min"`
	SubstituteUnready bool `json:"substitute_unready"`
	PolicyVersion     int  `json:"policy_version"`
	Version           int  `json:"version"`
}

func (r Rules) base() time.Duration { return time.Duration(r.BaseIntervalMin) * time.Minute }

// startCutoff는 구간 끝에서 이만큼 전부터 새 묶음을 시작하지 않는 시간이다.
func (r Rules) startCutoff() time.Duration {
	return time.Duration(r.EndMarginMin)*time.Minute + time.Duration(r.ReplyGapSec)*time.Second
}

// Window는 요일별 허용 구간이다. StartMinute·EndMinute는 현지 시각의 0시부터 분.
type Window struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Weekdays    []int  `json:"weekdays"` // 0=일 … 6=토 (구간이 시작하는 요일)
	StartMinute int    `json:"start_minute"`
	EndMinute   int    `json:"end_minute"` // 1440 = 다음 날 0시. 시작보다 작으면 다음 날로 이어짐
	Enabled     bool   `json:"enabled"`
	SortOrder   int    `json:"sort_order"`
}

// Overnight는 자정을 넘기는 구간인지다.
func (w Window) Overnight() bool { return w.EndMinute <= w.StartMinute }

// Interval은 실제 허용 시간 [Start, End)다.
type Interval struct {
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
	Labels []string  `json:"labels"`
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func atMinute(day time.Time, minute int) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), minute/60, minute%60, 0, 0, day.Location())
}

// Intervals는 [from, to) 사이의 허용 구간을 현지 시간대로 펼치고 겹치거나 맞닿은 구간을 합친다.
func Intervals(windows []Window, loc *time.Location, from, to time.Time) []Interval {
	var raw []Interval
	day := startOfDay(from.In(loc)).AddDate(0, 0, -1) // 전날 밤에 시작해 넘어오는 구간 포함
	end := to.In(loc)
	for ; day.Before(end); day = day.AddDate(0, 0, 1) {
		wd := int(day.Weekday())
		for _, w := range windows {
			if !w.Enabled || !containsInt(w.Weekdays, wd) || w.StartMinute == w.EndMinute {
				continue
			}
			s := atMinute(day, w.StartMinute)
			var e time.Time
			if w.Overnight() {
				e = atMinute(day.AddDate(0, 0, 1), w.EndMinute)
			} else {
				e = atMinute(day, w.EndMinute) // 1440이면 다음 날 0시로 정규화된다
			}
			if !e.After(from) || !s.Before(to) {
				continue
			}
			raw = append(raw, Interval{Start: s, End: e, Labels: []string{w.Label}})
		}
	}
	sort.Slice(raw, func(i, j int) bool { return raw[i].Start.Before(raw[j].Start) })
	var out []Interval
	for _, iv := range raw {
		if n := len(out); n > 0 && !iv.Start.After(out[n-1].End) {
			if iv.End.After(out[n-1].End) {
				out[n-1].End = iv.End
			}
			if !containsStr(out[n-1].Labels, iv.Labels[0]) {
				out[n-1].Labels = append(out[n-1].Labels, iv.Labels[0])
			}
			continue
		}
		out = append(out, iv)
	}
	return out
}

func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func containsStr(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// Slot은 새로 만들 예약 시각이다.
type Slot struct {
	At        time.Time `json:"at"`
	JitterSec int       `json:"jitter_sec"` // 이전 예약 + 기본 간격 뒤에 더한 지연 (구간 시작·현재 시각에 맞춘 경우 0)
	LocalDate string    `json:"local_date"`
	Window    string    `json:"window"`
}

// Params는 Generate 입력이다.
type Params struct {
	Rules     Rules
	Loc       *time.Location
	Intervals []Interval      // 허용 시간 (합쳐진 상태)
	Excluded  map[string]bool // 쉬는 날짜 (현지 날짜)
	Counts    map[string]int  // 이미 있는 예약 수 (현지 날짜별). Generate가 채워 나간다
	Prev      time.Time       // 직전 예약(실행 포함) 시각. 없으면 zero
	Pinned    []time.Time     // 시간 고정 예약 (간격 계산에 포함, 수량은 Counts에 이미 포함)
	Earliest  time.Time       // 이보다 이른 예약은 만들지 않는다 (현재 시각 + 준비 여유, 적용 범위 시작)
	Until     time.Time       // 이 시각 이후는 만들지 않는다
	Rand      randx.Source    // 추가 지연 (테스트에서는 고정값)
	Limit     int             // 0이면 제한 없음
	JitterFor func(i int) int // 지정하면 Rand 대신 i번째 예약의 지연(분)을 쓴다 (기존 지연 재사용)
}

func (p Params) jitter(i int) time.Duration {
	if p.JitterFor != nil {
		return time.Duration(p.JitterFor(i)) * time.Minute
	}
	span := p.Rules.JitterMaxMin - p.Rules.JitterMinMin
	m := p.Rules.JitterMinMin
	if span > 0 {
		m += p.Rand.IntN(span + 1)
	}
	return time.Duration(m) * time.Minute
}

// Generate는 규칙에 맞는 새 예약 시각을 만든다. 넣을 수 없는 수량은 만들지 않는다.
func Generate(p Params) []Slot {
	if p.Counts == nil {
		p.Counts = map[string]int{}
	}
	base := p.Rules.base()
	cutoff := p.Rules.startCutoff()
	pins := append([]time.Time(nil), p.Pinned...)
	sort.Slice(pins, func(i, j int) bool { return pins[i].Before(pins[j]) })
	pi := 0
	prev := p.Prev
	var out []Slot
	var j time.Duration
	drawn := false // 지연은 이전 예약이 있을 때만, 예약 하나당 한 번 뽑는다
	for _, iv := range p.Intervals {
		floor := iv.Start
		if floor.Before(p.Earliest) {
			floor = p.Earliest
		}
		lastStart := iv.End.Add(-cutoff) // 이 시각 이상이면 새 묶음을 시작하지 않는다
		for {
			if !p.Until.IsZero() && !floor.Before(p.Until) {
				return out
			}
			// 다음 = max(구간 시작·현재 시각, 이전 + 기본 간격 + 지연). 지연은 앞의 식으로 정해진 경우에만 기록한다
			cand, jit := floor, time.Duration(0)
			if !prev.IsZero() {
				if !drawn {
					j, drawn = p.jitter(len(out)), true
				}
				if c := prev.Add(base + j); c.After(cand) {
					cand, jit = c, j
				}
			}
			// 시간 고정 예약과 기본 간격 이상 떨어져야 한다. 가까우면 고정 예약 뒤로 간다
			moved := false
			for pi < len(pins) && pins[pi].Before(cand.Add(base)) {
				if pins[pi].After(prev) || prev.IsZero() {
					prev = pins[pi]
				}
				pi++
				moved = true
			}
			if moved {
				continue
			}
			if !cand.Before(lastStart) || (!p.Until.IsZero() && !cand.Before(p.Until)) {
				break // 이 구간에는 더 넣을 수 없다
			}
			local := cand.In(p.Loc)
			date := local.Format(dateLayout)
			if p.Excluded[date] || p.Counts[date] >= p.Rules.DailyTarget {
				// 그날은 더 넣지 않는다: 다음 날 0시부터 다시 본다
				next := startOfDay(local).AddDate(0, 0, 1)
				if !next.Before(lastStart) {
					break
				}
				floor = next
				continue
			}
			out = append(out, Slot{At: cand, JitterSec: int(jit / time.Second), LocalDate: date, Window: labelOf(iv)})
			p.Counts[date]++
			prev = cand
			if p.Limit > 0 && len(out) >= p.Limit {
				return out
			}
			drawn = false
			floor = cand
		}
	}
	return out
}

func labelOf(iv Interval) string {
	if len(iv.Labels) == 0 {
		return ""
	}
	s := iv.Labels[0]
	for _, l := range iv.Labels[1:] {
		s += "·" + l
	}
	return s
}

// Allowed는 t에 새 묶음을 시작해도 되는지다 (허용 구간 안, 구간 끝 여유 전, 쉬는 날 아님).
func Allowed(t time.Time, rules Rules, intervals []Interval, excluded map[string]bool, loc *time.Location) (bool, string) {
	if excluded[t.In(loc).Format(dateLayout)] {
		return false, "쉬는 날로 지정된 날짜예요"
	}
	for _, iv := range intervals {
		if !t.Before(iv.Start) && t.Before(iv.End) {
			if !t.Before(iv.End.Add(-rules.startCutoff())) {
				return false, "허용 구간 끝이 가까워 새 묶음을 시작하지 않아요"
			}
			return true, ""
		}
	}
	return false, "허용 시간 구간 밖이에요"
}

// Inside는 t가 쉬는 날이 아닌 허용 구간 안인지다. 구간 끝 여유는 새 묶음 시작에만 쓰므로 여기서는 보지 않는다.
func Inside(t time.Time, intervals []Interval, excluded map[string]bool, loc *time.Location) bool {
	if excluded[t.In(loc).Format(dateLayout)] {
		return false
	}
	for _, iv := range intervals {
		if !t.Before(iv.Start) && t.Before(iv.End) {
			return true
		}
	}
	return false
}

// Capacity는 하루 허용 구간에 넣을 수 있는 묶음 수의 범위다 (추가 지연이 최대일 때 ~ 최소일 때).
func Capacity(rules Rules, windows []Window, loc *time.Location, day time.Time) (minN, maxN int) {
	from := startOfDay(day.In(loc))
	ivs := Intervals(windows, loc, from, from.AddDate(0, 0, 1))
	count := func(jit int) int {
		r := rules
		r.DailyTarget = 1 << 30
		r.JitterMinMin, r.JitterMaxMin = jit, jit
		return len(Generate(Params{Rules: r, Loc: loc, Intervals: ivs, Earliest: from, Until: from.AddDate(0, 0, 1), Rand: randx.Default{}}))
	}
	return count(rules.JitterMaxMin), count(rules.JitterMinMin)
}
