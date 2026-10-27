package settings_test

import (
	"context"
	"errors"
	"testing"

	"github.com/yuntyu01/x-automation/internal/settings"
	"github.com/yuntyu01/x-automation/internal/testutil/pgtest"
)

func TestDefaultsAreValid(t *testing.T) {
	if errs := settings.Defaults().Validate(); len(errs) != 0 {
		t.Fatalf("defaults invalid: %v", errs)
	}
}

func TestValidateReportsFields(t *testing.T) {
	p := settings.Defaults()
	p.Generation.DailyAt = "25:00"
	p.Retention.Days = 0
	errs := p.Validate()
	if errs["generation.daily_at"] == "" || errs["retention.days"] == "" {
		t.Fatalf("missing field errors: %v", errs)
	}
}

func TestAutomationStartsOffAndPersists(t *testing.T) {
	pool := pgtest.New(t)
	ctx := context.Background()
	repo := settings.NewRepo(pool)
	s, err := repo.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s.AutomationEnabled || s.Policy.Retention.Days != 14 || s.Timezone != "Asia/Seoul" {
		t.Fatalf("initial = %+v", s)
	}
	on := true
	s, err = repo.SetFlags(ctx, &on, nil, nil)
	if err != nil || !s.AutomationEnabled || s.AutomationChangedAt == nil {
		t.Fatalf("set flags: %+v %v", s, err)
	}
	// 정책 변경이 자동 운영 상태를 초기화하지 않는다
	p := s.Policy
	p.Retention.Days = 30
	s2, err := repo.Update(ctx, s.Version, settings.ModeFullAuto, "Asia/Seoul", p)
	if err != nil || !s2.AutomationEnabled || s2.Policy.Retention.Days != 30 {
		t.Fatalf("update: %+v %v", s2, err)
	}
	if _, err := repo.Update(ctx, s.Version, settings.ModeFullAuto, "Asia/Seoul", p); !errors.Is(err, settings.ErrVersionConflict) {
		t.Fatalf("stale version should conflict, got %v", err)
	}
}
