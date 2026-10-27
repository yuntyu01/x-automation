// admin은 운영 보조 명령이다.
//
//	go run ./cmd/admin hash-password   비밀번호를 입력받아 ADMIN_PASSWORD_HASH 값을 출력
//	go run ./cmd/admin gen-key         TOKEN_ENCRYPTION_KEY로 쓸 32바이트 키 출력 (한 번만 만들고 보관)
//	go run ./cmd/admin migrate         DATABASE_URL(또는 DB_*)에 마이그레이션 적용
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/yuntyu01/x-automation/internal/auth"
	"github.com/yuntyu01/x-automation/internal/config"
	"github.com/yuntyu01/x-automation/internal/store"
	"github.com/yuntyu01/x-automation/migrations"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "hash-password":
		err = hashPassword()
	case "gen-key":
		err = genKey()
	case "migrate":
		err = migrate()
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "오류:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `사용법: admin <명령>
  hash-password  비밀번호를 입력받아 Argon2id 해시(ADMIN_PASSWORD_HASH)를 출력
  gen-key        TOKEN_ENCRYPTION_KEY용 키를 출력 (한 번 만들어 계속 사용)
  migrate        데이터베이스 마이그레이션 적용`)
}

func readSecret(prompt string) (string, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		fmt.Fprint(os.Stderr, prompt)
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		return string(b), err
	}
	// 파이프 입력 (자동화용). 첫 줄만 읽는다
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func hashPassword() error {
	p1, err := readSecret("새 비밀번호: ")
	if err != nil {
		return err
	}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		p2, err := readSecret("한 번 더: ")
		if err != nil {
			return err
		}
		if p1 != p2 {
			return fmt.Errorf("두 입력이 다릅니다")
		}
	}
	h, err := auth.HashPassword(p1, auth.DefaultParams)
	if err != nil {
		return err
	}
	fmt.Println(h)
	fmt.Fprintln(os.Stderr, ".env에 넣을 때는 작은따옴표로 감싸세요 (예: ADMIN_PASSWORD_HASH='$argon2id$...'). 배포 환경변수 화면에는 그대로 넣습니다.")
	return nil
}

func genKey() error {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	fmt.Println(base64.StdEncoding.EncodeToString(b))
	fmt.Fprintln(os.Stderr, "이 값을 비밀 저장소에 보관하세요. 바꾸면 저장된 X 연결 토큰을 다시 읽을 수 없습니다.")
	return nil
}

func migrate() error {
	// 마이그레이션에는 DB 연결만 필요하다. 나머지 필수값 검사는 건너뛴다
	lookup := func(k string) (string, bool) {
		switch k {
		case "APP_ENV":
			return "test", true
		}
		return os.LookupEnv(k)
	}
	cfg, err := config.LoadFrom(lookup)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	pool, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	applied, err := store.Migrate(ctx, pool, migrations.FS)
	if err != nil {
		return err
	}
	if len(applied) == 0 {
		fmt.Println("적용할 마이그레이션이 없습니다")
	}
	for _, a := range applied {
		fmt.Println("적용:", a)
	}
	return nil
}
