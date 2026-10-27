package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/yuntyu01/x-automation/internal/integration"
)

// R2Config는 S3 호환 저장소 연결 정보다. Prefix 밖의 객체는 읽거나 지우지 않는다.
type R2Config struct {
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
	Bucket          string
	Prefix          string // "x-automation/" 처럼 /로 끝난다
	PublicBaseURL   string // 공개 조회 주소 (없으면 앱이 인증된 경로로 대신 보여 준다)
	HTTPClient      *http.Client
}

type R2 struct {
	cfg    R2Config
	client *s3.Client
}

const maxObjectBytes = 20 << 20

func NewR2(cfg R2Config) (*R2, error) {
	if cfg.Endpoint == "" || cfg.Bucket == "" || cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		return nil, integration.ErrNotConfigured("R2")
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 60 * time.Second}
	}
	client := s3.New(s3.Options{
		Region:       "auto",
		BaseEndpoint: aws.String(cfg.Endpoint),
		Credentials:  credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		UsePathStyle: true,
		HTTPClient:   hc,
		// R2는 SDK의 기본 CRC32 체크섬 헤더를 거부한 사례가 있어 필요할 때만 보낸다 (docs/INTEGRATIONS.md)
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
		RetryMaxAttempts:           3,
	})
	return &R2{cfg: cfg, client: client}, nil
}

func (r *R2) Name() string { return "r2" }

func (r *R2) full(key string) (string, error) {
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "..") {
		return "", fmt.Errorf("잘못된 객체 키: %q", key)
	}
	return r.cfg.Prefix + key, nil
}

func (r *R2) Put(ctx context.Context, key, contentType string, body []byte) error {
	k, err := r.full(key)
	if err != nil {
		return err
	}
	_, err = r.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(r.cfg.Bucket),
		Key:           aws.String(k),
		Body:          bytes.NewReader(body),
		ContentLength: aws.Int64(int64(len(body))),
		ContentType:   aws.String(contentType),
		// 키에 고유 ID가 들어가 내용이 바뀌지 않는다
		CacheControl: aws.String("public, max-age=31536000, immutable"),
	})
	return classify("put", err)
}

func (r *R2) Get(ctx context.Context, key string) ([]byte, string, error) {
	k, err := r.full(key)
	if err != nil {
		return nil, "", err
	}
	out, err := r.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(r.cfg.Bucket), Key: aws.String(k)})
	if err != nil {
		return nil, "", classify("get", err)
	}
	defer out.Body.Close()
	b, err := io.ReadAll(io.LimitReader(out.Body, maxObjectBytes+1))
	if err != nil {
		return nil, "", classify("get", err)
	}
	if len(b) > maxObjectBytes {
		return nil, "", errors.New("객체가 너무 큽니다")
	}
	return b, aws.ToString(out.ContentType), nil
}

func (r *R2) Delete(ctx context.Context, key string) error {
	k, err := r.full(key)
	if err != nil {
		return err
	}
	// 단건 삭제만 쓴다 (DeleteObjects는 체크섬이 필요하고 R2에서 문제 사례가 있다). 없는 객체 삭제도 성공으로 온다
	_, err = r.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(r.cfg.Bucket), Key: aws.String(k)})
	return classify("delete", err)
}

func (r *R2) List(ctx context.Context, after string, limit int) ([]Object, string, error) {
	in := &s3.ListObjectsV2Input{Bucket: aws.String(r.cfg.Bucket), Prefix: aws.String(r.cfg.Prefix), MaxKeys: aws.Int32(int32(limit))}
	if after != "" {
		in.StartAfter = aws.String(r.cfg.Prefix + after)
	}
	out, err := r.client.ListObjectsV2(ctx, in)
	if err != nil {
		return nil, "", classify("list", err)
	}
	var objs []Object
	next := ""
	for _, o := range out.Contents {
		k := strings.TrimPrefix(aws.ToString(o.Key), r.cfg.Prefix)
		objs = append(objs, Object{Key: k, Size: aws.ToInt64(o.Size), LastModified: aws.ToTime(o.LastModified)})
		next = k
	}
	if !aws.ToBool(out.IsTruncated) {
		next = ""
	}
	return objs, next, nil
}

func (r *R2) PublicURL(key string) string {
	if r.cfg.PublicBaseURL == "" {
		return ""
	}
	return r.cfg.PublicBaseURL + "/" + escapePath(r.cfg.Prefix+key)
}

func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

// classify는 S3 오류를 재시도 가능·설정 문제로 나눈다. 키·서명 값은 메시지에 싣지 않는다.
func classify(op string, err error) error {
	if err == nil {
		return nil
	}
	e := &integration.Error{Provider: "R2", Op: op, Class: integration.Retryable, Message: "이미지 저장소 요청이 실패했어요", Err: err}
	var re *smithyhttp.ResponseError
	if errors.As(err, &re) {
		e.HTTPStatus = re.HTTPStatusCode()
		switch {
		case e.HTTPStatus == 404:
			return ErrNotFound
		case e.HTTPStatus == 401 || e.HTTPStatus == 403:
			e.Class = integration.Config
			e.Message = "이미지 저장소 권한이 없어요 (R2 키·버킷 권한 확인)"
		case e.HTTPStatus == 400:
			e.Class = integration.Config
			e.Message = "이미지 저장소가 요청을 거부했어요 (설정 확인)"
		}
	}
	var nsk interface{ ErrorCode() string }
	if errors.As(err, &nsk) && (nsk.ErrorCode() == "NoSuchKey" || nsk.ErrorCode() == "NotFound") {
		return ErrNotFound
	}
	return e
}
