// Package web는 Vite 빌드 산출물(dist)을 바이너리에 포함하고 SPA로 제공한다.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Handler는 정적 파일을 제공하고, 없는 경로는 index.html로 돌려 깊은 링크 새로고침을 지원한다.
// /api/ 아래 경로는 이 핸들러로 오지 않게 라우터가 먼저 처리해야 한다.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" && name != "index.html" {
			if st, err := fs.Stat(sub, name); err == nil && !st.IsDir() {
				if strings.HasPrefix(name, "assets/") {
					// 해시가 붙은 파일이라 내용이 바뀌면 이름도 바뀐다
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "no-cache")
				}
				files.ServeHTTP(w, r)
				return
			}
			// 확장자가 있는 정적 파일 요청은 SPA로 돌리지 않는다
			if path.Ext(name) != "" {
				http.NotFound(w, r)
				return
			}
		}
		serveIndex(w, r, sub)
	})
}

func serveIndex(w http.ResponseWriter, r *http.Request, sub fs.FS) {
	body, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("프론트엔드 빌드가 없습니다. `make build` 또는 `make web-build`를 먼저 실행하세요.\n"))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(body)
}
