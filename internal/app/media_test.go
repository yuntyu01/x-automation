package app_test

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

func pngBytes(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = byte(i % 251)
	}
	img.Set(0, 0, color.White)
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

func (h *harness) upload(name string, data []byte) resp {
	h.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", name)
	_, _ = fw.Write(data)
	_ = mw.WriteField("alt_text", "테스트 이미지")
	_ = mw.Close()
	req, _ := http.NewRequest("POST", h.srv.URL+"/api/media", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-CSRF-Token", h.csrf)
	c := &http.Client{Jar: h.jar}
	res, err := c.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := resp{Status: res.StatusCode, Header: res.Header, Raw: raw}
	_ = json.Unmarshal(raw, &out.Body)
	return out
}

func TestMediaUploadPublicReadAndAuthBoundary(t *testing.T) {
	h := newHarness(t)
	// 미인증 업로드 차단
	if r := h.upload("a.png", pngBytes(64, 64)); r.Status != 401 {
		t.Fatalf("anonymous upload = %d", r.Status)
	}
	h.login()
	bad := h.upload("evil.png", []byte("<script>alert(1)</script>"))
	if bad.Status != 422 {
		t.Fatalf("non-image upload = %d %s", bad.Status, bad.Raw)
	}
	up := h.upload("a.png", pngBytes(120, 80))
	if up.Status != 201 {
		t.Fatalf("upload = %d %s", up.Status, up.Raw)
	}
	id := up.Body["id"].(string)
	pub := up.Body["url"].(string)
	if !strings.Contains(pub, "/media/media/") {
		t.Fatalf("public url = %s", pub)
	}
	// 공개 조회는 로그인 없이 된다 (R2 공개 조회와 같은 성격)
	res, err := http.Get(pub)
	if err != nil || res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("public get = %v %v", res, err)
	}
	res.Body.Close()
	dir, _ := http.Get(h.srv.URL + "/media/media/")
	if dir.StatusCode != 404 {
		t.Fatalf("directory listing must not be served: %d", dir.StatusCode)
	}
	// 관리 API는 인증이 필요하다
	anon := &http.Client{}
	if r, _ := anon.Get(h.srv.URL + "/api/media"); r.StatusCode != 401 {
		t.Fatalf("anonymous list = %d", r.StatusCode)
	}
	crop := h.do("POST", "/api/media/"+id+"/crop", map[string]any{"aspect": "1:1"}, nil)
	if crop.Status != 201 || crop.Body["width"] != float64(80) {
		t.Fatalf("crop = %d %s", crop.Status, crop.Raw)
	}
	// 글에 붙이기
	c := h.do("POST", "/api/contents", map[string]any{"raw_text": "본문 하나.\n\n답글 하나.", "image_mode": "upload"}, nil)
	cid := c.Body["id"].(string)
	att := h.do("PUT", "/api/contents/"+cid+"/parts/0/media", map[string]any{"edit_version": c.Body["edit_version"], "media_ids": []string{id}, "image_mode": "upload"}, nil)
	if att.Status != 200 || att.Body["revision"].(map[string]any)["publishable"] != true {
		t.Fatalf("attach = %d %s", att.Status, att.Raw)
	}
	if del := h.do("DELETE", "/api/media/"+id, nil, nil); del.Status != 409 {
		t.Fatalf("delete in-use = %d", del.Status)
	}
	usage := h.do("GET", "/api/media/"+id+"/usage", nil, nil)
	if usage.Status != 200 || len(usage.Body["items"].([]any)) != 1 {
		t.Fatalf("usage = %s", usage.Raw)
	}
	dl := h.do("GET", "/api/media/"+id+"/download", nil, nil)
	if dl.Status != 200 || !strings.Contains(dl.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("download = %d %v", dl.Status, dl.Header)
	}
}

func TestRetentionAndOrphanEndpoints(t *testing.T) {
	h := newHarness(t)
	h.login()
	o := h.do("GET", "/api/media/orphans", nil, nil)
	if o.Status != 200 || !strings.Contains(string(o.Raw), `"items":[]`) {
		t.Fatalf("orphans = %d %s (빈 목록은 null이 아니라 [])", o.Status, o.Raw)
	}
	r := h.do("POST", "/api/media/retention/run", nil, nil)
	if r.Status != 200 || r.Body["deleted"] != float64(0) {
		t.Fatalf("retention run = %d %s", r.Status, r.Raw)
	}
	if c := h.do("POST", "/api/media/orphans/clean", nil, nil); c.Status != 200 || c.Body["deleted"] != float64(0) {
		t.Fatalf("orphan clean = %d %s", c.Status, c.Raw)
	}
	if f := h.do("GET", "/api/media?usage=failed", nil, nil); f.Status != 200 {
		t.Fatalf("failed filter = %d", f.Status)
	}
}
