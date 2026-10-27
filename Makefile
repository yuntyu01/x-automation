# 개발 인터페이스. 자세한 설명은 README.md
SHELL := /bin/bash
GO ?= go
NPM ?= npm
BIN := bin/server
# DB 통합 테스트용 계정 (테스트마다 임시 DB를 만들고 지운다). make db-up 으로 띄운 로컬 DB 기준
TEST_DATABASE_URL ?= postgres://xauto:xauto@127.0.0.1:5432/postgres?sslmode=disable
export TEST_DATABASE_URL

.PHONY: setup dev dev-api dev-web db-up db-down test test-go test-web lint build web-build e2e docker-build

setup: ## 의존성 설치·개발 설정 안내 (비밀값은 만들지 않는다)
	@command -v $(GO) >/dev/null || { echo "Go가 필요합니다 (go.mod의 toolchain을 자동으로 받습니다)"; exit 1; }
	@command -v node >/dev/null || { echo "Node.js 22.13 이상이 필요합니다"; exit 1; }
	$(GO) mod download
	cd web && $(NPM) ci
	@test -f .env || { cp .env.example .env; echo ".env를 만들었습니다. ADMIN_PASSWORD_HASH·TOKEN_ENCRYPTION_KEY를 채우세요 (README 참고)"; }

dev: ## API와 Vite 개발 서버를 함께 실행 (Ctrl+C로 둘 다 종료)
	@trap 'kill 0' EXIT; $(MAKE) --no-print-directory dev-api & $(MAKE) --no-print-directory dev-web & wait

dev-api:
	@set -a; [ -f .env ] && . ./.env; set +a; $(GO) run ./cmd/server

dev-web:
	cd web && $(NPM) run dev

db-up: ## 로컬 PostgreSQL (docker compose)
	docker compose up -d --wait db

db-down:
	docker compose down

test: test-go test-web

test-go:
	$(GO) test ./...

test-web:
	cd web && $(NPM) test

lint:
	$(GO) vet ./...
	$(GO) tool staticcheck ./...
	cd web && $(NPM) run typecheck && $(NPM) run lint

web-build:
	cd web && $(NPM) run build

build: web-build ## 프론트 빌드 후 Go 바이너리
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o $(BIN) ./cmd/server

e2e:
	@echo "E2E는 13단계에서 추가됩니다"; exit 1

docker-build:
	@echo "Dockerfile은 14단계에서 추가됩니다"; exit 1
