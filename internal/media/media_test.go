package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/yuntyu01/x-automation/internal/content"
	"github.com/yuntyu01/x-automation/internal/integration/storage"
	"github.com/yuntyu01/x-automation/internal/testutil/pgtest"
	"github.com/yuntyu01/x-automation/internal/thread"
)

func solid(w, h int, c color.Color) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

func jpegBytes(img image.Image) []byte {
	var b bytes.Buffer
	_ = jpeg.Encode(&b, img, &jpeg.Options{Quality: 90})
	return b.Bytes()
}

// withExif는 방향 값과 가짜 GPS 문자열이 든 APP1 세그먼트를 SOI 뒤에 넣는다.
func withExif(data []byte, orientation uint16) []byte {
	var tiff bytes.Buffer
	tiff.WriteString("MM")
	_ = binary.Write(&tiff, binary.BigEndian, uint16(42))
	_ = binary.Write(&tiff, binary.BigEndian, uint32(8))
	_ = binary.Write(&tiff, binary.BigEndian, uint16(1))
	_ = binary.Write(&tiff, binary.BigEndian, uint16(0x0112))
	_ = binary.Write(&tiff, binary.BigEndian, uint16(3))
	_ = binary.Write(&tiff, binary.BigEndian, uint32(1))
	_ = binary.Write(&tiff, binary.BigEndian, orientation)
	_ = binary.Write(&tiff, binary.BigEndian, uint16(0))
	_ = binary.Write(&tiff, binary.BigEndian, uint32(0))
	tiff.WriteString("GPS-SECRET-37.5665N")
	payload := append([]byte("Exif\x00\x00"), tiff.Bytes()...)
	seg := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
	seg = append(seg, payload...)
	out := append([]byte{}, data[:2]...)
	out = append(out, seg...)
	return append(out, data[2:]...)
}

func TestProcessRejectsBadInput(t *testing.T) {
	cases := map[string][]byte{
		"text":  []byte("hello, not an image"),
		"tiny":  jpegBytes(solid(8, 8, color.White)),
		"empty": {},
	}
	for name, data := range cases {
		var ie *ImageError
		if _, err := Process(data); !errors.As(err, &ie) {
			t.Errorf("%s: want ImageError, got %v", name, err)
		}
	}
	// 움직이는 GIF
	pal := []color.Color{color.Black, color.White}
	g := &gif.GIF{Image: []*image.Paletted{image.NewPaletted(image.Rect(0, 0, 32, 32), pal), image.NewPaletted(image.Rect(0, 0, 32, 32), pal)}, Delay: []int{10, 10}}
	var gb bytes.Buffer
	_ = gif.EncodeAll(&gb, g)
	if _, err := Process(gb.Bytes()); err == nil {
		t.Error("animated gif should be rejected")
	}
}

func TestProcessStripsExifAndAppliesOrientation(t *testing.T) {
	src := withExif(jpegBytes(solid(200, 100, color.RGBA{200, 10, 10, 255})), 6)
	if jpegOrientation(src) != 6 {
		t.Fatal("orientation not parsed")
	}
	p, err := Process(src)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(p.Data, []byte("GPS-SECRET")) {
		t.Fatal("exif metadata leaked")
	}
	if p.Width != 100 || p.Height != 200 {
		t.Fatalf("rotation not applied: %dx%d", p.Width, p.Height)
	}
	// 방향이 1이면 다시 압축하지 않고 메타데이터만 지운다
	plain := withExif(jpegBytes(solid(120, 80, color.White)), 1)
	q, _ := Process(plain)
	if bytes.Contains(q.Data, []byte("GPS-SECRET")) || q.Width != 120 || len(q.Thumb) == 0 {
		t.Fatal("plain jpeg not cleaned")
	}
}

func TestProcessDownscalesAndKeepsPNG(t *testing.T) {
	big := solid(5000, 1000, color.RGBA{0, 0, 0, 0})
	var b bytes.Buffer
	_ = png.Encode(&b, big)
	p, err := Process(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if p.Width != MaxSide || p.ContentType != "image/png" || len(p.Data) > MaxPostBytes {
		t.Fatalf("got %dx%d %s %d bytes", p.Width, p.Height, p.ContentType, len(p.Data))
	}
}

func TestServiceSaveDedupeCropDeleteAndAttach(t *testing.T) {
	pool := pgtest.New(t)
	ctx := context.Background()
	st, _ := storage.NewLocal(t.TempDir(), "http://localhost/media")
	svc := NewService(pool, st, nil, nil)
	data := jpegBytes(solid(300, 200, color.RGBA{30, 120, 200, 255}))
	a, err := svc.Save(ctx, SaveInput{Data: data, Source: "upload", AltText: "파란 하늘"})
	if err != nil || a.State != "active" || a.URL == "" || a.ThumbURL == "" {
		t.Fatalf("save = %+v %v", a, err)
	}
	again, _ := svc.Save(ctx, SaveInput{Data: data, Source: "upload"})
	if again.ID != a.ID {
		t.Fatal("same content should be reused")
	}
	c, err := svc.Crop(ctx, a.ID, image.Rect(0, 0, 100, 100))
	if err != nil || c.Width != 100 || c.ParentID == nil || *c.ParentID != a.ID {
		t.Fatalf("crop = %+v %v", c, err)
	}
	if b, _, err := svc.File(ctx, a.ID, true); err != nil || len(b) == 0 {
		t.Fatal("thumbnail missing")
	}

	cs := content.NewService(pool, nil, nil)
	cs.SetMediaLoader(svc.ContentLoader())
	d, _ := cs.Create(ctx, content.NewContent{RawText: "첫 문단.\n\n둘째 문단.", ImageMode: "upload", SplitMode: thread.ModeTwoParts})
	if d.Revision.Publishable {
		t.Fatal("upload mode without image must not be publishable")
	}
	d, err = cs.SetPartMedia(ctx, d.ID, d.EditVersion, 1, []string{a.ID, c.ID}, "upload")
	if err != nil || !d.Revision.Publishable || len(d.Revision.Media) != 2 || d.Revision.Media[0].URL == "" {
		t.Fatalf("attach = %+v %v", d.Revision.Media, err)
	}
	if !errors.Is(svc.Delete(ctx, a.ID), ErrInUse) {
		t.Fatal("in-use image must not be deletable")
	}
	// 승인 후 이미지를 바꾸면 새 revision (승인 미승계)
	ap, _ := cs.Approve(ctx, d.ID, d.Revision.ID)
	d2, err := cs.SetPartMedia(ctx, d.ID, ap.EditVersion, 1, []string{c.ID}, "")
	if err != nil || d2.Revision.ID == ap.Revision.ID || d2.Revision.ReviewState != "unreviewed" {
		t.Fatalf("image change after approve should fork revision: %v", err)
	}
	// 5장은 거부
	if _, err := cs.SetPartMedia(ctx, d.ID, d2.EditVersion, 0, []string{a.ID, c.ID, "00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000002", "00000000-0000-0000-0000-000000000003"}, ""); err == nil {
		t.Fatal("more than 4 images per part must be rejected")
	}
	// 쓰지 않는 이미지는 삭제되고 메타데이터는 남는다
	extra, _ := svc.Save(ctx, SaveInput{Data: jpegBytes(solid(64, 64, color.Black)), Source: "upload"})
	if err := svc.Delete(ctx, extra.ID); err != nil {
		t.Fatal(err)
	}
	gone, _ := svc.Get(ctx, extra.ID)
	if gone.State != "deleted" || gone.DeletedAt == nil {
		t.Fatalf("deleted state = %+v", gone)
	}
	if _, _, err := svc.File(ctx, extra.ID, false); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("deleted file should not be served")
	}
	page, _ := svc.List(ctx, ListFilter{Usage: "unused"})
	for _, it := range page.Items {
		if it.ID == a.ID {
			t.Fatal("used image listed as unused")
		}
	}
	orphans, err := svc.FindOrphans(ctx, 0, 10)
	if err != nil || len(orphans) != 0 {
		t.Fatalf("no orphans expected: %+v %v", orphans, err)
	}
	_ = st.Put(ctx, "media/2020/01/stray.jpg", "image/jpeg", []byte("x"))
	orphans, _ = svc.FindOrphans(ctx, 0, 10)
	if len(orphans) != 1 || orphans[0].Key != "media/2020/01/stray.jpg" {
		t.Fatalf("stray object should be an orphan: %+v", orphans)
	}
}
