package storage

import (
	"context"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Local은 목업·개발용 파일 저장소다. 공개 조회는 앱의 /media/ 경로가 맡는다 (R2 공개 조회와 같은 성격).
type Local struct {
	dir       string
	publicURL string // 예: http://localhost:8080/media
}

func NewLocal(dir, publicURL string) (*Local, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	return &Local{dir: dir, publicURL: strings.TrimRight(publicURL, "/")}, nil
}

func (l *Local) Name() string { return "local" }

func (l *Local) path(key string) (string, error) {
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "..") || strings.Contains(key, "\\") {
		return "", errors.New("잘못된 객체 키")
	}
	return filepath.Join(l.dir, filepath.FromSlash(key)), nil
}

func (l *Local) Put(_ context.Context, key, _ string, body []byte) error {
	p, err := l.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, body, 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func (l *Local) Get(_ context.Context, key string) ([]byte, string, error) {
	p, err := l.path(key)
	if err != nil {
		return nil, "", err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", ErrNotFound
	}
	return b, contentTypeFor(key), err
}

func (l *Local) Delete(_ context.Context, key string) error {
	p, err := l.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (l *Local) List(_ context.Context, after string, limit int) ([]Object, string, error) {
	var all []Object
	err := filepath.WalkDir(l.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(p, ".tmp") {
			return err
		}
		rel, _ := filepath.Rel(l.dir, p)
		info, _ := d.Info()
		all = append(all, Object{Key: filepath.ToSlash(rel), Size: info.Size(), LastModified: info.ModTime()})
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Key < all[j].Key })
	var out []Object
	for _, o := range all {
		if o.Key > after {
			out = append(out, o)
		}
		if len(out) == limit+1 {
			break
		}
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		next = out[len(out)-1].Key
	}
	return out, next, nil
}

func (l *Local) PublicURL(key string) string {
	if l.publicURL == "" {
		return ""
	}
	return l.publicURL + "/" + escapePath(key)
}

// Path는 /media/ 경로 핸들러가 파일을 찾을 때 쓴다.
func (l *Local) Path(key string) (string, error) {
	k, err := url.PathUnescape(key)
	if err != nil {
		return "", err
	}
	return l.path(k)
}

func contentTypeFor(key string) string {
	switch strings.ToLower(filepath.Ext(key)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	}
	return "application/octet-stream"
}
