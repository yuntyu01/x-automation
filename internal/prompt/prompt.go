// Package prompt는 사용자가 붙여넣은 프롬프트와 버전 이력을 관리한다.
//
//   - 본문 안의 {{변수}}는 구조화된 입력으로 받고 유효성을 검사한다
//   - 본문·변수·기본값이 바뀌면 새 버전을 만든다. 이미 만든 콘텐츠는 다시 생성하지 않는다
//   - 예시 프롬프트는 is_example로 표시한다. 사용자의 실제 프롬프트를 지어내지 않는다
package prompt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yuntyu01/x-automation/internal/store"
)

var ErrNotFound = errors.New("프롬프트를 찾을 수 없습니다")

type ValidationError struct{ Fields map[string]string }

func (e *ValidationError) Error() string { return "입력값을 확인해 주세요" }

// Variable은 프롬프트 변수 하나의 입력 규칙이다.
type Variable struct {
	Name     string   `json:"name"`  // 본문의 {{name}}
	Label    string   `json:"label"` // 화면 레이블
	Type     string   `json:"type"`  // text | textarea | select | number
	Options  []string `json:"options,omitempty"`
	Required bool     `json:"required"`
	Default  string   `json:"default"`
	Max      int      `json:"max,omitempty"` // 글자 수(text) 또는 최댓값(number)
	Min      int      `json:"min,omitempty"`
	Help     string   `json:"help,omitempty"`
}

// Defaults는 생성 옵션 기본값이다.
type Defaults struct {
	Model       string `json:"model"`
	Length      string `json:"length"` // short | medium | long
	Tone        string `json:"tone"`
	ImageMode   string `json:"image_mode"` // none | ai | upload
	ImagePrompt string `json:"image_prompt"`
	SplitMode   string `json:"split_mode"`
}

type Prompt struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Category       string    `json:"category"`
	Description    string    `json:"description"`
	IsExample      bool      `json:"is_example"`
	Active         bool      `json:"active"`
	CurrentVersion int       `json:"current_version"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	Version        Version   `json:"version"`
	UsageCount     int       `json:"usage_count"`
}

type Version struct {
	Version   int        `json:"version"`
	Body      string     `json:"body"`
	Variables []Variable `json:"variables"`
	Defaults  Defaults   `json:"defaults"`
	Note      string     `json:"note"`
	CreatedAt time.Time  `json:"created_at"`
}

type Input struct {
	Name        string     `json:"name"`
	Category    string     `json:"category"`
	Description string     `json:"description"`
	Body        string     `json:"body"`
	Variables   []Variable `json:"variables"`
	Defaults    Defaults   `json:"defaults"`
	Note        string     `json:"note"`
	IsExample   bool       `json:"is_example"`
}

var placeholder = regexp.MustCompile(`\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}`)
var varName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,39}$`)

// Placeholders는 본문에 쓰인 변수 이름을 순서대로 돌려준다.
func Placeholders(body string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range placeholder.FindAllStringSubmatch(body, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}

func (in *Input) validate() map[string]string {
	f := map[string]string{}
	in.Name = strings.TrimSpace(in.Name)
	in.Category = strings.TrimSpace(in.Category)
	if in.Name == "" {
		f["name"] = "이름을 입력하세요"
	} else if len([]rune(in.Name)) > 60 {
		f["name"] = "이름은 60자 이하로 써 주세요"
	}
	if len([]rune(in.Category)) > 40 {
		f["category"] = "카테고리는 40자 이하로 써 주세요"
	}
	if len([]rune(in.Description)) > 300 {
		f["description"] = "설명은 300자 이하로 써 주세요"
	}
	if strings.TrimSpace(in.Body) == "" {
		f["body"] = "프롬프트 내용을 붙여넣어 주세요"
	} else if len([]rune(in.Body)) > 20000 {
		f["body"] = "프롬프트는 20,000자 이하로 써 주세요"
	}
	declared := map[string]bool{}
	for i, v := range in.Variables {
		key := fmt.Sprintf("variables.%d", i)
		switch {
		case !varName.MatchString(v.Name):
			f[key+".name"] = "변수 이름은 영문·숫자·밑줄만 쓸 수 있어요 (예: topic)"
		case declared[v.Name]:
			f[key+".name"] = "같은 이름의 변수가 있어요"
		}
		declared[v.Name] = true
		if strings.TrimSpace(v.Label) == "" {
			in.Variables[i].Label = v.Name
		}
		switch v.Type {
		case "", "text", "textarea", "number":
			if v.Type == "" {
				in.Variables[i].Type = "text"
			}
		case "select":
			if len(v.Options) == 0 {
				f[key+".options"] = "선택지를 하나 이상 넣어 주세요"
			}
		default:
			f[key+".type"] = "변수 종류가 올바르지 않습니다"
		}
		if v.Default != "" {
			if msg := checkValue(in.Variables[i], v.Default); msg != "" {
				f[key+".default"] = "기본값: " + msg
			}
		}
	}
	if len(in.Variables) > 20 {
		f["variables"] = "변수는 20개까지예요"
	}
	for _, name := range Placeholders(in.Body) {
		if !declared[name] {
			f["body"] = fmt.Sprintf("본문의 {{%s}}에 해당하는 변수를 추가해 주세요", name)
			break
		}
	}
	return f
}

func checkValue(v Variable, val string) string {
	switch v.Type {
	case "number":
		n, err := strconv.Atoi(strings.TrimSpace(val))
		if err != nil {
			return "숫자를 입력하세요"
		}
		if v.Min != 0 && n < v.Min {
			return fmt.Sprintf("%d 이상이어야 해요", v.Min)
		}
		if v.Max != 0 && n > v.Max {
			return fmt.Sprintf("%d 이하여야 해요", v.Max)
		}
	case "select":
		for _, o := range v.Options {
			if o == val {
				return ""
			}
		}
		return "선택지 중에서 골라 주세요"
	default:
		if v.Max > 0 && len([]rune(val)) > v.Max {
			return fmt.Sprintf("%d자 이하로 써 주세요", v.Max)
		}
	}
	return ""
}

// Render는 변수를 검사하고 본문에 넣는다. 결과는 AI 공급자에게 보낼 사용자 프롬프트다.
func Render(v Version, values map[string]string) (string, map[string]string) {
	errs := map[string]string{}
	resolved := map[string]string{}
	for _, d := range v.Variables {
		val, ok := values[d.Name]
		if !ok || strings.TrimSpace(val) == "" {
			val = d.Default
		}
		if strings.TrimSpace(val) == "" {
			if d.Required {
				errs[d.Name] = d.Label + "을(를) 입력하세요"
			}
			resolved[d.Name] = ""
			continue
		}
		if msg := checkValue(d, val); msg != "" {
			errs[d.Name] = msg
			continue
		}
		resolved[d.Name] = val
	}
	if len(errs) > 0 {
		return "", errs
	}
	out := placeholder.ReplaceAllStringFunc(v.Body, func(m string) string {
		name := placeholder.FindStringSubmatch(m)[1]
		return resolved[name]
	})
	return out, nil
}

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func (s *Service) Create(ctx context.Context, in Input) (Prompt, error) {
	if f := in.validate(); len(f) > 0 {
		return Prompt{}, &ValidationError{Fields: f}
	}
	var id string
	err := store.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO prompts (name, category, description, is_example) VALUES ($1, $2, $3, $4) RETURNING id`,
			in.Name, in.Category, in.Description, in.IsExample).Scan(&id); err != nil {
			return err
		}
		return insertVersion(ctx, tx, id, 1, in)
	})
	if err != nil {
		return Prompt{}, err
	}
	return s.Get(ctx, id, 0)
}

func insertVersion(ctx context.Context, tx pgx.Tx, id string, version int, in Input) error {
	vars, _ := json.Marshal(nonNil(in.Variables))
	defs, _ := json.Marshal(in.Defaults)
	_, err := tx.Exec(ctx, `INSERT INTO prompt_versions (prompt_id, version, body, variables, defaults, note) VALUES ($1, $2, $3, $4, $5, $6)`,
		id, version, in.Body, vars, defs, strings.TrimSpace(in.Note))
	return err
}

func nonNil(v []Variable) []Variable {
	if v == nil {
		return []Variable{}
	}
	return v
}

// Update는 이름·설명 같은 정보만 바뀌면 그대로 두고, 본문·변수·기본값이 바뀌면 새 버전을 만든다.
func (s *Service) Update(ctx context.Context, id string, in Input) (Prompt, error) {
	if f := in.validate(); len(f) > 0 {
		return Prompt{}, &ValidationError{Fields: f}
	}
	cur, err := s.Get(ctx, id, 0)
	if err != nil {
		return Prompt{}, err
	}
	changed := cur.Version.Body != in.Body || !sameJSON(cur.Version.Variables, nonNil(in.Variables)) || cur.Version.Defaults != in.Defaults
	err = store.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE prompts SET name = $2, category = $3, description = $4, updated_at = now() WHERE id = $1`,
			id, in.Name, in.Category, in.Description); err != nil {
			return err
		}
		if !changed {
			return nil
		}
		next := cur.CurrentVersion + 1
		if err := insertVersion(ctx, tx, id, next, in); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE prompts SET current_version = $2 WHERE id = $1`, id, next)
		return err
	})
	if err != nil {
		return Prompt{}, err
	}
	return s.Get(ctx, id, 0)
}

func sameJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func (s *Service) SetActive(ctx context.Context, id string, active bool) (Prompt, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE prompts SET active = $2, updated_at = now() WHERE id = $1`, id, active)
	if err != nil {
		return Prompt{}, err
	}
	if tag.RowsAffected() == 0 {
		return Prompt{}, ErrNotFound
	}
	return s.Get(ctx, id, 0)
}

// Duplicate는 현재 버전을 복사해 새 프롬프트를 만든다.
func (s *Service) Duplicate(ctx context.Context, id string) (Prompt, error) {
	cur, err := s.Get(ctx, id, 0)
	if err != nil {
		return Prompt{}, err
	}
	return s.Create(ctx, Input{Name: cur.Name + " (복제)", Category: cur.Category, Description: cur.Description, Body: cur.Version.Body,
		Variables: cur.Version.Variables, Defaults: cur.Version.Defaults, IsExample: cur.IsExample})
}

// Get은 프롬프트와 버전(0이면 현재 버전)을 돌려준다.
func (s *Service) Get(ctx context.Context, id string, version int) (Prompt, error) {
	var p Prompt
	err := s.pool.QueryRow(ctx, `SELECT id, name, category, description, is_example, active, current_version, created_at, updated_at,
		(SELECT count(*) FROM content_revisions WHERE prompt_id = prompts.id)
		FROM prompts WHERE id = $1`, id).
		Scan(&p.ID, &p.Name, &p.Category, &p.Description, &p.IsExample, &p.Active, &p.CurrentVersion, &p.CreatedAt, &p.UpdatedAt, &p.UsageCount)
	if store.IsNoRows(err) {
		return p, ErrNotFound
	}
	if err != nil {
		return p, err
	}
	if version == 0 {
		version = p.CurrentVersion
	}
	p.Version, err = s.version(ctx, id, version)
	return p, err
}

func (s *Service) version(ctx context.Context, id string, version int) (Version, error) {
	var v Version
	var vars, defs []byte
	err := s.pool.QueryRow(ctx, `SELECT version, body, variables, defaults, note, created_at FROM prompt_versions WHERE prompt_id = $1 AND version = $2`,
		id, version).Scan(&v.Version, &v.Body, &vars, &defs, &v.Note, &v.CreatedAt)
	if store.IsNoRows(err) {
		return v, ErrNotFound
	}
	if err != nil {
		return v, err
	}
	_ = json.Unmarshal(vars, &v.Variables)
	_ = json.Unmarshal(defs, &v.Defaults)
	v.Variables = nonNil(v.Variables)
	return v, nil
}

// Versions는 변경 이력이다 (최신 먼저).
func (s *Service) Versions(ctx context.Context, id string) ([]Version, error) {
	rows, err := s.pool.Query(ctx, `SELECT version FROM prompt_versions WHERE prompt_id = $1 ORDER BY version DESC`, id)
	if err != nil {
		return nil, err
	}
	var nums []int
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return nil, err
		}
		nums = append(nums, n)
	}
	rows.Close()
	out := []Version{}
	for _, n := range nums {
		v, err := s.version(ctx, id, n)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

type ListFilter struct {
	IncludeInactive bool
	Category        string
}

func (s *Service) List(ctx context.Context, f ListFilter) ([]Prompt, error) {
	rows, err := s.pool.Query(ctx, `SELECT id FROM prompts WHERE ($1 OR active) AND ($2 = '' OR category = $2)
		ORDER BY active DESC, is_example ASC, updated_at DESC`, f.IncludeInactive, f.Category)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	out := []Prompt{}
	for _, id := range ids {
		p, err := s.Get(ctx, id, 0)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Active && !out[j].Active })
	return out, nil
}

// ExampleInput은 "예시 프롬프트 추가" 버튼이 만드는 예시다. 실제 사용자 프롬프트가 아님을 이름과 플래그로 표시한다.
func ExampleInput() Input {
	return Input{
		Name:        "(예시) 짧은 단편 소설",
		Category:    "소설",
		Description: "예시 프롬프트입니다. 쓰던 프롬프트로 바꿔 쓰세요.",
		IsExample:   true,
		Body: "다음 조건으로 X(트위터)에 올릴 짧은 이야기를 써 줘.\n" +
			"- 주제: {{topic}}\n- 분위기: {{tone}}\n- 길이: 한국어 기준 {{length}}자 안팎\n" +
			"- 앞부분에서 궁금증을 만들고 뒷부분에서 반전이나 여운을 남겨 줘.\n- 해시태그와 링크는 넣지 마.",
		Variables: []Variable{
			{Name: "topic", Label: "주제", Type: "text", Required: true, Max: 100, Help: "예: 오래된 편지, 마지막 버스"},
			{Name: "tone", Label: "분위기", Type: "select", Options: []string{"잔잔한", "긴장감 있는", "유쾌한", "쓸쓸한"}, Default: "잔잔한"},
			{Name: "length", Label: "길이(자)", Type: "number", Default: "250", Min: 80, Max: 1200},
		},
		Defaults: Defaults{Length: "medium", ImageMode: "none", SplitMode: "two_parts"},
	}
}
