package schedule

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yuntyu01/x-automation/internal/randx"
	"github.com/yuntyu01/x-automation/internal/settings"
	"github.com/yuntyu01/x-automation/internal/store"
)

// RebuildInput은 다시 배치할 범위다. To가 zero면 확보 기간 끝까지.
type RebuildInput struct {
	From, To time.Time
	Seed     uint64 // 미리보기와 적용이 같은 시각을 내도록 같은 씨앗을 쓴다
}

type PlanSlot struct {
	At        time.Time `json:"at"`
	LocalDate string    `json:"local_date"`
	Window    string    `json:"window"`
	JitterSec int       `json:"jitter_sec"`
	Title     *string   `json:"title"`
}

type DayPlan struct {
	Date        string `json:"date"`
	Target      int    `json:"target"`
	Count       int    `json:"count"`
	Shortfall   int    `json:"shortfall"`
	CapacityMin int    `json:"capacity_min"`
	CapacityMax int    `json:"capacity_max"`
	Excluded    bool   `json:"excluded"`
	Partial     bool   `json:"partial"` // 오늘처럼 일부 시간만 남은 날
}

type Report struct {
	Seed       uint64     `json:"seed"`
	From       time.Time  `json:"from"`
	To         time.Time  `json:"to"`
	Slots      []PlanSlot `json:"slots"`
	Days       []DayPlan  `json:"days"`
	Canceled   int        `json:"canceled"`   // 다시 만든 기존 예약 수
	Reassigned int        `json:"reassigned"` // 새 예약에 다시 넣은 글
	Released   int        `json:"released"`   // 자리가 모자라 보관함으로 돌아간 글
	Pinned     int        `json:"pinned"`     // 그대로 둔 고정 예약
}

var errDryRun = errors.New("dry run")

// rebuildTx는 범위 안의 실행 전·고정 아닌 예약을 새 규칙으로 다시 만들고, 있던 글을 순서대로 다시 넣는다.
// 게시 중·완료 예약과 고정 예약은 그대로 둔다. 일정만 바꾸고 글·이미지를 다시 생성하지 않는다.
func (s *Service) rebuildTx(ctx context.Context, tx pgx.Tx, c config, in RebuildInput) (Report, error) {
	now := s.clock.Now()
	from := in.From
	if from.Before(now.Add(lead)) {
		from = now.Add(lead)
	}
	to := in.To
	if to.IsZero() {
		to = c.horizonEnd(now)
	}
	rep := Report{Seed: in.Seed, From: from, To: to, Slots: []PlanSlot{}, Days: []DayPlan{}}
	rows, err := tx.Query(ctx, `UPDATE reservations SET state = 'canceled', delay_note = '시간표를 바꿔 다시 배치했어요', updated_at = now()
		WHERE id IN (SELECT id FROM reservations WHERE state IN `+pendingStates+` AND NOT pinned AND slot_at >= $1 AND slot_at < $2 ORDER BY slot_at FOR UPDATE)
		RETURNING slot_at, content_id`, from, to)
	if err != nil {
		return rep, err
	}
	type old struct {
		at      time.Time
		content *string
	}
	olds, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (old, error) {
		var o old
		return o, r.Scan(&o.at, &o.content)
	})
	if err != nil {
		return rep, err
	}
	sort.Slice(olds, func(i, j int) bool { return olds[i].at.Before(olds[j].at) })
	rep.Canceled = len(olds)

	var prev *time.Time
	if err := tx.QueryRow(ctx, `SELECT max(slot_at) FROM reservations WHERE state IN `+activeStates+` AND slot_at < $1`, from).Scan(&prev); err != nil {
		return rep, err
	}
	var p time.Time
	if prev != nil {
		p = *prev
	}
	pins, err := pinnedAfterAny(ctx, tx, from)
	if err != nil {
		return rep, err
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM reservations WHERE state IN `+pendingStates+` AND pinned AND slot_at >= $1 AND slot_at < $2`, from, to).Scan(&rep.Pinned); err != nil {
		return rep, err
	}
	counts, err := countsByDate(ctx, tx, from.In(c.loc).AddDate(0, 0, -1).Format(dateLayout))
	if err != nil {
		return rep, err
	}
	slots := Generate(Params{Rules: c.rules, Loc: c.loc, Intervals: Intervals(c.windows, c.loc, from.Add(-24*time.Hour), to),
		Excluded: c.excluded, Counts: counts, Prev: p, Pinned: pins, Earliest: from, Until: to, Rand: randx.NewSeeded(in.Seed)})
	ids, err := insertSlots(ctx, tx, slots, c.rules.PolicyVersion)
	if err != nil {
		return rep, err
	}
	review := c.st.OperationMode == settings.ModeReview
	next := 0
	titles := map[int]*string{}
	for _, o := range olds {
		if o.content == nil {
			continue
		}
		if next >= len(ids) {
			rep.Released++
			continue
		}
		if err := assign(ctx, tx, ids[next], *o.content, review); err != nil {
			var ve *ValidationError
			if errors.As(err, &ve) {
				rep.Released++
				continue
			}
			return rep, err
		}
		var t string
		_ = tx.QueryRow(ctx, `SELECT title FROM contents WHERE id = $1`, *o.content).Scan(&t)
		titles[next] = &t
		rep.Reassigned++
		next++
	}
	for i, sl := range slots {
		rep.Slots = append(rep.Slots, PlanSlot{At: sl.At, LocalDate: sl.LocalDate, Window: sl.Window, JitterSec: sl.JitterSec, Title: titles[i]})
	}
	// 날짜별 요약 (고정·실행된 예약 포함)
	after, err := countsByDate(ctx, tx, from.In(c.loc).Format(dateLayout))
	if err != nil {
		return rep, err
	}
	today := now.In(c.loc).Format(dateLayout)
	for d := startOfDay(from.In(c.loc)); d.Before(to); d = d.AddDate(0, 0, 1) {
		date := d.Format(dateLayout)
		dp := DayPlan{Date: date, Target: c.rules.DailyTarget, Count: after[date], Excluded: c.excluded[date], Partial: date == today || d.Before(from)}
		dp.CapacityMin, dp.CapacityMax = Capacity(c.rules, c.windows, c.loc, d)
		if !dp.Excluded && !dp.Partial && dp.Count < dp.Target {
			dp.Shortfall = dp.Target - dp.Count
		}
		rep.Days = append(rep.Days, dp)
	}
	return rep, nil
}

func pinnedAfterAny(ctx context.Context, db store.DB, t time.Time) ([]time.Time, error) {
	// 범위 뒤 첫 예약과도 간격을 지키도록, 범위 시작 뒤의 남은 모든 예약을 고정점으로 본다
	rows, err := db.Query(ctx, `SELECT slot_at FROM reservations WHERE state IN `+activeStates+` AND slot_at >= $1 ORDER BY slot_at`, t)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[time.Time])
}

// Rebuild는 범위 안의 예약을 지금 규칙으로 다시 만든다. dryRun이면 저장하지 않고 결과만 돌려준다.
func (s *Service) Rebuild(ctx context.Context, in RebuildInput, dryRun bool) (Report, error) {
	var rep Report
	err := store.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.lock(ctx, tx); err != nil {
			return err
		}
		c, err := s.load(ctx, tx)
		if err != nil {
			return err
		}
		if rep, err = s.rebuildTx(ctx, tx, c, in); err != nil {
			return err
		}
		if dryRun {
			return errDryRun
		}
		return nil
	})
	if errors.Is(err, errDryRun) {
		err = nil
	}
	if err == nil && !dryRun {
		_ = s.Refresh(ctx)
		s.kick()
	}
	return rep, err
}

// RulesInput은 시간표 설정 저장 요청이다.
type RulesInput struct {
	Rules   Rules    `json:"rules"`
	Windows []Window `json:"windows"`
	Scope   string   `json:"scope"` // tomorrow(내일부터 다시 배치) | now(지금 이후 모두) | later(이미 만든 예약은 그대로)
	Seed    uint64   `json:"seed"`
}

func (s *Service) scopeRange(c config, scope string) (RebuildInput, bool) {
	now := s.clock.Now()
	switch scope {
	case "now":
		return RebuildInput{From: now}, true
	case "tomorrow", "":
		return RebuildInput{From: startOfDay(now.In(c.loc)).AddDate(0, 0, 1)}, true
	}
	return RebuildInput{}, false
}

// SaveRules는 규칙·구간을 저장하고, 고른 범위의 예약을 다시 배치한다. dryRun이면 미리보기만 한다.
func (s *Service) SaveRules(ctx context.Context, in RulesInput, dryRun bool) (Report, error) {
	if f := ValidateRules(in.Rules, in.Windows); len(f) > 0 {
		return Report{}, &ValidationError{Fields: f}
	}
	switch in.Scope {
	case "", "tomorrow", "now", "later":
	default:
		return Report{}, &ValidationError{Fields: map[string]string{"scope": "적용 범위를 고르세요"}}
	}
	if in.Seed == 0 {
		in.Seed = uint64(s.clock.Now().UnixNano())
	}
	var rep Report
	err := store.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.lock(ctx, tx); err != nil {
			return err
		}
		if err := saveRules(ctx, tx, in.Rules, in.Windows); err != nil {
			return err
		}
		c, err := s.load(ctx, tx)
		if err != nil {
			return err
		}
		if r, ok := s.scopeRange(c, in.Scope); ok {
			r.Seed = in.Seed
			if rep, err = s.rebuildTx(ctx, tx, c, r); err != nil {
				return err
			}
		} else {
			rep = Report{Seed: in.Seed, Slots: []PlanSlot{}, Days: []DayPlan{}}
			now := s.clock.Now()
			for d := startOfDay(now.In(c.loc)); d.Before(c.horizonEnd(now)); d = d.AddDate(0, 0, 1) {
				dp := DayPlan{Date: d.Format(dateLayout), Target: c.rules.DailyTarget, Excluded: c.excluded[d.Format(dateLayout)]}
				dp.CapacityMin, dp.CapacityMax = Capacity(c.rules, c.windows, c.loc, d)
				rep.Days = append(rep.Days, dp)
			}
		}
		if dryRun {
			return errDryRun
		}
		return nil
	})
	if errors.Is(err, errDryRun) {
		err = nil
	}
	if err == nil && !dryRun {
		if _, err := s.Ensure(ctx); err != nil {
			return rep, err
		}
		_ = s.Refresh(ctx)
		s.kick()
	}
	return rep, err
}

// ── 화면용 목록 ───────────────────────────────────────────────────────

type Item struct {
	ID               string     `json:"id"`
	SlotAt           time.Time  `json:"slot_at"`
	OriginalSlotAt   time.Time  `json:"original_slot_at"`
	JitterSec        int        `json:"jitter_sec"`
	LocalDate        string     `json:"local_date"`
	Pinned           bool       `json:"pinned"`
	Manual           bool       `json:"manual"`
	State            string     `json:"state"`
	WaitReason       *string    `json:"wait_reason"`
	DelayNote        *string    `json:"delay_note"`
	GenerationDueAt  *time.Time `json:"generation_due_at"`
	Generating       bool       `json:"generating"`
	ContentID        *string    `json:"content_id"`
	Title            *string    `json:"title"`
	Preview          *string    `json:"preview"`
	Stage            *string    `json:"stage"`
	PartCount        int        `json:"part_count"`
	ImageCount       int        `json:"image_count"`
	UnapprovedChange bool       `json:"unapproved_change"` // 승인 뒤 고친 내용이 있어 승인본으로 게시한다
	Window           string     `json:"window"`
	BlockReason      *string    `json:"block_reason"` // 시각이 됐지만 게시를 시작하지 못한 이유 (실게시 꺼짐·연결 필요 등)
	RunID            *string    `json:"run_id"`       // 가장 최근 게시 실행
	RunNote          *string    `json:"run_note"`
	PostURL          *string    `json:"post_url"` // 본문 X 주소
}

type DayView struct {
	Date        string     `json:"date"`
	Target      int        `json:"target"`
	Count       int        `json:"count"`
	Posts       int        `json:"posts"`        // 예상 X 게시물 수 (본문 + 답글)
	PostsApprox bool       `json:"posts_approx"` // 빈 예약은 본문 + 답글 1개로 어림
	Excluded    bool       `json:"excluded"`
	ExcludeNote string     `json:"exclude_note"`
	CapacityMin int        `json:"capacity_min"`
	CapacityMax int        `json:"capacity_max"`
	Intervals   []Interval `json:"intervals"`
	Items       []Item     `json:"items"`
}

// List는 from 날짜부터 days일의 예약을 날짜별로 돌려준다.
func (s *Service) List(ctx context.Context, from string, days int) ([]DayView, error) {
	c, err := s.load(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	start, err := time.ParseInLocation(dateLayout, from, c.loc)
	if err != nil {
		start = startOfDay(s.clock.Now().In(c.loc))
	}
	if days < 1 || days > 14 {
		days = 1
	}
	end := start.AddDate(0, 0, days)
	exNotes := map[string]string{}
	ex, err := loadExceptions(ctx, s.pool, start.Format(dateLayout))
	if err != nil {
		return nil, err
	}
	for _, e := range ex {
		exNotes[e.Date] = e.Note
	}
	ivs := Intervals(c.windows, c.loc, start, end)
	rows, err := s.pool.Query(ctx, `SELECT r.id, r.slot_at, r.original_slot_at, r.jitter_seconds, to_char(r.local_date, 'YYYY-MM-DD'), r.pinned, r.manual,
		r.state, r.wait_reason, r.delay_note, r.generation_due_at,
		EXISTS (SELECT 1 FROM generation_requests g WHERE g.reservation_id = r.id AND g.state IN ('queued', 'running')),
		r.content_id, c.title, left(rv.raw_text, 140), o.stage,
		(SELECT count(*) FROM thread_parts tp WHERE tp.revision_id = r.revision_id),
		(SELECT count(*) FROM part_media pm WHERE pm.revision_id = r.revision_id),
		COALESCE(c.approved_revision_id IS NOT NULL AND c.current_revision_id <> c.approved_revision_id, false),
		r.block_reason, lr.id, lr.note, lr.x_url
		FROM reservations r
		LEFT JOIN LATERAL (SELECT pr.id, pr.note, pp.x_url FROM publish_runs pr
			LEFT JOIN publish_parts pp ON pp.run_id = pr.id AND pp.position = 0
			WHERE pr.reservation_id = r.id ORDER BY pr.created_at DESC LIMIT 1) lr ON true
		LEFT JOIN contents c ON c.id = r.content_id
		LEFT JOIN content_overview o ON o.id = r.content_id
		LEFT JOIN content_revisions rv ON rv.id = COALESCE(r.revision_id, c.current_revision_id)
		WHERE r.local_date >= $1::date AND r.local_date < $2::date
		  AND (r.state <> 'canceled' OR (r.delay_note IS NOT NULL AND r.delay_note NOT LIKE '시간표를 바꿔%'))
		ORDER BY r.slot_at, r.id`, start.Format(dateLayout), end.Format(dateLayout))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byDate := map[string][]Item{}
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.ID, &it.SlotAt, &it.OriginalSlotAt, &it.JitterSec, &it.LocalDate, &it.Pinned, &it.Manual, &it.State, &it.WaitReason,
			&it.DelayNote, &it.GenerationDueAt, &it.Generating, &it.ContentID, &it.Title, &it.Preview, &it.Stage, &it.PartCount, &it.ImageCount,
			&it.UnapprovedChange, &it.BlockReason, &it.RunID, &it.RunNote, &it.PostURL); err != nil {
			return nil, err
		}
		for _, iv := range ivs {
			if !it.SlotAt.Before(iv.Start) && it.SlotAt.Before(iv.End) {
				it.Window = labelOf(iv)
			}
		}
		byDate[it.LocalDate] = append(byDate[it.LocalDate], it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []DayView
	for d := start; d.Before(end); d = d.AddDate(0, 0, 1) {
		date := d.Format(dateLayout)
		dv := DayView{Date: date, Target: c.rules.DailyTarget, Excluded: c.excluded[date], ExcludeNote: exNotes[date], Items: byDate[date], Intervals: []Interval{}}
		if dv.Items == nil {
			dv.Items = []Item{}
		}
		dv.CapacityMin, dv.CapacityMax = Capacity(c.rules, c.windows, c.loc, d)
		dv.Intervals = append(dv.Intervals, Intervals(c.windows, c.loc, d, d.AddDate(0, 0, 1))...)
		for _, it := range dv.Items {
			if it.State == "canceled" || it.State == "failed" {
				continue
			}
			dv.Count++
			if it.ContentID != nil && it.PartCount > 0 {
				dv.Posts += it.PartCount
			} else {
				dv.Posts += 2
				dv.PostsApprox = true
			}
		}
		out = append(out, dv)
	}
	return out, nil
}
