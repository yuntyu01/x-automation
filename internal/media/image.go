package media

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // WebP 디코더 등록
)

const (
	MaxUploadBytes = 15 << 20 // 받는 파일 상한
	MaxPostBytes   = 5 << 20  // X 이미지 첨부 상한 (docs/INTEGRATIONS.md)
	MaxPixels      = 20_000_000
	MaxSide        = 4096 // 이 이상은 줄여서 저장 (메모리 256Mi 환경 고려)
	ThumbSide      = 480
	MinSide        = 16
)

// ImageError는 사용자에게 보여 줄 검사 실패다.
type ImageError struct{ Message string }

func (e *ImageError) Error() string { return e.Message }

type Processed struct {
	Data        []byte
	ContentType string
	Ext         string
	Width       int
	Height      int
	Thumb       []byte
}

// Process는 업로드·생성 이미지를 검사하고 저장용으로 정리한다.
//   - 실제 내용으로 형식을 판별한다 (확장자·선언 형식은 믿지 않는다)
//   - JPEG의 EXIF·XMP(위치 정보 등)를 지우고 회전 정보는 픽셀에 반영한다
//   - 너무 크면 줄이고, X 첨부 상한(5MB)을 넘으면 JPEG로 다시 압축한다
//   - 썸네일(최대 480px JPEG)을 만든다
func Process(data []byte) (Processed, error) {
	if len(data) == 0 {
		return Processed{}, &ImageError{"빈 파일이에요"}
	}
	if len(data) > MaxUploadBytes {
		return Processed{}, &ImageError{"파일이 15MB를 넘어요"}
	}
	ct := http.DetectContentType(data)
	switch ct {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
	default:
		return Processed{}, &ImageError{"JPG·PNG·WEBP·GIF 이미지만 올릴 수 있어요"}
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Processed{}, &ImageError{"이미지를 읽을 수 없어요 (손상되었거나 지원하지 않는 형식)"}
	}
	if cfg.Width < MinSide || cfg.Height < MinSide {
		return Processed{}, &ImageError{fmt.Sprintf("이미지가 너무 작아요 (최소 %dpx)", MinSide)}
	}
	if cfg.Width*cfg.Height > MaxPixels {
		return Processed{}, &ImageError{"해상도가 너무 커요 (2,000만 화소 이하)"}
	}
	if format == "gif" {
		g, err := gif.DecodeAll(bytes.NewReader(data))
		if err != nil {
			return Processed{}, &ImageError{"GIF를 읽을 수 없어요"}
		}
		if len(g.Image) > 1 {
			return Processed{}, &ImageError{"움직이는 GIF는 지원하지 않아요. 정지 이미지로 올려 주세요"}
		}
	}

	out := Processed{ContentType: ct, Width: cfg.Width, Height: cfg.Height, Data: data}
	orientation := 1
	if format == "jpeg" {
		orientation = jpegOrientation(data)
		out.Data = stripJPEGMetadata(data)
	}

	needDecode := orientation != 1 || cfg.Width > MaxSide || cfg.Height > MaxSide || len(out.Data) > MaxPostBytes
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Processed{}, &ImageError{"이미지를 읽을 수 없어요"}
	}
	if needDecode {
		img = applyOrientation(img, orientation)
		img = fit(img, MaxSide)
		b := img.Bounds()
		out.Width, out.Height = b.Dx(), b.Dy()
		// PNG(투명도 가능)는 PNG로, 나머지는 JPEG로 다시 만든다
		if format == "png" {
			var buf bytes.Buffer
			if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&buf, img); err != nil {
				return Processed{}, err
			}
			out.Data, out.ContentType = buf.Bytes(), "image/png"
		}
		if format != "png" || len(out.Data) > MaxPostBytes {
			data, err := encodeJPEGUnder(img, MaxPostBytes)
			if err != nil {
				return Processed{}, err
			}
			out.Data, out.ContentType = data, "image/jpeg"
		}
	}
	out.Ext = extFor(out.ContentType)
	thumb, err := Thumbnail(img, ThumbSide)
	if err != nil {
		return Processed{}, err
	}
	out.Thumb = thumb
	return out, nil
}

func extFor(ct string) string {
	switch ct {
	case "image/png":
		return "png"
	case "image/gif":
		return "gif"
	case "image/webp":
		return "webp"
	}
	return "jpg"
}

func encodeJPEGUnder(img image.Image, limit int) ([]byte, error) {
	img = flatten(img)
	for _, q := range []int{90, 85, 78, 70, 60} {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: q}); err != nil {
			return nil, err
		}
		if buf.Len() <= limit {
			return buf.Bytes(), nil
		}
	}
	// 품질을 낮춰도 크면 크기를 줄인다
	b := img.Bounds()
	return encodeJPEGUnder(fit(img, max(b.Dx(), b.Dy())*3/4), limit)
}

// Thumbnail은 긴 변이 side 이하인 JPEG를 만든다. 투명 영역은 흰색으로 채운다.
func Thumbnail(img image.Image, side int) ([]byte, error) {
	t := flatten(fit(img, side))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, t, &jpeg.Options{Quality: 80}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func fit(img image.Image, side int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= side && h <= side {
		return img
	}
	if w >= h {
		h = max(1, h*side/w)
		w = side
	} else {
		w = max(1, w*side/h)
		h = side
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
	return dst
}

func flatten(img image.Image) image.Image {
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Over)
	return dst
}

// Crop은 픽셀 영역을 잘라 원본 형식(JPEG/PNG)으로 다시 만든다.
func Crop(data []byte, r image.Rectangle) (Processed, error) {
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Processed{}, &ImageError{"이미지를 읽을 수 없어요"}
	}
	r = r.Intersect(img.Bounds())
	if r.Dx() < MinSide || r.Dy() < MinSide {
		return Processed{}, &ImageError{"자르는 영역이 너무 작아요"}
	}
	dst := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	draw.Draw(dst, dst.Bounds(), img, r.Min, draw.Src)
	var buf bytes.Buffer
	if format == "png" {
		if err := png.Encode(&buf, dst); err != nil {
			return Processed{}, err
		}
	} else if err := jpeg.Encode(&buf, flatten(dst), &jpeg.Options{Quality: 92}); err != nil {
		return Processed{}, err
	}
	return Process(buf.Bytes())
}

// ── JPEG 메타데이터 ────────────────────────────────────────────────────

// jpegOrientation은 EXIF 방향 값(1~8)을 읽는다. 없거나 읽을 수 없으면 1.
func jpegOrientation(data []byte) int {
	seg := findAPP1Exif(data)
	if len(seg) < 14 {
		return 1
	}
	tiff := seg[6:]
	var bo binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 1
	}
	off := int(bo.Uint32(tiff[4:8]))
	if off+2 > len(tiff) {
		return 1
	}
	n := int(bo.Uint16(tiff[off : off+2]))
	for i := 0; i < n; i++ {
		e := off + 2 + i*12
		if e+12 > len(tiff) {
			return 1
		}
		if bo.Uint16(tiff[e:e+2]) == 0x0112 {
			v := int(bo.Uint16(tiff[e+8 : e+10]))
			if v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}

func findAPP1Exif(data []byte) []byte {
	i := 2
	for i+4 <= len(data) {
		if data[i] != 0xFF {
			return nil
		}
		marker := data[i+1]
		if marker == 0xDA || marker == 0xD9 { // 이미지 데이터 시작
			return nil
		}
		l := int(binary.BigEndian.Uint16(data[i+2 : i+4]))
		if l < 2 || i+2+l > len(data) {
			return nil
		}
		seg := data[i+4 : i+2+l]
		if marker == 0xE1 && bytes.HasPrefix(seg, []byte("Exif\x00\x00")) {
			return seg
		}
		i += 2 + l
	}
	return nil
}

// stripJPEGMetadata는 APP1(EXIF·XMP)·APP13(IPTC)·주석 세그먼트를 픽셀 손실 없이 지운다.
func stripJPEGMetadata(data []byte) []byte {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return data
	}
	var out bytes.Buffer
	out.Write(data[:2])
	i := 2
	for i+4 <= len(data) {
		if data[i] != 0xFF {
			return data // 예상 밖 구조면 원본 유지
		}
		marker := data[i+1]
		if marker == 0xDA { // 스캔 시작: 나머지는 그대로
			out.Write(data[i:])
			return out.Bytes()
		}
		l := int(binary.BigEndian.Uint16(data[i+2 : i+4]))
		if l < 2 || i+2+l > len(data) {
			return data
		}
		if marker != 0xE1 && marker != 0xED && marker != 0xFE {
			out.Write(data[i : i+2+l])
		}
		i += 2 + l
	}
	return data
}

func applyOrientation(img image.Image, o int) image.Image {
	if o <= 1 || o > 8 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	var dst *image.RGBA
	if o >= 5 {
		dst = image.NewRGBA(image.Rect(0, 0, h, w))
	} else {
		dst = image.NewRGBA(image.Rect(0, 0, w, h))
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := img.At(b.Min.X+x, b.Min.Y+y)
			var nx, ny int
			switch o {
			case 2:
				nx, ny = w-1-x, y
			case 3:
				nx, ny = w-1-x, h-1-y
			case 4:
				nx, ny = x, h-1-y
			case 5:
				nx, ny = y, x
			case 6:
				nx, ny = h-1-y, x
			case 7:
				nx, ny = h-1-y, w-1-x
			case 8:
				nx, ny = y, w-1-x
			}
			dst.Set(nx, ny, c)
		}
	}
	return dst
}
