// Package settings는 앱에서 바꿀 수 있는 운영 설정을 다룬다.
// 비밀값·인증 설정은 여기 두지 않는다 (환경변수 전용).
package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/yuntyu01/x-automation/internal/store"
)

type OperationMode string

const (
	ModeFullAuto    OperationMode = "full_auto"    // 준비·검증 통과분을 예약 게시
	ModeReview      OperationMode = "review"       // 승인한 revision만 게시
	ModePublishOnly OperationMode = "publish_only" // 사용자가 등록한 글만 분할·예약·게시
)

func (m OperationMode) Valid() bool {
	return m == ModeFullAuto || m == ModeReview || m == ModePublishOnly
}

// Settings는 app_settings 한 행이다.
type Settings struct {
	AutomationEnabled   bool          `json:"automation_enabled"`
	AutomationChangedAt *time.Time    `json:"automation_changed_at"`
	GenerationPaused    bool          `json:"generation_paused"`
	PublishingPaused    bool          `json:"publishing_paused"`
	OperationMode       OperationMode `json:"operation_mode"`
	Timezone            string        `json:"timezone"`
	SetupCompletedAt    *time.Time    `json:"setup_completed_at"`
	Policy              Policy        `json:"policy"`
	Version             int           `json:"version"`
	UpdatedAt           time.Time     `json:"updated_at"`
}

// Location은 설정된 시간대를 돌려준다 (잘못된 값이면 Asia/Seoul).
func (s Settings) Location() *time.Location {
	if loc, err := time.LoadLocation(s.Timezone); err == nil {
		return loc
	}
	loc, _ := time.LoadLocation("Asia/Seoul")
	return loc
}

type Policy struct {
	Generation GenerationPolicy `json:"generation"`
	Thread     ThreadPolicy     `json:"thread"`
	Image      ImagePolicy      `json:"image"`
	Retention  RetentionPolicy  `json:"retention"`
	Budget     BudgetPolicy     `json:"budget"`
	Pricing    Pricing          `json:"pricing"`
}

type GenerationPolicy struct {
	TextEnabled      bool   `json:"text_enabled"`
	ImageEnabled     bool   `json:"image_enabled"`
	Timing           string `json:"timing"`            // on_publish | manual | prepare
	PrepareStrategy  string `json:"prepare_strategy"`  // daily_at | before_slot | stock
	DailyAt          string `json:"daily_at"`          // HH:MM (현지 시각)
	HorizonDays      int    `json:"horizon_days"`      // 매일 지정 시각 전략의 확보 기간
	BeforeMinutes    int    `json:"before_minutes"`    // 예약 전 N분
	StockMin         int    `json:"stock_min"`         // 재고 임계치
	StockTarget      int    `json:"stock_target"`      // 목표 재고
	FillOnStart      bool   `json:"fill_on_start"`     // 첫 운영 시작·가까운 예약 추가 시 바로 보충
	DefaultPromptID  string `json:"default_prompt_id"` // 자동 생성에 쓸 프롬프트
	TextModel        string `json:"text_model"`
	DailyLimit       int    `json:"daily_limit"`       // 하루 생성 상한 (묶음)
	MaxRegenerations int    `json:"max_regenerations"` // 한 콘텐츠의 재생성 상한
	MaxConcurrent    int    `json:"max_concurrent"`
	MaxAttempts      int    `json:"max_attempts"`
	TextOnlyFallback bool   `json:"text_only_fallback"` // 이미지 실패 시 텍스트만 게시 (사용자가 고른 경우만)
	PolicyVersion    int    `json:"policy_version"`
}

type ThreadPolicy struct {
	SplitMode       string `json:"split_mode"`        // two_parts | overflow | manual
	MaxReplies      int    `json:"max_replies"`       // 0이면 제한 없음. 넘으면 수정 대기
	Numbering       bool   `json:"numbering"`         // 1/N 표기
	LongPostEnabled bool   `json:"long_post_enabled"` // 장문 게시 (계정·API 능력 확인 후에만)
}

type ImagePolicy struct {
	DefaultMode string `json:"default_mode"` // none | upload | ai
	Model       string `json:"model"`
	Quality     string `json:"quality"`
	Size        string `json:"size"`
	Style       string `json:"style"`
	Count       int    `json:"count"`
	Placement   int    `json:"placement"`    // 0=본문, 1=첫 답글 …
	MadeWithAI  bool   `json:"made_with_ai"` // AI 생성 이미지가 붙은 게시에 X의 AI 표시(made_with_ai)를 보낸다
}

type RetentionPolicy struct {
	Days int `json:"days"`
}

type BudgetPolicy struct {
	MonthlyLimitMicros int64  `json:"monthly_limit_micros"` // 0이면 한도 없음
	Currency           string `json:"currency"`
	OnLimit            string `json:"on_limit"` // stop_generation | stop_all
}

// Pricing은 추정 비용 계산용 요율표다. 확인일·출처가 없는 값은 화면에 "미확인"으로 표시한다.
type Pricing struct {
	Version            string `json:"version"`
	VerifiedAt         string `json:"verified_at"`
	Source             string `json:"source"`
	Currency           string `json:"currency"`
	XPostMicros        int64  `json:"x_post_micros"`
	XPostWithURLMicros int64  `json:"x_post_with_url_micros"`
	XMediaMicros       int64  `json:"x_media_micros"`
	TextInputPerMTok   int64  `json:"text_input_per_mtok_micros"`
	TextOutputPerMTok  int64  `json:"text_output_per_mtok_micros"`
	ImagePerImage      int64  `json:"image_per_image_micros"`
}

// Defaults는 제안 초기값이다 (지시서 2.2). 사용자 확정값이 아니며 첫 설정에서 바꿀 수 있다.
func Defaults() Policy {
	return Policy{
		Generation: GenerationPolicy{
			TextEnabled: true, ImageEnabled: false,
			Timing: "prepare", PrepareStrategy: "daily_at",
			DailyAt: "21:00", HorizonDays: 1, BeforeMinutes: 30,
			StockMin: 5, StockTarget: 20, FillOnStart: true,
			DailyLimit: 40, MaxRegenerations: 3, MaxConcurrent: 2, MaxAttempts: 3,
			PolicyVersion: 1,
		},
		Thread:    ThreadPolicy{SplitMode: "two_parts", MaxReplies: 0},
		Image:     ImagePolicy{DefaultMode: "none", Quality: "medium", Size: "1024x1024", Count: 1, MadeWithAI: true},
		Retention: RetentionPolicy{Days: 14},
		Budget:    BudgetPolicy{Currency: "USD", OnLimit: "stop_generation"},
		Pricing:   Pricing{Currency: "USD"},
	}
}

var hhmm = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

// Validate는 필드별 오류를 돌려준다 (입력 옆 표시용).
func (p Policy) Validate() map[string]string {
	errs := map[string]string{}
	g := p.Generation
	switch g.Timing {
	case "on_publish", "manual", "prepare":
	default:
		errs["generation.timing"] = "생성 시점을 선택하세요"
	}
	switch g.PrepareStrategy {
	case "daily_at", "before_slot", "stock":
	default:
		errs["generation.prepare_strategy"] = "미리 생성 방식을 선택하세요"
	}
	if !hhmm.MatchString(g.DailyAt) {
		errs["generation.daily_at"] = "HH:MM 형식으로 입력하세요"
	}
	if g.HorizonDays < 1 || g.HorizonDays > 7 {
		errs["generation.horizon_days"] = "1~7일 사이로 입력하세요"
	}
	if g.BeforeMinutes < 5 || g.BeforeMinutes > 24*60 {
		errs["generation.before_minutes"] = "5분~24시간 사이로 입력하세요"
	}
	if g.StockMin < 0 || g.StockTarget < 1 || g.StockTarget > 200 || g.StockMin > g.StockTarget {
		errs["generation.stock_target"] = "목표 재고는 1~200이고 임계치보다 커야 합니다"
	}
	if g.DailyLimit < 1 || g.DailyLimit > 500 {
		errs["generation.daily_limit"] = "하루 생성 상한은 1~500입니다"
	}
	if g.MaxRegenerations < 0 || g.MaxRegenerations > 20 {
		errs["generation.max_regenerations"] = "재생성 상한은 0~20입니다"
	}
	if g.MaxConcurrent < 1 || g.MaxConcurrent > 8 {
		errs["generation.max_concurrent"] = "동시 작업 수는 1~8입니다"
	}
	if g.MaxAttempts < 1 || g.MaxAttempts > 10 {
		errs["generation.max_attempts"] = "재시도 상한은 1~10입니다"
	}
	switch p.Thread.SplitMode {
	case "two_parts", "overflow", "manual":
	default:
		errs["thread.split_mode"] = "분할 방식을 선택하세요"
	}
	if p.Thread.MaxReplies < 0 || p.Thread.MaxReplies > 25 {
		errs["thread.max_replies"] = "최대 답글 수는 0(제한 없음)~25입니다"
	}
	switch p.Image.DefaultMode {
	case "none", "upload", "ai":
	default:
		errs["image.default_mode"] = "이미지 방식을 선택하세요"
	}
	if p.Image.Count < 1 || p.Image.Count > 4 {
		errs["image.count"] = "이미지는 묶음당 1~4장입니다"
	}
	if p.Image.Placement < 0 || p.Image.Placement > 25 {
		errs["image.placement"] = "이미지 위치가 올바르지 않습니다"
	}
	if p.Retention.Days < 1 || p.Retention.Days > 365 {
		errs["retention.days"] = "보관 기간은 1~365일입니다"
	}
	if p.Budget.MonthlyLimitMicros < 0 {
		errs["budget.monthly_limit_micros"] = "한도는 0 이상이어야 합니다"
	}
	switch p.Budget.OnLimit {
	case "stop_generation", "stop_all":
	default:
		errs["budget.on_limit"] = "한도 도달 시 동작을 선택하세요"
	}
	for k, v := range map[string]int64{
		"pricing.x_post_micros": p.Pricing.XPostMicros, "pricing.x_post_with_url_micros": p.Pricing.XPostWithURLMicros,
		"pricing.x_media_micros": p.Pricing.XMediaMicros, "pricing.text_input_per_mtok_micros": p.Pricing.TextInputPerMTok,
		"pricing.text_output_per_mtok_micros": p.Pricing.TextOutputPerMTok, "pricing.image_per_image_micros": p.Pricing.ImagePerImage,
	} {
		if v < 0 {
			errs[k] = "단가는 0 이상이어야 합니다"
		}
	}
	return errs
}

var ErrVersionConflict = errors.New("settings version conflict")

type Repo struct{ db store.DB }

func NewRepo(db store.DB) *Repo { return &Repo{db: db} }

// Get은 설정을 읽는다. 저장된 정책에 없는 필드는 기본값으로 채운다.
func (r *Repo) Get(ctx context.Context) (Settings, error) {
	var s Settings
	var raw []byte
	err := r.db.QueryRow(ctx, `SELECT automation_enabled, automation_changed_at, generation_paused, publishing_paused,
		operation_mode, timezone, setup_completed_at, config, version, updated_at FROM app_settings WHERE id = 1`).
		Scan(&s.AutomationEnabled, &s.AutomationChangedAt, &s.GenerationPaused, &s.PublishingPaused,
			&s.OperationMode, &s.Timezone, &s.SetupCompletedAt, &raw, &s.Version, &s.UpdatedAt)
	if err != nil {
		return s, err
	}
	s.Policy = Defaults()
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &s.Policy); err != nil {
			return s, fmt.Errorf("저장된 설정을 읽을 수 없습니다: %w", err)
		}
	}
	return s, nil
}

// Update는 운영 모드·시간대·정책을 바꾼다. version이 다르면 충돌로 거부한다.
func (r *Repo) Update(ctx context.Context, version int, mode OperationMode, tz string, p Policy) (Settings, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return Settings{}, err
	}
	tag, err := r.db.Exec(ctx, `UPDATE app_settings SET operation_mode = $1, timezone = $2, config = $3,
		version = version + 1, updated_at = now() WHERE id = 1 AND version = $4`, string(mode), tz, raw, version)
	if err != nil {
		return Settings{}, err
	}
	if tag.RowsAffected() == 0 {
		return Settings{}, ErrVersionConflict
	}
	return r.Get(ctx)
}

// SetFlags는 자동 운영·생성 정지·게시 정지 스위치를 바꾼다. nil이면 그대로 둔다.
func (r *Repo) SetFlags(ctx context.Context, automation, genPaused, pubPaused *bool) (Settings, error) {
	_, err := r.db.Exec(ctx, `UPDATE app_settings SET
		automation_changed_at = CASE WHEN $1::boolean IS NOT NULL AND $1::boolean <> automation_enabled THEN now() ELSE automation_changed_at END,
		automation_enabled = COALESCE($1, automation_enabled),
		generation_paused = COALESCE($2, generation_paused),
		publishing_paused = COALESCE($3, publishing_paused),
		version = version + 1, updated_at = now() WHERE id = 1`, automation, genPaused, pubPaused)
	if err != nil {
		return Settings{}, err
	}
	return r.Get(ctx)
}

// CompleteSetup은 첫 설정 완료 시각을 남긴다.
func (r *Repo) CompleteSetup(ctx context.Context) error {
	_, err := r.db.Exec(ctx, `UPDATE app_settings SET setup_completed_at = COALESCE(setup_completed_at, now()), updated_at = now() WHERE id = 1`)
	return err
}
