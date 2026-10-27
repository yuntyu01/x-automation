package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/yuntyu01/x-automation/internal/schedule"
)

func scheduleError(err error) error {
	var ve *schedule.ValidationError
	var we *schedule.WarningError
	switch {
	case errors.As(err, &ve):
		return FieldError(ve.Fields)
	case errors.As(err, &we):
		e := NewError(http.StatusConflict, "needs_confirm", "규칙과 다른 점이 있어요. 확인 후 진행하세요")
		e.Details = map[string]any{"warnings": we.Warnings}
		return e
	case errors.Is(err, schedule.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, schedule.ErrNotEditable), errors.Is(err, schedule.ErrConflict):
		return NewError(http.StatusConflict, "conflict", err.Error())
	}
	return err
}

// localTime은 설정 시간대의 날짜·시각을 절대 시각으로 바꾼다.
func (s *Server) localTime(r *http.Request, date, hm string) (time.Time, bool) {
	loc, _ := time.LoadLocation("Asia/Seoul")
	if st, err := s.Settings.Get(r.Context()); err == nil {
		loc = st.Location()
	}
	t, err := time.ParseInLocation("2006-01-02 15:04", date+" "+hm, loc)
	return t, err == nil
}

func (s *Server) mountSchedule(mux *http.ServeMux) {
	if s.Schedule == nil {
		return
	}
	mux.HandleFunc("GET /api/schedule", func(w http.ResponseWriter, r *http.Request) {
		// 보기 전에 예약 채우기·배치·상태를 맞춘다 (1분 주기를 기다리지 않게)
		if _, err := s.Schedule.Ensure(r.Context()); err != nil {
			WriteError(w, r, err)
			return
		}
		_, _ = s.Schedule.AutoAssign(r.Context())
		_ = s.Schedule.Refresh(r.Context())
		days, _ := strconv.Atoi(r.URL.Query().Get("days"))
		list, err := s.Schedule.List(r.Context(), r.URL.Query().Get("from"), days)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"days": list, "now": s.Clock.Now()})
	})
	mux.HandleFunc("GET /api/schedule/settings", func(w http.ResponseWriter, r *http.Request) {
		st, err := s.Schedule.Settings(r.Context())
		if err != nil {
			WriteError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, st)
	})
	saveRules := func(dry bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var in schedule.RulesInput
			if err := DecodeJSON(w, r, &in); err != nil {
				WriteError(w, r, err)
				return
			}
			rep, err := s.Schedule.SaveRules(r.Context(), in, dry)
			if err != nil {
				WriteError(w, r, scheduleError(err))
				return
			}
			WriteJSON(w, http.StatusOK, rep)
		}
	}
	mux.HandleFunc("POST /api/schedule/settings/preview", saveRules(true))
	mux.HandleFunc("PUT /api/schedule/settings", saveRules(false))

	mux.HandleFunc("POST /api/schedule/exceptions", func(w http.ResponseWriter, r *http.Request) {
		var in schedule.Exception
		if err := DecodeJSON(w, r, &in); err != nil {
			WriteError(w, r, err)
			return
		}
		n, err := s.Schedule.AddException(r.Context(), in.Date, in.Note)
		if err != nil {
			WriteError(w, r, scheduleError(err))
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"closed": n})
	})
	mux.HandleFunc("DELETE /api/schedule/exceptions/{date}", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Schedule.RemoveException(r.Context(), r.PathValue("date")); err != nil {
			WriteError(w, r, scheduleError(err))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	simple := func(fn func(w http.ResponseWriter, r *http.Request, id string) error) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if err := fn(w, r, r.PathValue("id")); err != nil {
				WriteError(w, r, scheduleError(err))
				return
			}
			WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
		}
	}
	mux.HandleFunc("POST /api/schedule/reservations/{id}/assign", simple(func(w http.ResponseWriter, r *http.Request, id string) error {
		var in struct {
			ContentID string `json:"content_id"`
		}
		if err := DecodeJSON(w, r, &in); err != nil {
			return err
		}
		return s.Schedule.Assign(r.Context(), id, in.ContentID)
	}))
	mux.HandleFunc("POST /api/schedule/reservations/{id}/unassign", simple(func(w http.ResponseWriter, r *http.Request, id string) error {
		return s.Schedule.Unassign(r.Context(), id)
	}))
	mux.HandleFunc("POST /api/schedule/reservations/{id}/skip", simple(func(w http.ResponseWriter, r *http.Request, id string) error {
		return s.Schedule.Skip(r.Context(), id)
	}))
	mux.HandleFunc("POST /api/schedule/reservations/{id}/unpin", simple(func(w http.ResponseWriter, r *http.Request, id string) error {
		return s.Schedule.Unpin(r.Context(), id)
	}))
	mux.HandleFunc("POST /api/schedule/reservations/{id}/move", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			To string `json:"to"`
		}
		if err := DecodeJSON(w, r, &in); err != nil {
			WriteError(w, r, err)
			return
		}
		if in.To != "next" && in.To != "tomorrow" {
			WriteError(w, r, FieldError(map[string]string{"to": "어디로 옮길지 고르세요"}))
			return
		}
		target, err := s.Schedule.Move(r.Context(), r.PathValue("id"), in.To)
		if err != nil {
			WriteError(w, r, scheduleError(err))
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"reservation_id": target})
	})
	type pinReq struct {
		Date      string `json:"date"`
		Time      string `json:"time"`
		ContentID string `json:"content_id"`
		Confirm   bool   `json:"confirm"`
	}
	pinInput := func(w http.ResponseWriter, r *http.Request) (schedule.PinInput, bool) {
		var in pinReq
		if err := DecodeJSON(w, r, &in); err != nil {
			WriteError(w, r, err)
			return schedule.PinInput{}, false
		}
		at, ok := s.localTime(r, in.Date, in.Time)
		if !ok {
			WriteError(w, r, FieldError(map[string]string{"at": "날짜와 시각을 확인하세요"}))
			return schedule.PinInput{}, false
		}
		return schedule.PinInput{At: at, ContentID: in.ContentID, Confirm: in.Confirm}, true
	}
	mux.HandleFunc("POST /api/schedule/reservations/{id}/pin", func(w http.ResponseWriter, r *http.Request) {
		in, ok := pinInput(w, r)
		if !ok {
			return
		}
		if err := s.Schedule.Pin(r.Context(), r.PathValue("id"), in); err != nil {
			WriteError(w, r, scheduleError(err))
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /api/schedule/pinned", func(w http.ResponseWriter, r *http.Request) {
		in, ok := pinInput(w, r)
		if !ok {
			return
		}
		id, err := s.Schedule.CreatePinned(r.Context(), in)
		if err != nil {
			WriteError(w, r, scheduleError(err))
			return
		}
		WriteJSON(w, http.StatusCreated, map[string]any{"reservation_id": id})
	})
	mux.HandleFunc("POST /api/schedule/swap", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			A string `json:"a"`
			B string `json:"b"`
		}
		if err := DecodeJSON(w, r, &in); err != nil {
			WriteError(w, r, err)
			return
		}
		if err := s.Schedule.Swap(r.Context(), in.A, in.B); err != nil {
			WriteError(w, r, scheduleError(err))
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /api/schedule/place", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ContentIDs []string `json:"content_ids"`
		}
		if err := DecodeJSON(w, r, &in); err != nil {
			WriteError(w, r, err)
			return
		}
		if len(in.ContentIDs) == 0 || len(in.ContentIDs) > 200 {
			WriteError(w, r, FieldError(map[string]string{"content_ids": "1~200개를 고르세요"}))
			return
		}
		if _, err := s.Schedule.Ensure(r.Context()); err != nil {
			WriteError(w, r, err)
			return
		}
		res, err := s.Schedule.PlaceMany(r.Context(), in.ContentIDs)
		if err != nil {
			WriteError(w, r, scheduleError(err))
			return
		}
		WriteJSON(w, http.StatusOK, res)
	})
}
