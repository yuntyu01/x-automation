package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"testing"
)

const secret = "GPS-SECRET-37.5665N"

// pngChunk는 CRC까지 맞춘 PNG 청크다.
func pngChunk(name string, payload []byte) []byte {
	b := make([]byte, 8, 12+len(payload))
	binary.BigEndian.PutUint32(b, uint32(len(payload)))
	copy(b[4:], name)
	b = append(b, payload...)
	return binary.BigEndian.AppendUint32(b, crc32.ChecksumIEEE(b[4:]))
}

// 작은 PNG(다시 인코딩할 필요 없음)라도 텍스트·EXIF 청크의 위치 정보는 지운다
func TestProcessStripsPNGTextAndExif(t *testing.T) {
	var b bytes.Buffer
	_ = png.Encode(&b, solid(64, 48, color.RGBA{10, 20, 30, 255}))
	data := b.Bytes()
	ihdrEnd := 8 + 12 + 13
	var withMeta []byte
	withMeta = append(withMeta, data[:ihdrEnd]...)
	withMeta = append(withMeta, pngChunk("tEXt", []byte("Comment\x00"+secret))...)
	withMeta = append(withMeta, pngChunk("eXIf", []byte("MM\x00*"+secret))...)
	withMeta = append(withMeta, data[ihdrEnd:]...)
	out, err := Process(withMeta)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out.Data, []byte(secret)) || out.ContentType != "image/png" {
		t.Fatalf("metadata kept (type %s)", out.ContentType)
	}
	if _, err := png.Decode(bytes.NewReader(out.Data)); err != nil {
		t.Fatalf("stripped png is broken: %v", err)
	}
}

// 마커 앞 채움 바이트처럼 예상 밖 구조가 있어도 원본을 그대로 두지 않는다
func TestProcessJPEGWithFillBytesIsStripped(t *testing.T) {
	src := withExif(jpegBytes(solid(64, 48, color.RGBA{200, 10, 10, 255})), 1)
	// SOI 바로 뒤에 채움 바이트 하나
	odd := append([]byte{0xFF, 0xD8, 0xFF}, src[2:]...)
	out, err := Process(odd)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out.Data, []byte(secret)) {
		t.Fatal("exif kept after fill byte")
	}
}

// 메타데이터가 있는 WebP는 원본 대신 다시 인코딩한 이미지를 남긴다
func TestWebPMetadataDetection(t *testing.T) {
	riff := func(chunks ...[]byte) []byte {
		body := []byte("WEBP")
		for _, c := range chunks {
			body = append(body, c...)
		}
		out := []byte("RIFF")
		out = binary.LittleEndian.AppendUint32(out, uint32(len(body)))
		return append(out, body...)
	}
	chunk := func(name string, payload []byte) []byte {
		c := []byte(name)
		c = binary.LittleEndian.AppendUint32(c, uint32(len(payload)))
		c = append(c, payload...)
		if len(payload)%2 == 1 {
			c = append(c, 0)
		}
		return c
	}
	if webpHasMetadata(riff(chunk("VP8 ", []byte{1, 2, 3}))) {
		t.Error("plain webp flagged")
	}
	if !webpHasMetadata(riff(chunk("VP8X", make([]byte, 10)), chunk("VP8 ", []byte{1, 2, 3}), chunk("EXIF", []byte(secret)))) {
		t.Error("exif chunk not detected")
	}
	if !webpHasMetadata([]byte("RIFF\x00\x00\x00\x00JUNK")) {
		t.Error("unreadable structure must be treated as having metadata")
	}
}

// GIF는 모든 프레임을 풀지 않고 개수로 판별한다
func TestGIFFrameCount(t *testing.T) {
	pal := []color.Color{color.Black, color.White}
	encode := func(frames int) []byte {
		g := &gif.GIF{}
		for i := 0; i < frames; i++ {
			g.Image = append(g.Image, image.NewPaletted(image.Rect(0, 0, 32, 32), pal))
			g.Delay = append(g.Delay, 10)
		}
		var b bytes.Buffer
		_ = gif.EncodeAll(&b, g)
		return b.Bytes()
	}
	if n := gifFrames(encode(1)); n != 1 {
		t.Fatalf("still gif frames = %d", n)
	}
	if n := gifFrames(encode(3)); n != 2 { // 두 번째에서 멈춘다
		t.Fatalf("animated gif frames = %d", n)
	}
	if _, err := Process(encode(1)); err != nil {
		t.Fatalf("still gif rejected: %v", err)
	}
	if n := gifFrames([]byte("GIF89a\x10\x00\x10\x00\x00\x00\x00\x99")); n != -1 {
		t.Fatalf("broken gif = %d", n)
	}
}

// 16비트 PNG는 화소당 메모리가 두 배라 더 낮은 해상도에서 거절한다
func TestSixteenBitPNGPixelBudget(t *testing.T) {
	img := image.NewNRGBA64(image.Rect(0, 0, 3200, 3200)) // 1,024만 화소 × 2
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	var ie *ImageError
	if _, err := Process(b.Bytes()); !errors.As(err, &ie) {
		t.Fatalf("16-bit 10MP+ png = %v", err)
	}
}
