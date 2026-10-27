package content

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/yuntyu01/x-automation/internal/store"
	"github.com/yuntyu01/x-automation/internal/thread"
)

const (
	MaxImportBytes = 2 << 20
	MaxImportRows  = 500
)

// Table은 가져오기 파일을 표로 푼 결과다.
type Table struct {
	Headers []string   `json:"headers"`
	Rows    [][]string `json:"rows"`
}

// Mapping은 앱 필드 → 파일 열 이름이다. Parts에 여러 열을 순서대로 지정하면 직접 나누기로 등록한다.
type Mapping struct {
	Title     string   `json:"title"`
	Category  string   `json:"category"`
	Tags      string   `json:"tags"`
	Text      string   `json:"text"`
	SplitMode string   `json:"split_mode"`
	Parts     []string `json:"parts"`
}

type RowResult struct {
	Row         int               `json:"row"` // 1부터 (헤더 제외)
	OK          bool              `json:"ok"`
	Errors      map[string]string `json:"errors"`
	Title       string            `json:"title"`
	Category    string            `json:"category"`
	PartCount   int               `json:"part_count"`
	Note        string            `json:"note"`
	Publishable bool              `json:"publishable"`
	Problems    []string          `json:"problems"`
}

type ImportPreview struct {
	Rows    []RowResult `json:"rows"`
	Valid   int         `json:"valid"`
	Invalid int         `json:"invalid"`
}

type ImportResult struct {
	Imported int         `json:"imported"`
	Skipped  []RowResult `json:"skipped"`
	IDs      []string    `json:"ids"`
}

// ParseTable은 CSV(첫 줄 헤더) 또는 JSON 배열을 표로 푼다.
func ParseTable(format string, data string) (Table, error) {
	if len(data) > MaxImportBytes {
		return Table{}, &ValidationError{Fields: map[string]string{"file": "파일이 2MB를 넘어요. 나눠서 가져오세요"}}
	}
	if !utf8.ValidString(data) {
		return Table{}, &ValidationError{Fields: map[string]string{"file": "UTF-8 인코딩 파일만 가져올 수 있어요 (엑셀은 'CSV UTF-8'로 저장)"}}
	}
	data = strings.TrimPrefix(data, "\ufeff")
	switch format {
	case "csv":
		r := csv.NewReader(strings.NewReader(data))
		r.FieldsPerRecord = -1
		r.LazyQuotes = false
		recs, err := r.ReadAll()
		if err != nil {
			return Table{}, &ValidationError{Fields: map[string]string{"file": "CSV를 읽지 못했어요: " + err.Error()}}
		}
		if len(recs) == 0 {
			return Table{}, &ValidationError{Fields: map[string]string{"file": "빈 파일이에요"}}
		}
		t := Table{Headers: recs[0], Rows: recs[1:]}
		for i, h := range t.Headers {
			t.Headers[i] = strings.TrimSpace(h)
		}
		if len(t.Rows) > MaxImportRows {
			return Table{}, &ValidationError{Fields: map[string]string{"file": fmt.Sprintf("한 번에 %d행까지 가져올 수 있어요", MaxImportRows)}}
		}
		return t, nil
	case "json":
		var items []map[string]any
		if err := json.Unmarshal([]byte(data), &items); err != nil {
			return Table{}, &ValidationError{Fields: map[string]string{"file": "JSON 배열이 아니에요: " + err.Error()}}
		}
		if len(items) > MaxImportRows {
			return Table{}, &ValidationError{Fields: map[string]string{"file": fmt.Sprintf("한 번에 %d개까지 가져올 수 있어요", MaxImportRows)}}
		}
		keys := map[string]bool{}
		for _, it := range items {
			for k := range it {
				keys[k] = true
			}
		}
		t := Table{}
		for k := range keys {
			t.Headers = append(t.Headers, k)
		}
		sort.Slice(t.Headers, func(i, j int) bool {
			return headerRank(t.Headers[i]) < headerRank(t.Headers[j]) || (headerRank(t.Headers[i]) == headerRank(t.Headers[j]) && t.Headers[i] < t.Headers[j])
		})
		for _, it := range items {
			row := make([]string, len(t.Headers))
			for i, h := range t.Headers {
				row[i] = jsonCell(it[h])
			}
			t.Rows = append(t.Rows, row)
		}
		return t, nil
	}
	return Table{}, &ValidationError{Fields: map[string]string{"format": "csv 또는 json만 가져올 수 있어요"}}
}

func headerRank(h string) int {
	switch h {
	case "title":
		return 0
	case "category":
		return 1
	case "tags":
		return 2
	case "text":
		return 3
	case "split_mode":
		return 4
	case "parts":
		return 5
	}
	return 10
}

func jsonCell(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []any:
		var parts []string
		for _, e := range x {
			parts = append(parts, jsonCell(e))
		}
		// 배열은 부분 목록(직접 나누기) 또는 태그로 쓰인다
		return strings.Join(parts, thread.ManualSeparator)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

// SuggestMapping은 열 이름으로 매핑을 추측한다 (한글 열 이름 포함).
func SuggestMapping(headers []string) Mapping {
	m := Mapping{}
	for _, h := range headers {
		k := strings.ToLower(strings.TrimSpace(h))
		switch k {
		case "title", "제목":
			m.Title = h
		case "category", "카테고리", "분류":
			m.Category = h
		case "tags", "태그":
			m.Tags = h
		case "text", "body", "원문", "본문", "내용":
			m.Text = h
		case "split_mode", "분할":
			m.SplitMode = h
		case "parts":
			m.Parts = []string{h}
		}
		if strings.HasPrefix(k, "part_") || strings.HasPrefix(k, "reply") || strings.HasPrefix(k, "답글") {
			m.Parts = append(m.Parts, h)
		}
	}
	return m
}

type importRow struct {
	n   NewContent
	res RowResult
}

func (s *Service) buildRows(ctx context.Context, t Table, m Mapping, defaults NewContent) []importRow {
	idx := map[string]int{}
	for i, h := range t.Headers {
		idx[h] = i
	}
	cell := func(row []string, col string) string {
		if col == "" {
			return ""
		}
		i, ok := idx[col]
		if !ok || i >= len(row) {
			return ""
		}
		return row[i]
	}
	var out []importRow
	for i, row := range t.Rows {
		blank := true
		for _, c := range row {
			if strings.TrimSpace(c) != "" {
				blank = false
			}
		}
		if blank {
			continue
		}
		res := RowResult{Row: i + 1, Errors: map[string]string{}, Problems: []string{}}
		n := defaults
		n.Title = strings.TrimSpace(cell(row, m.Title))
		n.Category = strings.TrimSpace(cell(row, m.Category))
		n.Tags = splitTags(cell(row, m.Tags))
		n.Source = "import"
		n.Origin = "import"
		var parts []string
		for _, col := range m.Parts {
			v := cell(row, col)
			if strings.TrimSpace(v) == "" {
				continue
			}
			parts = append(parts, v)
		}
		if len(parts) > 0 {
			n.RawText = thread.JoinManual(parts)
			n.SplitMode = thread.ModeManual
		} else {
			n.RawText = cell(row, m.Text)
			if sm := strings.TrimSpace(cell(row, m.SplitMode)); sm != "" {
				n.SplitMode = thread.Mode(sm)
			}
		}
		if f := validateFields(n.Title, n.Category, n.Tags, n.RawText, n.SplitMode, n.ImageMode, n.SplitOptions); len(f) > 0 {
			res.Errors = f
		}
		if strings.TrimSpace(n.RawText) == "" {
			res.Errors["text"] = "원문(또는 부분 열)이 비어 있어요"
		}
		if n.Title == "" && n.RawText != "" {
			n.Title = autoTitle(n.RawText)
		}
		res.Title, res.Category = n.Title, n.Category
		if len(res.Errors) == 0 {
			r, v := s.evaluate(ctx, n.RawText, n.SplitMode, n.SplitOptions, n.ImageMode, nil)
			res.PartCount, res.Note, res.Publishable, res.Problems = len(r.Parts), v.Note, r.Publishable, v.Problems
		}
		res.OK = len(res.Errors) == 0
		out = append(out, importRow{n: n, res: res})
	}
	return out
}

func splitTags(s string) []string {
	f := func(r rune) bool { return r == ',' || r == ';' || r == '\n' || r == '|' }
	var out []string
	for _, t := range strings.FieldsFunc(s, f) {
		if strings.Trim(strings.TrimSpace(t), "-") == "" {
			continue // JSON 배열을 합칠 때 쓴 구분선
		}
		out = append(out, t)
	}
	return cleanTags(out)
}

// PreviewImport는 등록 전에 행별 오류와 분할 결과를 보여 준다.
func (s *Service) PreviewImport(ctx context.Context, t Table, m Mapping, defaults NewContent) ImportPreview {
	p := ImportPreview{Rows: []RowResult{}}
	for _, r := range s.buildRows(ctx, t, m, defaults) {
		p.Rows = append(p.Rows, r.res)
		if r.res.OK {
			p.Valid++
		} else {
			p.Invalid++
		}
	}
	return p
}

// CommitImport는 오류 없는 행만 한 트랜잭션으로 등록한다. 오류 행은 건너뛰고 이유를 돌려준다.
func (s *Service) CommitImport(ctx context.Context, t Table, m Mapping, defaults NewContent) (ImportResult, error) {
	rows := s.buildRows(ctx, t, m, defaults)
	res := ImportResult{Skipped: []RowResult{}, IDs: []string{}}
	err := store.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		for _, r := range rows {
			if !r.res.OK {
				res.Skipped = append(res.Skipped, r.res)
				continue
			}
			id, err := s.CreateTx(ctx, tx, r.n)
			if err != nil {
				return err
			}
			res.IDs = append(res.IDs, id)
			res.Imported++
		}
		return nil
	})
	return res, err
}

// ── 내보내기 ──────────────────────────────────────────────────────────

type ExportItem struct {
	ID          string       `json:"id"`
	Title       string       `json:"title"`
	Category    string       `json:"category"`
	Tags        []string     `json:"tags"`
	Stage       string       `json:"stage"`
	SplitMode   string       `json:"split_mode"`
	Text        string       `json:"text"`
	Parts       []string     `json:"parts"`
	Posts       []ExportPost `json:"posts"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
	PublishedAt *time.Time   `json:"published_at"`
}

type ExportPost struct {
	Position int    `json:"position"`
	PostID   string `json:"post_id"`
	URL      string `json:"url"`
}

// ExportItems는 필터에 맞는 콘텐츠를 모은다. 자격증명·토큰은 포함하지 않는다.
func (s *Service) ExportItems(ctx context.Context, f Filter) ([]ExportItem, error) {
	f.Limit, f.Offset = 100, 0
	var out []ExportItem
	for {
		page, err := s.List(ctx, f)
		if err != nil {
			return nil, err
		}
		for _, it := range page.Items {
			d, err := s.Get(ctx, it.ID)
			if err != nil {
				return nil, err
			}
			e := ExportItem{ID: d.ID, Title: d.Title, Category: d.Category, Tags: d.Tags, Stage: d.Stage, SplitMode: string(d.Revision.SplitMode),
				Text: d.Revision.RawText, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt, PublishedAt: it.LastPublishedAt, Parts: []string{}, Posts: []ExportPost{}}
			for _, p := range d.Revision.Parts {
				e.Parts = append(e.Parts, p.Text)
			}
			rows, err := s.pool.Query(ctx, `SELECT pp.position, pp.x_post_id, COALESCE(pp.x_url, '') FROM publish_parts pp
				JOIN publish_runs pr ON pr.id = pp.run_id WHERE pr.content_id = $1 AND pp.x_post_id IS NOT NULL ORDER BY pr.created_at, pp.position`, d.ID)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var p ExportPost
				if err := rows.Scan(&p.Position, &p.PostID, &p.URL); err != nil {
					rows.Close()
					return nil, err
				}
				e.Posts = append(e.Posts, p)
			}
			rows.Close()
			out = append(out, e)
		}
		f.Offset += len(page.Items)
		if len(page.Items) == 0 || f.Offset >= page.Total {
			break
		}
	}
	return out, nil
}

// csvSafe는 스프레드시트가 수식으로 실행하지 않게 위험한 첫 글자 앞에 작은따옴표를 붙인다.
func csvSafe(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}

// WriteCSV는 UTF-8 BOM(엑셀 한글)과 함께 쓴다. 줄바꿈은 따옴표 안에 그대로 보존된다.
func WriteCSV(w io.Writer, items []ExportItem) error {
	var buf bytes.Buffer
	buf.WriteString("\ufeff")
	cw := csv.NewWriter(&buf)
	_ = cw.Write([]string{"id", "title", "category", "tags", "stage", "split_mode", "text", "parts", "x_urls", "created_at", "published_at"})
	for _, e := range items {
		var urls []string
		for _, p := range e.Posts {
			urls = append(urls, p.URL)
		}
		pub := ""
		if e.PublishedAt != nil {
			pub = e.PublishedAt.Format(time.RFC3339)
		}
		_ = cw.Write([]string{e.ID, csvSafe(e.Title), csvSafe(e.Category), csvSafe(strings.Join(e.Tags, ", ")), e.Stage, e.SplitMode,
			csvSafe(e.Text), csvSafe(strings.Join(e.Parts, thread.ManualSeparator)), strings.Join(urls, " "), e.CreatedAt.Format(time.RFC3339), pub})
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
}

// Templates는 가져오기 양식이다. 예시 행에는 예시임을 적는다.
func TemplateCSV() []byte {
	var buf bytes.Buffer
	buf.WriteString("\ufeff")
	cw := csv.NewWriter(&buf)
	_ = cw.Write([]string{"title", "category", "tags", "text", "split_mode"})
	_ = cw.Write([]string{"(예시) 비 오는 밤", "소설", "단편, 비", "첫 문단입니다.\n\n둘째 문단은 답글로 이어집니다.", "two_parts"})
	_ = cw.Write([]string{"(예시) 직접 나눈 글", "썰", "", "본문에 올릴 부분\n---\n답글로 올릴 부분", "manual"})
	cw.Flush()
	return buf.Bytes()
}

func TemplateJSON() []byte {
	v := []map[string]any{
		{"title": "(예시) 비 오는 밤", "category": "소설", "tags": []string{"단편", "비"}, "text": "첫 문단입니다.\n\n둘째 문단은 답글로 이어집니다.", "split_mode": "two_parts"},
		{"title": "(예시) 직접 나눈 글", "category": "썰", "parts": []string{"본문에 올릴 부분", "답글로 올릴 부분"}},
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	return b
}
