# 구조와 주요 결정

1인 운영자가 X 타래(본문 + 자기 답글)를 준비·예약·게시하는 앱. Go 프로세스 하나가 프론트·API·워커를 모두 담당하는 모놀리스다.

## 전체 그림

```
브라우저 (React SPA, 모바일 우선)
   │  같은 origin · 세션 쿠키 · CSRF 헤더
   ▼
Go 서버 (cmd/server)
   ├─ internal/httpapi    라우팅·요청 검증·JSON 오류
   ├─ internal/web        embed.FS 정적 파일 + SPA 대체 경로
   ├─ 도메인 패키지        content · prompt · thread · schedule · generation · publish · media · budget · notification
   ├─ internal/jobs       PostgreSQL 작업 큐 (FOR UPDATE SKIP LOCKED + lease)
   └─ internal/integration  X · AI · R2 실제 어댑터와 테스트 대역
   │
   ├─▶ PostgreSQL   영속 데이터 + 작업 큐 + 세션 + 알림
   ├─▶ Cloudflare R2 (S3 호환)  이미지 원본·썸네일, 공개 조회
   ├─▶ X API v2     OAuth 2.0 사용자 토큰으로 미디어 업로드·게시
   └─▶ AI 공급자     텍스트·이미지 생성 (구조화 출력 검증)
```

## 연동 경계

| 경계 | 원칙 |
|---|---|
| X | `integration/x`의 인터페이스 뒤에 실제 HTTP 클라이언트와 테스트용 가짜 서버. 게시 요청 전 의도 기록, 성공 ID 즉시 저장, 응답 불명확은 `unknown` 보류 |
| AI | `integration/ai` 인터페이스. 출력은 JSON 스키마로 검증하고 텍스트로만 취급. 모델명·단가는 설정·요율표로 관리 |
| R2 | `integration/storage` 인터페이스. 실제 S3 호환 어댑터와 로컬 파일 대역. 앱 전용 접두사 밖은 읽거나 지우지 않음 |
| 시계·난수 | `internal/clock`, `internal/randx` 주입. 시간표·재시도 테스트를 결정적으로 만든다 |

`INTEGRATION_MODE=mock`이면 세 연동 모두 대역을 쓰고 화면·로그에 "목업"을 표시한다. 운영(`APP_ENV=production`)에서 키가 없다고 목업으로 자동 전환하지 않는다 — 연결 안 됨 상태로 남는다.

## 주요 결정과 이유

| 결정 | 이유 |
|---|---|
| Go 1.27.3 (go.mod toolchain 고정) | 작성 시점 최신 안정판. pgx·x/crypto 최신판이 1.25+/1.26+를 요구 |
| 표준 `net/http` ServeMux (메서드·경로 패턴) | 1.22+ 패턴 라우팅으로 충분. 외부 라우터 불필요 |
| pgx v5 + 자체 마이그레이션 실행기 | 마이그레이션은 순서 있는 SQL 파일을 embed하고 `pg_advisory_lock`으로 동시 실행을 막는다. 파괴적 변경을 자동 실행하지 않음 |
| PostgreSQL 작업 큐 | Redis 등 추가 의존성 없이 lease·재시도·고유키를 한 트랜잭션에서 처리 |
| React 19 + TypeScript 6 + Vite 8 | 지시서 요구. TS 7(네이티브 포트)은 typescript-eslint가 아직 지원하지 않아 6.0을 쓴다 |
| TanStack Query | 서버 상태 캐시·재시도·polling을 일관되게 처리. 상태 갱신은 polling(간격 조절)으로 충분 |
| 가중 글자 수: 프론트 `twitter-text`(공식), Go는 같은 규칙의 구현 + 공식 적합성 fixture | 운영 Node 서버를 두지 않고 양쪽 결과를 같은 fixture로 대조 |
| X 인증: OAuth 2.0 Authorization Code + PKCE | 갱신 토큰(offline.access)으로 무인 운영. 토큰은 TOKEN_ENCRYPTION_KEY(AES-256-GCM)로 암호화해 DB 저장 |
| 비밀번호: Argon2id (golang.org/x/crypto/argon2) | OWASP 권장. 해시 생성 CLI `cmd/admin` 제공 |
| 세션: 서버 저장(DB) + HttpOnly·Secure·SameSite=Lax 쿠키 + 동기화 토큰 CSRF | OAuth 콜백이 최상위 이동이라 Lax 필요. 상태 변경 요청은 CSRF 헤더 필수 |
| 시각 저장 | DB는 timestamptz(UTC). 시간대·정책은 별도 컬럼. tzdata를 바이너리에 포함 |
| 정적 파일 | `web/` 빌드 결과를 `internal/web/dist`로 내보내고 `embed.FS`로 포함. `/api/`는 SPA로 대체하지 않음 |

## 디렉터리

| 경로 | 책임 |
|---|---|
| `cmd/server` | HTTP·워커 시작, 종료 처리 |
| `cmd/admin` | 비밀번호 해시 생성 등 운영 보조 명령 |
| `internal/*` | 지시서 10.1절의 모듈 구성 (단계별로 채움) |
| `migrations` | 순서 있는 SQL 마이그레이션 (embed) |
| `web/` | React 앱 (`web/src`), Vite 설정, E2E |
| `docs/` | 구조·API·배포·운영·진행 기록 |

이 문서는 단계가 진행되며 갱신한다. 외부 API 세부(권한·제한·가격 확인일)는 `docs/INTEGRATIONS.md`에 둔다.
