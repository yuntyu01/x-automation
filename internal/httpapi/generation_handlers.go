package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/yuntyu01/x-automation/internal/content"
	"github.com/yuntyu01/x-automation/internal/generation"
)

func generationError(err error) error {
	var ve *generation.ValidationError
	switch {
	case errors.As(err, &ve):
		return FieldError(ve.Fields)
	case errors.Is(err, generation.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, generation.ErrResultGone):
		return NewError(http.StatusConflict, "result_gone", err.Error())
	case errors.Is(err, content.ErrConflict):
		return ErrConflict
	}
	return contentError(err)
}

func (s *Server) mountGeneration(mux *http.ServeMux) {
	if s.Generation == nil {
		return
	}
	mux.HandleFunc("GET /api/generation", func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		list, err := s.Generation.List(r.Context(), r.URL.Query().Get("active") == "1", limit)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"items": list, "provider": s.Generation.ProviderName()})
	})
	// 버튼 생성: 1·5·20·직접 수량. 기본은 저장까지이고 빈 예약 배치는 따로 고른다 (지금 게시와 분리)
	mux.HandleFunc("POST /api/generation", func(w http.ResponseWriter, r *http.Request) {
		var in generation.SubmitInput
		if err := DecodeJSON(w, r, &in); err != nil {
			WriteError(w, r, err)
			return
		}
		in.Trigger = "manual"
		req, created, err := s.Generation.Submit(r.Context(), in)
		if err != nil {
			WriteError(w, r, generationError(err))
			return
		}
		status := http.StatusCreated
		if !created {
			status = http.StatusOK
		}
		WriteJSON(w, status, req)
	})
	mux.HandleFunc("POST /api/generation/estimate", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Quantity int                `json:"quantity"`
			Options  generation.Options `json:"options"`
		}
		if err := DecodeJSON(w, r, &in); err != nil {
			WriteError(w, r, err)
			return
		}
		m, known, cur, err := s.Generation.Estimate(r.Context(), in.Quantity, in.Options)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"micros": m, "pricing_known": known, "currency": cur})
	})
	mux.HandleFunc("GET /api/generation/plan", func(w http.ResponseWriter, r *http.Request) {
		pl, err := s.Planner.Preview(r.Context())
		if err != nil {
			WriteError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, pl)
	})
	mux.HandleFunc("POST /api/generation/fill", func(w http.ResponseWriter, r *http.Request) {
		req, c, err := s.Planner.FillNow(r.Context(), "button")
		if err != nil {
			WriteError(w, r, generationError(err))
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"request": req, "shortfall": c.Shortfall})
	})
	mux.HandleFunc("GET /api/generation/{id}", func(w http.ResponseWriter, r *http.Request) {
		req, err := s.Generation.Get(r.Context(), r.PathValue("id"))
		if err != nil {
			WriteError(w, r, generationError(err))
			return
		}
		WriteJSON(w, http.StatusOK, req)
	})
	mux.HandleFunc("POST /api/generation/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		req, err := s.Generation.Cancel(r.Context(), r.PathValue("id"))
		if err != nil {
			WriteError(w, r, generationError(err))
			return
		}
		WriteJSON(w, http.StatusOK, req)
	})

	// 부분 재생성·이미지만 생성: 결과는 바로 덮어쓰지 않고 비교 후 적용한다
	mux.HandleFunc("POST /api/contents/{id}/regenerate", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			IdempotencyKey string `json:"idempotency_key"`
			Target         string `json:"target"` // all | part | image
			PartIndex      int    `json:"part_index"`
			Instruction    string `json:"instruction"`
			ImagePrompt    string `json:"image_prompt"`
		}
		if err := DecodeJSON(w, r, &in); err != nil {
			WriteError(w, r, err)
			return
		}
		id := r.PathValue("id")
		if _, err := s.Contents.Get(r.Context(), id); err != nil {
			WriteError(w, r, contentError(err))
			return
		}
		switch in.Target {
		case "all", "part", "image":
		default:
			WriteError(w, r, FieldError(map[string]string{"target": "무엇을 다시 만들지 고르세요"}))
			return
		}
		opts := generation.Options{Text: in.Target != "image", Image: in.Target == "image", Target: in.Target, PartIndex: in.PartIndex,
			Instruction: in.Instruction, ImagePrompt: in.ImagePrompt, ImageCount: 1}
		req, created, err := s.Generation.Submit(r.Context(), generation.SubmitInput{IdempotencyKey: in.IdempotencyKey, Trigger: "regenerate",
			ContentID: &id, Quantity: 1, Options: opts})
		if err != nil {
			WriteError(w, r, generationError(err))
			return
		}
		status := http.StatusCreated
		if !created {
			status = http.StatusOK
		}
		WriteJSON(w, status, req)
	})
	mux.HandleFunc("GET /api/contents/{id}/generation-results", func(w http.ResponseWriter, r *http.Request) {
		list, err := s.Generation.Results(r.Context(), r.PathValue("id"))
		if err != nil {
			WriteError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"items": list})
	})
	mux.HandleFunc("POST /api/contents/{id}/generation-results/{rid}/apply", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			EditVersion int `json:"edit_version"`
		}
		if err := DecodeJSON(w, r, &in); err != nil {
			WriteError(w, r, err)
			return
		}
		d, err := s.Generation.ApplyResult(r.Context(), r.PathValue("id"), r.PathValue("rid"), in.EditVersion)
		if err != nil {
			WriteError(w, r, generationError(err))
			return
		}
		WriteJSON(w, http.StatusOK, d)
	})
	mux.HandleFunc("POST /api/contents/{id}/generation-results/{rid}/discard", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Generation.DiscardResult(r.Context(), r.PathValue("id"), r.PathValue("rid")); err != nil {
			WriteError(w, r, generationError(err))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	// 사용량·예산 (이번 달 추정)
	mux.HandleFunc("GET /api/usage", func(w http.ResponseWriter, r *http.Request) {
		sum, err := s.Budget.Summary(r.Context())
		if err != nil {
			WriteError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, sum)
	})

	// 자동 운영 시작·중지와 생성·게시 일시정지. 첫 시작 때 바로 채우기를 켜 두었으면 부족분을 채운다
	mux.HandleFunc("POST /api/automation", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			AutomationEnabled *bool `json:"automation_enabled"`
			GenerationPaused  *bool `json:"generation_paused"`
			PublishingPaused  *bool `json:"publishing_paused"`
		}
		if err := DecodeJSON(w, r, &in); err != nil {
			WriteError(w, r, err)
			return
		}
		before, err := s.Settings.Get(r.Context())
		if err != nil {
			WriteError(w, r, err)
			return
		}
		st, err := s.Settings.SetFlags(r.Context(), in.AutomationEnabled, in.GenerationPaused, in.PublishingPaused)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		out := map[string]any{"settings": st}
		if !before.AutomationEnabled && st.AutomationEnabled && st.Policy.Generation.FillOnStart && st.Policy.Generation.Timing != "manual" {
			if req, c, err := s.Planner.FillNow(r.Context(), "start"); err != nil {
				var ve *generation.ValidationError
				if errors.As(err, &ve) {
					out["fill_note"] = ve.Fields["generation"]
				}
			} else {
				out["fill_request"], out["fill_shortfall"] = req, c.Shortfall
			}
		}
		WriteJSON(w, http.StatusOK, out)
	})
}
