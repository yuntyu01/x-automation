// Package randx는 난수원을 주입할 수 있게 한다. 랜덤 지연은 시간표를 만들 때 한 번만 뽑아 DB에 저장한다.
package randx

import (
	"math/rand/v2"
	"sync"
)

type Source interface {
	// IntN은 [0, n) 범위의 정수를 돌려준다. n <= 0이면 0.
	IntN(n int) int
}

// Default는 프로세스 전역 난수원이다.
type Default struct{}

func (Default) IntN(n int) int {
	if n <= 0 {
		return 0
	}
	return rand.IntN(n)
}

// Seeded는 테스트에서 재현 가능한 난수원이다.
type Seeded struct {
	mu sync.Mutex
	r  *rand.Rand
}

func NewSeeded(seed uint64) *Seeded {
	return &Seeded{r: rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))}
}

func (s *Seeded) IntN(n int) int {
	if n <= 0 {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.r.IntN(n)
}

// Fixed는 미리 정한 값을 순서대로 돌려준다 (값이 n 이상이면 n-1로 자른다).
type Fixed struct {
	mu     sync.Mutex
	Values []int
	i      int
}

func (f *Fixed) IntN(n int) int {
	if n <= 0 {
		return 0
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.Values) == 0 {
		return 0
	}
	v := f.Values[f.i%len(f.Values)]
	f.i++
	if v >= n {
		v = n - 1
	}
	if v < 0 {
		v = 0
	}
	return v
}
