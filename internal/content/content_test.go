package content_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yuntyu01/x-automation/internal/content"
	"github.com/yuntyu01/x-automation/internal/testutil/pgtest"
	"github.com/yuntyu01/x-automation/internal/thread"
)

func newSvc(t *testing.T) (*content.Service, context.Context) {
	pool := pgtest.New(t)
	return content.NewService(pool, nil, nil), context.Background()
}

const twoParagraphs = "그날 밤 나는 오래된 편지를 다시 꺼내 읽었다. 글씨는 바래 있었지만 마음은 그대로였다.\n\n창밖으로 비가 내리고 있었다. 나는 답장을 쓰기로 했다."

func TestCreateEditAutosaveAndConflict(t *testing.T) {
	svc, ctx := newSvc(t)
	d, err := svc.Create(ctx, content.NewContent{RawText: twoParagraphs, Category: "소설", Tags: []string{"#비", "비", " 편지 "}})
	if err != nil {
		t.Fatal(err)
	}
	if d.Title == "" || len(d.Revision.Parts) != 2 || !d.Revision.Publishable || d.Stage != "review" {
		t.Fatalf("created = title %q parts %d publishable %v stage %s", d.Title, len(d.Revision.Parts), d.Revision.Publishable, d.Stage)
	}
	if strings.Join(d.Tags, ",") != "비,편지" {
		t.Fatalf("tags = %v", d.Tags)
	}
	// 자동 저장: 같은 revision을 고친다
	d2, err := svc.SaveDraft(ctx, d.ID, content.Draft{EditVersion: d.EditVersion, Title: "편지", Category: "소설", RawText: twoParagraphs + " 끝.", SplitMode: thread.ModeTwoParts, ImageMode: "none"})
	if err != nil || d2.Revision.ID != d.Revision.ID || d2.EditVersion != d.EditVersion+1 || d2.Title != "편지" {
		t.Fatalf("autosave = %+v %v", d2.Content, err)
	}
	// 낡은 버전으로 저장하면 충돌
	if _, err := svc.SaveDraft(ctx, d.ID, content.Draft{EditVersion: d.EditVersion, RawText: "다른 기기", SplitMode: thread.ModeTwoParts, ImageMode: "none"}); !errors.Is(err, content.ErrConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
	// 새로고침 후에도 유지
	again, _ := svc.Get(ctx, d.ID)
	if !strings.HasSuffix(again.Revision.RawText, "끝.") {
		t.Fatal("autosave not persisted")
	}
}

func TestApprovalIsInvalidatedByEdit(t *testing.T) {
	svc, ctx := newSvc(t)
	d, _ := svc.Create(ctx, content.NewContent{RawText: twoParagraphs})
	ap, err := svc.Approve(ctx, d.ID, d.Revision.ID)
	if err != nil || ap.ApprovedRevisionID == nil || *ap.ApprovedRevisionID != d.Revision.ID || ap.Stage != "ready" {
		t.Fatalf("approve = %+v %v", ap.Content, err)
	}
	// 승인된 revision을 고치면 새 revision이 생기고 승인은 이어받지 않는다
	ed, err := svc.SaveDraft(ctx, d.ID, content.Draft{EditVersion: ap.EditVersion, RawText: twoParagraphs + "\n\n덧붙인 문단.", SplitMode: thread.ModeTwoParts, ImageMode: "none"})
	if err != nil {
		t.Fatal(err)
	}
	if ed.Revision.ID == d.Revision.ID || ed.Revision.Number != 2 || ed.Revision.ReviewState != "unreviewed" {
		t.Fatalf("edit after approve = rev %s #%d %s", ed.Revision.ID, ed.Revision.Number, ed.Revision.ReviewState)
	}
	if ed.ApprovedRevisionID == nil || *ed.ApprovedRevisionID != d.Revision.ID || *ed.ApprovedNumber != 1 {
		t.Fatal("previous approval should remain on revision 1 only")
	}
	if ed.Stage != "review" {
		t.Fatalf("stage after edit = %s", ed.Stage)
	}
	// 이전 승인본은 그대로 보존된다
	old, _ := svc.GetRevision(ctx, d.ID, d.Revision.ID)
	if old.RawText != twoParagraphs || old.FrozenAt == nil {
		t.Fatal("approved revision must be immutable")
	}
	revs, _ := svc.Revisions(ctx, d.ID)
	if len(revs) != 2 {
		t.Fatalf("revisions = %d", len(revs))
	}
	// 되돌리기: 1번 내용으로 새 편집본
	rs, err := svc.Restore(ctx, d.ID, d.Revision.ID, ed.EditVersion)
	if err != nil || rs.Revision.RawText != twoParagraphs {
		t.Fatalf("restore = %v", err)
	}
}

func TestCannotApproveInvalidRevision(t *testing.T) {
	svc, ctx := newSvc(t)
	d, _ := svc.Create(ctx, content.NewContent{RawText: "짧음\n---\n" + strings.Repeat("가", 141), SplitMode: thread.ModeManual})
	if d.Revision.Publishable {
		t.Fatal("over-limit manual part must not be publishable")
	}
	var se *content.StateError
	if _, err := svc.Approve(ctx, d.ID, d.Revision.ID); !errors.As(err, &se) {
		t.Fatalf("want state error, got %v", err)
	}
	img, _ := svc.Create(ctx, content.NewContent{RawText: twoParagraphs, ImageMode: "upload"})
	if img.Revision.Publishable || len(img.Revision.Validation.Problems) == 0 {
		t.Fatal("image mode without image must not be publishable")
	}
}

func TestListFiltersSearchAndBulk(t *testing.T) {
	svc, ctx := newSvc(t)
	a, _ := svc.Create(ctx, content.NewContent{Title: "비 오는 밤", RawText: twoParagraphs, Category: "소설"})
	b, _ := svc.Create(ctx, content.NewContent{Title: "명언 하나", RawText: "늦었다고 생각할 때가 가장 빠르다", Category: "명언"})
	_, _ = svc.Create(ctx, content.NewContent{Title: "100% 진짜_썰", RawText: "썰 본문입니다. 아주 짧아요.", Category: "썰"})
	page, err := svc.List(ctx, content.Filter{Query: "편지"})
	if err != nil || page.Total != 1 || page.Items[0].ID != a.ID {
		t.Fatalf("search body = %+v %v", page.Items, err)
	}
	page, _ = svc.List(ctx, content.Filter{Query: "100%"})
	if page.Total != 1 {
		t.Fatalf("like escaping: %d", page.Total)
	}
	page, _ = svc.List(ctx, content.Filter{Category: "명언"})
	if page.Total != 1 || page.Items[0].ID != b.ID {
		t.Fatal("category filter")
	}
	res, _ := svc.Archive(ctx, []string{b.ID}, true)
	if res.Updated != 1 {
		t.Fatal("archive")
	}
	page, _ = svc.List(ctx, content.Filter{})
	if page.Total != 2 || page.Counts["archived"] != 1 || page.Counts["all"] != 2 {
		t.Fatalf("after archive total=%d counts=%v", page.Total, page.Counts)
	}
	page, _ = svc.List(ctx, content.Filter{Stage: "archived"})
	if page.Total != 1 {
		t.Fatal("archived filter")
	}
	page, _ = svc.List(ctx, content.Filter{Limit: 1, Sort: "title"})
	if len(page.Items) != 1 || page.Total != 2 {
		t.Fatal("pagination")
	}
	del, _ := svc.Delete(ctx, []string{a.ID})
	if del.Updated != 1 {
		t.Fatalf("delete never-used = %+v", del)
	}
	dup, err := svc.Duplicate(ctx, b.ID)
	if err != nil || !strings.Contains(dup.Title, "복제") {
		t.Fatalf("duplicate = %v", err)
	}
}

func TestImportPreviewCommitAndExportSafety(t *testing.T) {
	svc, ctx := newSvc(t)
	csvData := "\ufeff제목,카테고리,태그,원문\n첫 글,소설,\"단편, 비\",\"첫 문단.\n\n둘째 문단.\"\n,명언,,\n=위험,썰,,\"=SUM(A1) 같은 원문\"\n"
	tbl, err := content.ParseTable("csv", csvData)
	if err != nil {
		t.Fatal(err)
	}
	m := content.SuggestMapping(tbl.Headers)
	if m.Title != "제목" || m.Text != "원문" || m.Tags != "태그" {
		t.Fatalf("mapping = %+v", m)
	}
	pv := svc.PreviewImport(ctx, tbl, m, content.NewContent{SplitMode: thread.ModeTwoParts, ImageMode: "none"})
	if pv.Valid != 2 || pv.Invalid != 1 || pv.Rows[1].Errors["text"] == "" {
		t.Fatalf("preview = %+v", pv)
	}
	res, err := svc.CommitImport(ctx, tbl, m, content.NewContent{SplitMode: thread.ModeTwoParts, ImageMode: "none"})
	if err != nil || res.Imported != 2 || len(res.Skipped) != 1 {
		t.Fatalf("commit = %+v %v", res, err)
	}
	first, _ := svc.Get(ctx, res.IDs[0])
	if first.Revision.RawText != "첫 문단.\n\n둘째 문단." || strings.Join(first.Tags, "|") != "단편|비" || first.Source != "import" {
		t.Fatalf("imported = %q %v %s", first.Revision.RawText, first.Tags, first.Source)
	}
	jsonData := `[{"title":"J","tags":["a","b"],"parts":["본문","답글"]}]`
	jt, _ := content.ParseTable("json", jsonData)
	jr, err := svc.CommitImport(ctx, jt, content.SuggestMapping(jt.Headers), content.NewContent{SplitMode: thread.ModeTwoParts, ImageMode: "none"})
	if err != nil || jr.Imported != 1 {
		t.Fatalf("json import = %+v %v", jr, err)
	}
	jd, _ := svc.Get(ctx, jr.IDs[0])
	if len(jd.Revision.Parts) != 2 || jd.Revision.SplitMode != thread.ModeManual || strings.Join(jd.Tags, ",") != "a,b" {
		t.Fatalf("json parts = %d mode %s tags %v", len(jd.Revision.Parts), jd.Revision.SplitMode, jd.Tags)
	}

	items, err := svc.ExportItems(ctx, content.Filter{})
	if err != nil || len(items) != 3 {
		t.Fatalf("export = %d %v", len(items), err)
	}
	var buf strings.Builder
	if err := content.WriteCSV(&buf, items); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.HasPrefix(out, "\ufeff") || !strings.Contains(out, "'=위험") || !strings.Contains(out, "'=SUM(A1)") || !strings.Contains(out, "첫 문단.\n\n둘째 문단.") {
		t.Fatalf("csv export unsafe or lost newlines:\n%s", out)
	}
	// 내보낸 CSV를 다시 가져올 수 있다 (줄바꿈·한글 보존)
	rt, err := content.ParseTable("csv", out)
	if err != nil || len(rt.Rows) != 3 {
		t.Fatalf("roundtrip parse = %v", err)
	}
}

// 장문 게시 설정이 바뀌면 고정되지 않은 글만 새 한도로 다시 나눈다
func TestResplitAllFollowsPostLimit(t *testing.T) {
	pool := pgtest.New(t)
	ctx := context.Background()
	long := false
	svc := content.NewService(pool, nil, func(context.Context) bool { return long })
	text := strings.Repeat("가나다라마바사아자차카타파하 ", 60) // 한 문단, 약 1,600자(가중)
	a, err := svc.Create(ctx, content.NewContent{RawText: text, SplitMode: thread.ModeOverflow})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := svc.Create(ctx, content.NewContent{RawText: text, SplitMode: thread.ModeOverflow})
	if len(a.Revision.Parts) < 5 {
		t.Fatalf("standard limit should split into many parts: %d", len(a.Revision.Parts))
	}
	if _, err := svc.Approve(ctx, b.ID, b.CurrentRevisionID); err != nil {
		t.Fatal(err)
	}
	long = true
	n, err := svc.ResplitAll(ctx)
	if err != nil || n != 1 {
		t.Fatalf("resplit = %d %v (approved revision must stay frozen)", n, err)
	}
	a2, _ := svc.Get(ctx, a.ID)
	b2, _ := svc.Get(ctx, b.ID)
	if len(a2.Revision.Parts) != 1 || a2.Revision.Validation.Limit != thread.LongPostLimit {
		t.Fatalf("long post limit = %d parts, limit %d", len(a2.Revision.Parts), a2.Revision.Validation.Limit)
	}
	if len(b2.Revision.Parts) != len(b.Revision.Parts) {
		t.Fatal("frozen approved revision must keep its parts")
	}
}
