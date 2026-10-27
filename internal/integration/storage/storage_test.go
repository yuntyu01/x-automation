package storage

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"
)

func exercise(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()
	if err := s.Put(ctx, "media/2026/10/a.jpg", "image/jpeg", []byte("aaa")); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "thumb/2026/10/a.jpg", "image/jpeg", []byte("t")); err != nil {
		t.Fatal(err)
	}
	b, ct, err := s.Get(ctx, "media/2026/10/a.jpg")
	if err != nil || !bytes.Equal(b, []byte("aaa")) || ct != "image/jpeg" {
		t.Fatalf("get = %q %q %v", b, ct, err)
	}
	if _, _, err := s.Get(ctx, "media/none.jpg"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing = %v", err)
	}
	objs, next, err := s.List(ctx, "", 1)
	if err != nil || len(objs) != 1 || next == "" {
		t.Fatalf("list page1 = %+v %q %v", objs, next, err)
	}
	objs2, next2, err := s.List(ctx, next, 10)
	if err != nil || len(objs2) != 1 || next2 != "" || objs2[0].Key == objs[0].Key {
		t.Fatalf("list page2 = %+v %q %v", objs2, next2, err)
	}
	if err := s.Delete(ctx, "media/2026/10/a.jpg"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "media/2026/10/a.jpg"); err != nil {
		t.Fatalf("deleting missing object must succeed: %v", err)
	}
	if err := s.Put(ctx, "../escape", "x", nil); err == nil {
		t.Fatal("path traversal key must be rejected")
	}
}

func TestLocalStore(t *testing.T) {
	l, err := NewLocal(t.TempDir(), "http://localhost:8080/media")
	if err != nil {
		t.Fatal(err)
	}
	exercise(t, l)
	if u := l.PublicURL("media/a b.jpg"); u != "http://localhost:8080/media/media/a%20b.jpg" {
		t.Fatalf("public url = %s", u)
	}
}

// R2 어댑터는 S3 호환 가짜 서버로 실제 요청 형식을 검증한다 (실제 R2 연결은 운영 키가 있을 때만 확인 가능)
func TestR2AdapterAgainstFakeS3(t *testing.T) {
	backend := s3mem.New()
	faker := gofakes3.New(backend)
	srv := httptest.NewServer(faker.Server())
	defer srv.Close()
	if err := backend.CreateBucket("bucket"); err != nil {
		t.Fatal(err)
	}
	// 앱 접두사 밖의 다른 서비스 파일
	_ = backend.CreateBucket("other")
	r2, err := NewR2(R2Config{Endpoint: srv.URL, AccessKeyID: "k", SecretAccessKey: "s", Bucket: "bucket", Prefix: "x-automation/", PublicBaseURL: "https://img.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// 다른 서비스가 같은 버킷에 둔 파일 (앱 접두사 밖)
	if _, err := r2.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String("bucket"), Key: aws.String("admin/keep.png"), Body: bytes.NewReader([]byte("x"))}); err != nil {
		t.Fatal(err)
	}
	exercise(t, r2)
	objs, _, _ := r2.List(ctx, "", 100)
	for _, o := range objs {
		if strings.Contains(o.Key, "admin/") {
			t.Fatal("list must stay inside the app prefix")
		}
	}
	if u := r2.PublicURL("media/x.jpg"); u != "https://img.example.com/x-automation/media/x.jpg" {
		t.Fatalf("public url = %s", u)
	}
}
