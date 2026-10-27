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

## 작업 큐와 생성

- 작업(`jobs`)은 `(kind, unique_key)`가 고유하다. 같은 일을 두 번 넣으면 기존 작업을 돌려준다
- 워커는 `FOR UPDATE SKIP LOCKED`로 하나씩 선점하고 lease(소유자·버전·만료)를 heartbeat로 늘린다. 결과 저장은 소유자·버전이 맞을 때만 반영된다. 외부 호출 직전에 소유권을 다시 확인한다
- lease가 끝난 실행 중 작업: 생성(`generate.item`)은 다시 대기(같은 생성을 다시 해도 외부 중복 게시가 없다), 그 밖의 작업은 `unknown`(결과 확인 필요)
- 생성 요청 하나는 수량만큼의 `generate.item` 작업이다. 요청 고유키는 버튼은 화면이 만든 키, 자동 생성은 전략·기간·정책 버전으로 만든다

### 부족분

```
부족분 = 창 안의 콘텐츠 없는 예약
       − 준비 완료·미배정 콘텐츠 (content_overview stage=ready, 예시 제외)
       − 진행 중 생성 (예약에 묶이지 않은 요청의 남은 수량)
       − 검토 대기 AI 생성분 (review_state=pending, generation_request_id 있음)
→ min(부족분, 오늘 남은 생성 상한, 100)
```

- 특정 예약에 묶인 진행 중 생성이 있는 예약은 창에서 뺀다 (이미 채워지는 중)
- 미완성 초안·미승인 글은 준비 완료로 세지 않는다. 다만 검토 후 게시 모드에서 AI가 만든 검토 대기 글은 "확보된 생성 자리"로 본다. 그렇지 않으면 승인을 기다리는 동안 매 주기 같은 수를 다시 생성해 비용이 쌓인다
- 예약 전 N분·게시할 때 전략은 재고로 덮을 수 있는 앞쪽 예약은 남기고, 덮지 못하는 뒤쪽 예약에만 생성 요청을 묶는다 (재고는 시간표가 앞 예약부터 배정)
- 전략 판단은 `generation_marks`(키 = 전략·기간·정책 버전)에 남긴다. 부족분이 0이어도 남겨 같은 기간을 다시 판단하지 않고, 화면에 "최근 자동 판단"으로 보여 준다
- 계획 판단은 `pg_advisory_xact_lock` 안에서 하므로 여러 인스턴스가 겹쳐도 요청이 한 번만 생긴다

### 비용 예약

- 한도가 있으면 생성 작업은 외부 호출 전에 추정 비용을 `budget_reservations`에 예약한다 (실행마다 다른 키 = 요청·번호·lease 버전). 합계 계산은 트랜잭션 잠금 안에서 해 동시에 한도를 넘지 않는다
- 사용량은 `usage_ledger`에 `(category, ref_key)`로 한 번만 기록하고, 실행이 끝나면 예약을 닫는다. 프로세스가 죽어 남은 예약은 6시간 뒤 해제한다
- 금액은 설정의 요율표로 계산한 추정치다. 요율표에 확인일이 없으면 화면에 "단가 미확인"으로 보인다

이 문서는 단계가 진행되며 갱신한다. 외부 API 세부(권한·제한·가격 확인일)는 `docs/INTEGRATIONS.md`에 둔다.
