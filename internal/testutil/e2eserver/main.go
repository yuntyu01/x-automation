// Command e2eserver는 브라우저 E2E용 서버를 띄운다.
//
//   - 이름에 e2e가 들어간 전용 데이터베이스를 지우고 새로 만든다 (다른 DB를 실수로 지우지 않게 이름을 검사)
//   - 목업 연동(INTEGRATION_MODE=mock)으로 실행한다: 실제 X 게시·AI 호출·R2 저장이 일어나지 않는다
//   - 관리자 비밀번호 해시와 토큰 키는 실행마다 새로 만든다 (비밀값을 파일·저장소에 남기지 않음)
//   - 빌드된 서버 바이너리(bin/server)를 그대로 실행한다
//
// 환경변수: E2E_ADMIN_DATABASE_URL(DB를 만들 접속, 기본 로컬 postgres), E2E_DB_NAME(기본 xa_e2e),
// E2E_PORT(기본 18090), E2E_PASSWORD(기본 e2e-test-password), E2E_SERVER_BIN(기본 bin/server)
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"github.com/jackc/pgx/v5"

	"github.com/yuntyu01/x-automation/internal/auth"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "e2eserver:", err)
		os.Exit(1)
	}
}

func run() error {
	adminURL := env("E2E_ADMIN_DATABASE_URL", "postgres://xauto:xauto@127.0.0.1:5432/postgres?sslmode=disable")
	dbName := env("E2E_DB_NAME", "xa_e2e")
	if !strings.Contains(dbName, "e2e") || strings.ContainsAny(dbName, `"; `) {
		return fmt.Errorf("E2E_DB_NAME(%q)에는 e2e가 들어가야 하고 따옴표·공백이 없어야 합니다", dbName)
	}
	port := env("E2E_PORT", "18090")
	password := env("E2E_PASSWORD", "e2e-test-password")
	bin := env("E2E_SERVER_BIN", "bin/server")

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return fmt.Errorf("DB 접속 실패: %w", err)
	}
	for _, q := range []string{fmt.Sprintf(`DROP DATABASE IF EXISTS "%s" WITH (FORCE)`, dbName), fmt.Sprintf(`CREATE DATABASE "%s"`, dbName)} {
		if _, err := conn.Exec(ctx, q); err != nil {
			_ = conn.Close(ctx)
			return err
		}
	}
	_ = conn.Close(ctx)
	u, err := url.Parse(adminURL)
	if err != nil {
		return err
	}
	u.Path = "/" + dbName

	hash, err := auth.HashPassword(password, auth.Params{Memory: 16 * 1024, Time: 1, Threads: 1, KeyLen: 32, SaltLen: 16})
	if err != nil {
		return err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	dataDir, err := os.MkdirTemp("", "xa-e2e-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dataDir)

	cmd := exec.Command(bin)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(),
		"APP_ENV=development",
		"PORT="+port,
		"APP_BASE_URL=http://127.0.0.1:"+port, // 브라우저가 쓰는 주소와 같아야 출처 검사를 통과한다
		"DATABASE_URL="+u.String(),
		"ADMIN_USERNAME=admin",
		"ADMIN_PASSWORD_HASH="+hash,
		"TOKEN_ENCRYPTION_KEY="+base64.StdEncoding.EncodeToString(key),
		"INTEGRATION_MODE=mock",
		"PUBLISHING_ENABLED=false",
		"DATA_DIR="+dataDir,
		"LOG_LEVEL=warn",
	)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("서버 실행 실패 (%s, 먼저 make build): %w", bin, err)
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		s := <-sig
		_ = cmd.Process.Signal(s)
	}()
	return cmd.Wait()
}
