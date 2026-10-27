package prompt_test

import (
	"context"
	"errors"
	"testing"

	"github.com/yuntyu01/x-automation/internal/prompt"
	"github.com/yuntyu01/x-automation/internal/testutil/pgtest"
)

func TestPromptVersioningAndRender(t *testing.T) {
	pool := pgtest.New(t)
	ctx := context.Background()
	svc := prompt.NewService(pool)

	var ve *prompt.ValidationError
	if _, err := svc.Create(ctx, prompt.Input{Name: "x", Body: "주제: {{topic}}"}); !errors.As(err, &ve) || ve.Fields["body"] == "" {
		t.Fatalf("undeclared placeholder should fail: %v", err)
	}
	in := prompt.Input{Name: "썰 프롬프트", Category: "썰", Body: "주제 {{topic}} 말투 {{tone}} 길이 {{len}}",
		Variables: []prompt.Variable{
			{Name: "topic", Label: "주제", Required: true, Max: 20},
			{Name: "tone", Type: "select", Options: []string{"담백", "유쾌"}, Default: "담백"},
			{Name: "len", Type: "number", Default: "200", Min: 50, Max: 500},
		}}
	p, err := svc.Create(ctx, in)
	if err != nil || p.CurrentVersion != 1 || p.IsExample {
		t.Fatalf("create = %+v %v", p, err)
	}
	// 정보만 바꾸면 버전 그대로
	in.Description = "설명 추가"
	p2, _ := svc.Update(ctx, p.ID, in)
	if p2.CurrentVersion != 1 || p2.Description != "설명 추가" {
		t.Fatalf("metadata update bumped version: %d", p2.CurrentVersion)
	}
	// 본문이 바뀌면 새 버전, 이전 버전은 남는다
	in.Body = "새 본문 {{topic}} {{tone}} {{len}}"
	p3, _ := svc.Update(ctx, p.ID, in)
	if p3.CurrentVersion != 2 {
		t.Fatalf("body change version = %d", p3.CurrentVersion)
	}
	old, _ := svc.Get(ctx, p.ID, 1)
	if old.Version.Body != "주제 {{topic}} 말투 {{tone}} 길이 {{len}}" {
		t.Fatal("old version lost")
	}
	vs, _ := svc.Versions(ctx, p.ID)
	if len(vs) != 2 || vs[0].Version != 2 {
		t.Fatalf("versions = %+v", vs)
	}

	if _, errs := prompt.Render(p3.Version, map[string]string{}); errs["topic"] == "" {
		t.Fatal("required variable missing should error")
	}
	if _, errs := prompt.Render(p3.Version, map[string]string{"topic": "비", "tone": "화남"}); errs["tone"] == "" {
		t.Fatal("select outside options should error")
	}
	if _, errs := prompt.Render(p3.Version, map[string]string{"topic": "비", "len": "9999"}); errs["len"] == "" {
		t.Fatal("number over max should error")
	}
	text, errs := prompt.Render(p3.Version, map[string]string{"topic": "비"})
	if len(errs) > 0 || text != "새 본문 비 담백 200" {
		t.Fatalf("render = %q %v", text, errs)
	}

	if off, _ := svc.SetActive(ctx, p.ID, false); off.Active {
		t.Fatal("deactivate")
	}
	list, _ := svc.List(ctx, prompt.ListFilter{})
	if len(list) != 0 {
		t.Fatal("inactive prompts hidden by default")
	}
	all, _ := svc.List(ctx, prompt.ListFilter{IncludeInactive: true})
	if len(all) != 1 {
		t.Fatal("include inactive")
	}
	dup, err := svc.Duplicate(ctx, p.ID)
	if err != nil || dup.ID == p.ID || dup.Version.Body != in.Body {
		t.Fatalf("duplicate = %v", err)
	}
	ex, err := svc.Create(ctx, prompt.ExampleInput())
	if err != nil || !ex.IsExample {
		t.Fatalf("example = %v", err)
	}
}
