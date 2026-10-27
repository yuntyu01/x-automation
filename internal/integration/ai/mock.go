package ai

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"
)

// Mock은 목업 모드의 공급자다. 실제 AI를 부르지 않고 입력에서 결정적으로 글·이미지를 만든다.
// 만든 글에는 목업이라는 표시가 들어간다 (실제 생성 성공으로 오해하지 않게).
type Mock struct {
	Fail func(op string) error // 테스트에서 실패를 주입한다
}

func (m *Mock) Name() string { return "mock" }

func (m *Mock) GenerateThread(_ context.Context, req ThreadRequest) (ThreadDraft, Usage, error) {
	if m.Fail != nil {
		if err := m.Fail("text"); err != nil {
			return ThreadDraft{}, Usage{}, err
		}
	}
	sum := sha256.Sum256([]byte(req.Prompt + req.Instructions))
	seed := fmt.Sprintf("%x", sum[:3])
	subject := firstLine(req.Prompt, 24)
	body := fmt.Sprintf("[목업 생성 %s] %s에 대한 이야기를 시작한다. 처음에는 별일 아닌 것처럼 보였다. 그런데 작은 단서 하나가 모든 것을 바꿔 놓았다.\n\n"+
		"며칠 뒤 나는 그 단서를 다시 꺼내 보았다. 그리고 그날 일어난 일이 우연이 아니었다는 것을 알게 되었다. 이야기는 여기서 끝나지 않는다.", seed, subject)
	if strings.Contains(req.Instructions, "짧게") {
		body = fmt.Sprintf("[목업 생성 %s] %s. 작은 단서 하나가 모든 것을 바꿨다.", seed, subject)
	}
	d := ThreadDraft{Title: "목업 " + subject, Body: body, ImagePrompt: "목업 이미지: " + subject, Tags: []string{"목업"}}
	return d, Usage{InputTokens: int64(len([]rune(req.Prompt + req.Instructions))), OutputTokens: int64(len([]rune(body)))}, nil
}

func (m *Mock) GenerateImages(_ context.Context, req ImageRequest) ([]Image, Usage, error) {
	if m.Fail != nil {
		if err := m.Fail("image"); err != nil {
			return nil, Usage{}, err
		}
	}
	n := max(req.Count, 1)
	var out []Image
	for i := 0; i < n; i++ {
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s#%d", req.Prompt, i)))
		img := image.NewRGBA(image.Rect(0, 0, 512, 512))
		for y := 0; y < 512; y++ {
			for x := 0; x < 512; x++ {
				img.Set(x, y, color.RGBA{sum[0] / 2, uint8(int(sum[1]) * y / 512), uint8(int(sum[2]) * x / 512), 255})
			}
		}
		var b bytes.Buffer
		_ = png.Encode(&b, img)
		out = append(out, Image{Data: b.Bytes(), ContentType: "image/png"})
	}
	return out, Usage{Images: n}, nil
}

func firstLine(s string, n int) string {
	s = strings.TrimSpace(strings.SplitN(strings.TrimSpace(s), "\n", 2)[0])
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	if s == "" {
		return "주제 없음"
	}
	return s
}
