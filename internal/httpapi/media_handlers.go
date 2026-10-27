package httpapi

import (
	"errors"
	"fmt"
	"image"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/yuntyu01/x-automation/internal/content"
	"github.com/yuntyu01/x-automation/internal/integration"
	"github.com/yuntyu01/x-automation/internal/integration/storage"
	"github.com/yuntyu01/x-automation/internal/media"
)

func mediaError(err error) error {
	var ie *media.ImageError
	var ge *integration.Error
	switch {
	case errors.As(err, &ie):
		return FieldError(map[string]string{"file": ie.Message})
	case errors.Is(err, media.ErrNotFound), errors.Is(err, storage.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, media.ErrInUse):
		return NewError(http.StatusConflict, "in_use", err.Error())
	case errors.As(err, &ge):
		status := http.StatusBadGateway
		if ge.Class == integration.Config {
			status = http.StatusServiceUnavailable
		}
		return NewError(status, "storage_"+string(ge.Class), ge.Message)
	}
	return contentError(err)
}

func (s *Server) mountMedia(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/media", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, media.MaxUploadBytes+(1<<20))
		if err := r.ParseMultipartForm(media.MaxUploadBytes + (1 << 20)); err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				WriteError(w, r, FieldError(map[string]string{"file": "파일이 15MB를 넘어요"}))
				return
			}
			WriteError(w, r, FieldError(map[string]string{"file": "파일을 받지 못했어요"}))
			return
		}
		defer r.MultipartForm.RemoveAll()
		f, _, err := r.FormFile("file")
		if err != nil {
			WriteError(w, r, FieldError(map[string]string{"file": "파일을 선택하세요"}))
			return
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, media.MaxUploadBytes+1))
		if err != nil {
			WriteError(w, r, err)
			return
		}
		a, err := s.Media.Save(r.Context(), media.SaveInput{Data: data, Source: "upload", AltText: r.FormValue("alt_text")})
		if err != nil {
			WriteError(w, r, mediaError(err))
			return
		}
		WriteJSON(w, http.StatusCreated, a)
	})
	mux.HandleFunc("GET /api/media", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		offset, _ := strconv.Atoi(q.Get("offset"))
		page, err := s.Media.List(r.Context(), media.ListFilter{Usage: q.Get("usage"), Query: q.Get("q"), Limit: limit, Offset: offset})
		if err != nil {
			WriteError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, page)
	})
	mux.HandleFunc("GET /api/media/orphans", func(w http.ResponseWriter, r *http.Request) {
		list, err := s.Media.FindOrphans(r.Context(), 24*time.Hour, 200)
		if err != nil {
			WriteError(w, r, mediaError(err))
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"items": list})
	})
	mux.HandleFunc("GET /api/media/{id}", func(w http.ResponseWriter, r *http.Request) {
		a, err := s.Media.Get(r.Context(), r.PathValue("id"))
		if err != nil {
			WriteError(w, r, mediaError(err))
			return
		}
		WriteJSON(w, http.StatusOK, a)
	})
	mux.HandleFunc("GET /api/media/{id}/usage", func(w http.ResponseWriter, r *http.Request) {
		list, err := s.Contents.MediaUsage(r.Context(), r.PathValue("id"))
		if err != nil {
			WriteError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"items": list})
	})
	mux.HandleFunc("PATCH /api/media/{id}", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			AltText string `json:"alt_text"`
		}
		if err := DecodeJSON(w, r, &in); err != nil {
			WriteError(w, r, err)
			return
		}
		a, err := s.Media.UpdateAlt(r.Context(), r.PathValue("id"), in.AltText)
		if err != nil {
			WriteError(w, r, mediaError(err))
			return
		}
		WriteJSON(w, http.StatusOK, a)
	})
	mux.HandleFunc("POST /api/media/{id}/crop", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			X, Y, W, H int
			Aspect     string `json:"aspect"` // "1:1", "16:9", "4:5" — 가운데 기준
		}
		if err := DecodeJSON(w, r, &in); err != nil {
			WriteError(w, r, err)
			return
		}
		src, err := s.Media.Get(r.Context(), r.PathValue("id"))
		if err != nil {
			WriteError(w, r, mediaError(err))
			return
		}
		rect := image.Rect(in.X, in.Y, in.X+in.W, in.Y+in.H)
		if in.Aspect != "" {
			var aw, ah int
			if _, err := fmt.Sscanf(in.Aspect, "%d:%d", &aw, &ah); err != nil || aw <= 0 || ah <= 0 {
				WriteError(w, r, FieldError(map[string]string{"aspect": "비율은 1:1 같은 형식이에요"}))
				return
			}
			rect = centerCrop(src.Width, src.Height, aw, ah)
		}
		a, err := s.Media.Crop(r.Context(), src.ID, rect)
		if err != nil {
			WriteError(w, r, mediaError(err))
			return
		}
		WriteJSON(w, http.StatusCreated, a)
	})
	mux.HandleFunc("DELETE /api/media/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Media.Delete(r.Context(), r.PathValue("id")); err != nil {
			WriteError(w, r, mediaError(err))
			return
		}
		WriteJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	serveFile := func(download bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			data, ct, err := s.Media.File(r.Context(), r.PathValue("id"), !download && r.URL.Query().Get("thumb") == "1")
			if err != nil {
				WriteError(w, r, mediaError(err))
				return
			}
			w.Header().Set("Content-Type", ct)
			w.Header().Set("Cache-Control", "private, max-age=3600")
			if download {
				ext := "jpg"
				switch ct {
				case "image/png":
					ext = "png"
				case "image/webp":
					ext = "webp"
				case "image/gif":
					ext = "gif"
				}
				w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="image-%s.%s"`, r.PathValue("id")[:8], ext))
			}
			_, _ = w.Write(data)
		}
	}
	mux.HandleFunc("GET /api/media/{id}/file", serveFile(false))
	mux.HandleFunc("GET /api/media/{id}/download", serveFile(true))

	mux.HandleFunc("PUT /api/contents/{id}/parts/{index}/media", func(w http.ResponseWriter, r *http.Request) {
		idx, err := strconv.Atoi(r.PathValue("index"))
		if err != nil {
			WriteError(w, r, ErrNotFound)
			return
		}
		var in struct {
			EditVersion int      `json:"edit_version"`
			MediaIDs    []string `json:"media_ids"`
			ImageMode   string   `json:"image_mode"`
		}
		if err := DecodeJSON(w, r, &in); err != nil {
			WriteError(w, r, err)
			return
		}
		d, err := s.Contents.SetPartMedia(r.Context(), r.PathValue("id"), in.EditVersion, idx, in.MediaIDs, in.ImageMode)
		if errors.Is(err, content.ErrConflict) {
			WriteError(w, r, ErrConflict)
			return
		}
		if err != nil {
			WriteError(w, r, mediaError(err))
			return
		}
		WriteJSON(w, http.StatusOK, d)
	})
}

// centerCrop은 가운데를 기준으로 비율에 맞는 가장 큰 영역을 고른다.
func centerCrop(w, h, aw, ah int) image.Rectangle {
	cw, ch := w, w*ah/aw
	if ch > h {
		ch = h
		cw = h * aw / ah
	}
	x, y := (w-cw)/2, (h-ch)/2
	return image.Rect(x, y, x+cw, y+ch)
}
