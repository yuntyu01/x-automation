package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/yuntyu01/x-automation/internal/content"
	"github.com/yuntyu01/x-automation/internal/prompt"
	"github.com/yuntyu01/x-automation/internal/thread"
)

// contentError는 도메인 오류를 API 오류로 바꾼다.
func contentError(err error) error {
	var ve *content.ValidationError
	var se *content.StateError
	var pve *prompt.ValidationError
	switch {
	case errors.As(err, &ve):
		return FieldError(ve.Fields)
	case errors.As(err, &pve):
		return FieldError(pve.Fields)
	case errors.As(err, &se):
		return NewError(http.StatusConflict, "invalid_state", se.Message)
	case errors.Is(err, content.ErrNotFound), errors.Is(err, prompt.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, content.ErrNotDeletable):
		return NewError(http.StatusConflict, "not_deletable", err.Error())
	}
	return err
}

func (s *Server) localDate(r *http.Request, v string, endOfDay bool) (time.Time, bool) {
	if v == "" {
		return time.Time{}, true
	}
	loc, _ := time.LoadLocation("Asia/Seoul")
	if st, err := s.Settings.Get(r.Context()); err == nil {
		loc = st.Location()
	}
	d, err := time.ParseInLocation("2006-01-02", v, loc)
	if err != nil {
		return time.Time{}, false
	}
	if endOfDay {
		d = d.AddDate(0, 0, 1)
	}
	return d, true
}

func (s *Server) contentFilter(w http.ResponseWriter, r *http.Request) (content.Filter, bool) {
	q := r.URL.Query()
	f := content.Filter{Stage: q.Get("stage"), Category: q.Get("category"), Query: q.Get("q"), Sort: q.Get("sort")}
	f.Limit, _ = strconv.Atoi(q.Get("limit"))
	f.Offset, _ = strconv.Atoi(q.Get("offset"))
	var ok1, ok2 bool
	f.From, ok1 = s.localDate(r, q.Get("from"), false)
	f.To, ok2 = s.localDate(r, q.Get("to"), true)
	if !ok1 || !ok2 {
		WriteError(w, r, FieldError(map[string]string{"from": "날짜는 YYYY-MM-DD 형식이에요"}))
		return f, false
	}
	if f.Stage != "" && f.Stage != "all" {
		valid := false
		for _, st := range content.Stages {
			valid = valid || st == f.Stage
		}
		if !valid {
			WriteError(w, r, FieldError(map[string]string{"stage": "알 수 없는 상태 필터예요"}))
			return f, false
		}
	}
	return f, true
}

// splitDefaults는 새 콘텐츠의 분할 기본값을 설정에서 가져온다.
func (s *Server) splitDefaults(r *http.Request) (thread.Mode, content.SplitOptions, string) {
	st, err := s.Settings.Get(r.Context())
	if err != nil {
		return thread.ModeTwoParts, content.SplitOptions{}, "none"
	}
	return thread.Mode(st.Policy.Thread.SplitMode), content.SplitOptions{Numbering: st.Policy.Thread.Numbering, MaxReplies: st.Policy.Thread.MaxReplies},
		st.Policy.Image.DefaultMode
}

func (s *Server) mountContents(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/contents", func(w http.ResponseWriter, r *http.Request) {
		f, ok := s.contentFilter(w, r)
		if !ok {
			return
		}
		page, err := s.Contents.List(r.Context(), f)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, page)
	})
	mux.HandleFunc("GET /api/contents/categories", func(w http.ResponseWriter, r *http.Request) {
		cats, err := s.Contents.Categories(r.Context())
		if err != nil {
			WriteError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"items": cats})
	})
	mux.HandleFunc("POST /api/contents", func(w http.ResponseWriter, r *http.Request) {
		var in content.NewContent
		if err := DecodeJSONLimit(w, r, &in, 512<<10); err != nil {
			WriteError(w, r, err)
			return
		}
		mode, so, img := s.splitDefaults(r)
		if in.SplitMode == "" {
			in.SplitMode, in.SplitOptions = mode, so
		}
		if in.ImageMode == "" {
			in.ImageMode = img
		}
		d, err := s.Contents.Create(r.Context(), in)
		if err != nil {
			WriteError(w, r, contentError(err))
			return
		}
		WriteJSON(w, http.StatusCreated, d)
	})
	mux.HandleFunc("POST /api/contents/batch", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Items []content.NewContent `json:"items"`
		}
		if err := DecodeJSONLimit(w, r, &in, 2<<20); err != nil {
			WriteError(w, r, err)
			return
		}
		if len(in.Items) == 0 || len(in.Items) > 100 {
			WriteError(w, r, FieldError(map[string]string{"items": "한 번에 1~100개까지 등록할 수 있어요"}))
			return
		}
		mode, so, img := s.splitDefaults(r)
		var ids []string
		fields := map[string]string{}
		for i, it := range in.Items {
			if it.SplitMode == "" {
				it.SplitMode, it.SplitOptions = mode, so
			}
			if it.ImageMode == "" {
				it.ImageMode = img
			}
			d, err := s.Contents.Create(r.Context(), it)
			if err != nil {
				var ve *content.ValidationError
				if errors.As(err, &ve) {
					for k, v := range ve.Fields {
						fields[fmt.Sprintf("items.%d.%s", i, k)] = v
					}
					continue
				}
				WriteError(w, r, err)
				return
			}
			ids = append(ids, d.ID)
		}
		WriteJSON(w, http.StatusCreated, map[string]any{"created": len(ids), "ids": ids, "errors": fields})
	})
	mux.HandleFunc("POST /api/contents/split-preview", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			RawText      string               `json:"raw_text"`
			SplitMode    thread.Mode          `json:"split_mode"`
			SplitOptions content.SplitOptions `json:"split_options"`
			ToManual     bool                 `json:"to_manual"`
		}
		if err := DecodeJSONLimit(w, r, &in, 512<<10); err != nil {
			WriteError(w, r, err)
			return
		}
		limit := thread.V3.MaxWeighted
		if st, err := s.Settings.Get(r.Context()); err == nil && st.Policy.Thread.LongPostEnabled {
			limit = thread.LongPostLimit
		}
		opts := thread.Options{Mode: in.SplitMode, Numbering: in.SplitOptions.Numbering, MaxReplies: in.SplitOptions.MaxReplies, Limit: limit}
		res := thread.Split(in.RawText, opts)
		out := map[string]any{"result": res}
		if in.ToManual {
			out["manual_text"] = thread.ToManual(in.RawText, opts)
		}
		WriteJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("GET /api/contents/{id}", func(w http.ResponseWriter, r *http.Request) {
		d, err := s.Contents.Get(r.Context(), r.PathValue("id"))
		if err != nil {
			WriteError(w, r, contentError(err))
			return
		}
		WriteJSON(w, http.StatusOK, d)
	})
	mux.HandleFunc("PUT /api/contents/{id}", func(w http.ResponseWriter, r *http.Request) {
		var in content.Draft
		if err := DecodeJSONLimit(w, r, &in, 512<<10); err != nil {
			WriteError(w, r, err)
			return
		}
		d, err := s.Contents.SaveDraft(r.Context(), r.PathValue("id"), in)
		if errors.Is(err, content.ErrConflict) {
			// 충돌하면 서버의 현재 상태를 함께 돌려줘 화면에서 비교·선택하게 한다
			cur, gerr := s.Contents.Get(r.Context(), r.PathValue("id"))
			e := *ErrConflict
			e.RequestID = RequestID(r.Context())
			body := map[string]any{"error": e}
			if gerr == nil {
				body["current"] = cur
			}
			WriteJSON(w, http.StatusConflict, body)
			return
		}
		if err != nil {
			WriteError(w, r, contentError(err))
			return
		}
		WriteJSON(w, http.StatusOK, d)
	})
	revAction := func(fn func(r *http.Request, id, rev string) (content.Detail, error)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var in struct {
				RevisionID string `json:"revision_id"`
			}
			if err := DecodeJSON(w, r, &in); err != nil {
				WriteError(w, r, err)
				return
			}
			d, err := fn(r, r.PathValue("id"), in.RevisionID)
			if err != nil {
				WriteError(w, r, contentError(err))
				return
			}
			WriteJSON(w, http.StatusOK, d)
		}
	}
	mux.HandleFunc("POST /api/contents/{id}/approve", revAction(func(r *http.Request, id, rev string) (content.Detail, error) {
		return s.Contents.Approve(r.Context(), id, rev)
	}))
	mux.HandleFunc("POST /api/contents/{id}/reject", revAction(func(r *http.Request, id, rev string) (content.Detail, error) {
		return s.Contents.Reject(r.Context(), id, rev)
	}))
	mux.HandleFunc("GET /api/contents/{id}/revisions", func(w http.ResponseWriter, r *http.Request) {
		list, err := s.Contents.Revisions(r.Context(), r.PathValue("id"))
		if err != nil {
			WriteError(w, r, contentError(err))
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"items": list})
	})
	mux.HandleFunc("GET /api/contents/{id}/revisions/{rev}", func(w http.ResponseWriter, r *http.Request) {
		rev, err := s.Contents.GetRevision(r.Context(), r.PathValue("id"), r.PathValue("rev"))
		if err != nil {
			WriteError(w, r, contentError(err))
			return
		}
		WriteJSON(w, http.StatusOK, rev)
	})
	mux.HandleFunc("POST /api/contents/{id}/revisions/{rev}/restore", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			EditVersion int `json:"edit_version"`
		}
		if err := DecodeJSON(w, r, &in); err != nil {
			WriteError(w, r, err)
			return
		}
		d, err := s.Contents.Restore(r.Context(), r.PathValue("id"), r.PathValue("rev"), in.EditVersion)
		if errors.Is(err, content.ErrConflict) {
			WriteError(w, r, ErrConflict)
			return
		}
		if err != nil {
			WriteError(w, r, contentError(err))
			return
		}
		WriteJSON(w, http.StatusOK, d)
	})
	mux.HandleFunc("POST /api/contents/{id}/duplicate", func(w http.ResponseWriter, r *http.Request) {
		d, err := s.Contents.Duplicate(r.Context(), r.PathValue("id"))
		if err != nil {
			WriteError(w, r, contentError(err))
			return
		}
		WriteJSON(w, http.StatusCreated, d)
	})
	mux.HandleFunc("POST /api/contents/bulk", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Action   string   `json:"action"`
			IDs      []string `json:"ids"`
			Category string   `json:"category"`
		}
		if err := DecodeJSON(w, r, &in); err != nil {
			WriteError(w, r, err)
			return
		}
		if len(in.IDs) == 0 || len(in.IDs) > 200 {
			WriteError(w, r, FieldError(map[string]string{"ids": "1~200개를 선택하세요"}))
			return
		}
		var res content.BulkResult
		var err error
		switch in.Action {
		case "archive":
			res, err = s.Contents.Archive(r.Context(), in.IDs, true)
		case "unarchive":
			res, err = s.Contents.Archive(r.Context(), in.IDs, false)
		case "category":
			res, err = s.Contents.SetCategory(r.Context(), in.IDs, in.Category)
		case "delete":
			res, err = s.Contents.Delete(r.Context(), in.IDs)
		default:
			WriteError(w, r, FieldError(map[string]string{"action": "알 수 없는 작업이에요"}))
			return
		}
		if err != nil {
			WriteError(w, r, contentError(err))
			return
		}
		WriteJSON(w, http.StatusOK, res)
	})

	// 가져오기·내보내기
	type importReq struct {
		Format   string          `json:"format"`
		Data     string          `json:"data"`
		Mapping  content.Mapping `json:"mapping"`
		Defaults struct {
			SplitMode thread.Mode `json:"split_mode"`
			Category  string      `json:"category"`
		} `json:"defaults"`
	}
	parseImport := func(w http.ResponseWriter, r *http.Request) (importReq, content.Table, bool) {
		var in importReq
		if err := DecodeJSONLimit(w, r, &in, content.MaxImportBytes+(512<<10)); err != nil {
			WriteError(w, r, err)
			return in, content.Table{}, false
		}
		t, err := content.ParseTable(in.Format, in.Data)
		if err != nil {
			WriteError(w, r, contentError(err))
			return in, t, false
		}
		return in, t, true
	}
	defaultsFor := func(r *http.Request, in importReq) content.NewContent {
		mode, so, _ := s.splitDefaults(r)
		if in.Defaults.SplitMode.Valid() {
			mode = in.Defaults.SplitMode
		}
		return content.NewContent{SplitMode: mode, SplitOptions: so, ImageMode: "none", Category: in.Defaults.Category}
	}
	mux.HandleFunc("POST /api/contents/import/parse", func(w http.ResponseWriter, r *http.Request) {
		_, t, ok := parseImport(w, r)
		if !ok {
			return
		}
		sample := t.Rows
		if len(sample) > 20 {
			sample = sample[:20]
		}
		WriteJSON(w, http.StatusOK, map[string]any{"headers": t.Headers, "sample": sample, "row_count": len(t.Rows), "mapping": content.SuggestMapping(t.Headers)})
	})
	mux.HandleFunc("POST /api/contents/import/preview", func(w http.ResponseWriter, r *http.Request) {
		in, t, ok := parseImport(w, r)
		if !ok {
			return
		}
		WriteJSON(w, http.StatusOK, s.Contents.PreviewImport(r.Context(), t, in.Mapping, defaultsFor(r, in)))
	})
	mux.HandleFunc("POST /api/contents/import/commit", func(w http.ResponseWriter, r *http.Request) {
		in, t, ok := parseImport(w, r)
		if !ok {
			return
		}
		res, err := s.Contents.CommitImport(r.Context(), t, in.Mapping, defaultsFor(r, in))
		if err != nil {
			WriteError(w, r, contentError(err))
			return
		}
		WriteJSON(w, http.StatusOK, res)
	})
	mux.HandleFunc("GET /api/contents/import/template", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Query().Get("format") == "json" {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Content-Disposition", `attachment; filename="contents-template.json"`)
			_, _ = w.Write(content.TemplateJSON())
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="contents-template.csv"`)
		_, _ = w.Write(content.TemplateCSV())
	})
	mux.HandleFunc("GET /api/contents/export", func(w http.ResponseWriter, r *http.Request) {
		f, ok := s.contentFilter(w, r)
		if !ok {
			return
		}
		items, err := s.Contents.ExportItems(r.Context(), f)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		stamp := time.Now().Format("20060102-1504")
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Query().Get("format") == "json" {
			w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="contents-%s.json"`, stamp))
			WriteJSON(w, http.StatusOK, map[string]any{"exported_at": time.Now().UTC(), "items": items})
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="contents-%s.csv"`, stamp))
		if err := content.WriteCSV(w, items); err != nil {
			WriteError(w, r, err)
		}
	})
}

func (s *Server) mountPrompts(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/prompts", func(w http.ResponseWriter, r *http.Request) {
		list, err := s.Prompts.List(r.Context(), prompt.ListFilter{IncludeInactive: r.URL.Query().Get("all") == "1", Category: r.URL.Query().Get("category")})
		if err != nil {
			WriteError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"items": list})
	})
	mux.HandleFunc("POST /api/prompts", func(w http.ResponseWriter, r *http.Request) {
		var in prompt.Input
		if err := DecodeJSONLimit(w, r, &in, 256<<10); err != nil {
			WriteError(w, r, err)
			return
		}
		in.IsExample = false // 예시 표시는 예시 추가 버튼으로만 붙는다
		p, err := s.Prompts.Create(r.Context(), in)
		if err != nil {
			WriteError(w, r, contentError(err))
			return
		}
		WriteJSON(w, http.StatusCreated, p)
	})
	mux.HandleFunc("POST /api/prompts/example", func(w http.ResponseWriter, r *http.Request) {
		p, err := s.Prompts.Create(r.Context(), prompt.ExampleInput())
		if err != nil {
			WriteError(w, r, contentError(err))
			return
		}
		WriteJSON(w, http.StatusCreated, p)
	})
	mux.HandleFunc("GET /api/prompts/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, _ := strconv.Atoi(r.URL.Query().Get("version"))
		p, err := s.Prompts.Get(r.Context(), r.PathValue("id"), v)
		if err != nil {
			WriteError(w, r, contentError(err))
			return
		}
		WriteJSON(w, http.StatusOK, p)
	})
	mux.HandleFunc("PUT /api/prompts/{id}", func(w http.ResponseWriter, r *http.Request) {
		var in prompt.Input
		if err := DecodeJSONLimit(w, r, &in, 256<<10); err != nil {
			WriteError(w, r, err)
			return
		}
		p, err := s.Prompts.Update(r.Context(), r.PathValue("id"), in)
		if err != nil {
			WriteError(w, r, contentError(err))
			return
		}
		WriteJSON(w, http.StatusOK, p)
	})
	mux.HandleFunc("POST /api/prompts/{id}/duplicate", func(w http.ResponseWriter, r *http.Request) {
		p, err := s.Prompts.Duplicate(r.Context(), r.PathValue("id"))
		if err != nil {
			WriteError(w, r, contentError(err))
			return
		}
		WriteJSON(w, http.StatusCreated, p)
	})
	mux.HandleFunc("POST /api/prompts/{id}/active", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Active bool `json:"active"`
		}
		if err := DecodeJSON(w, r, &in); err != nil {
			WriteError(w, r, err)
			return
		}
		p, err := s.Prompts.SetActive(r.Context(), r.PathValue("id"), in.Active)
		if err != nil {
			WriteError(w, r, contentError(err))
			return
		}
		WriteJSON(w, http.StatusOK, p)
	})
	mux.HandleFunc("GET /api/prompts/{id}/versions", func(w http.ResponseWriter, r *http.Request) {
		list, err := s.Prompts.Versions(r.Context(), r.PathValue("id"))
		if err != nil {
			WriteError(w, r, contentError(err))
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"items": list})
	})
	mux.HandleFunc("POST /api/prompts/{id}/render", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Version int               `json:"version"`
			Values  map[string]string `json:"values"`
		}
		if err := DecodeJSON(w, r, &in); err != nil {
			WriteError(w, r, err)
			return
		}
		p, err := s.Prompts.Get(r.Context(), r.PathValue("id"), in.Version)
		if err != nil {
			WriteError(w, r, contentError(err))
			return
		}
		text, errs := prompt.Render(p.Version, in.Values)
		if len(errs) > 0 {
			WriteError(w, r, FieldError(errs))
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"text": text})
	})
}
