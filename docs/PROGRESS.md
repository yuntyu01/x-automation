# 진행 기록

지시서: `docs/BUILD_INSTRUCTIONS.md`. 세션을 재개하면 이 파일의 "다음 작업"부터 이어 간다.

## 단계 체크리스트

| 단계 | 내용 | 상태 | 커밋 |
|---|---|---|---|
| 01 | 저장소 조사와 실행 기반 | 완료 | eaddf85 |
| 02 | PostgreSQL·설정·마이그레이션 | 완료 | 560ecbe |
| 03 | 고정 관리자 인증 | 완료 | (보고에 기록) |
| 04 | 모바일 앱 셸·디자인 시스템 | 대기 | |
| 05 | 콘텐츠·프롬프트·직접 등록 | 대기 | |
| 06 | R2·이미지 관리 | 대기 | |
| 07 | AI 생성·세 가지 생성 정책 | 대기 | |
| 08 | 길이 검증·분할·타래 미리보기 | 대기 | |
| 09 | 시간표·예약·랜덤 추가 지연 | 대기 | |
| 10 | X 연결·본문과 답글 게시·복구 | 대기 | |
| 11 | 비용 제어·내부 알림·운영 홈 | 대기 | |
| 12 | 게시 후 14일 정리·재배포 내구성 | 대기 | |
| 13 | 전체 사용자 흐름·모바일 품질 검증 | 대기 | |
| 14 | 배포·CI·문서·최종 main 검증 | 대기 | |

커밋 SHA는 같은 커밋 안에 넣을 수 없으므로 다음 단계 기록에서 앞 단계 SHA를 채운다.

## 최종 인수 조건 추적 (지시서 16절)

| 번호 | 시나리오 | 구현 경로 | 검증 근거 | 상태 |
|---|---|---|---|---|
| A01 | 지정 관리자만 로그인, 미인증 API 차단 | `internal/auth`, `internal/httpapi/server.go`(requireAuth), `web/src/auth` | `internal/app/auth_test.go`(미인증 401·CSRF·출처·만료·시도 제한·XFF 우회 불가·로그아웃), 브라우저 로그인 후 원래 경로 복귀 | 진행 (기능 API 추가 시 계속 확인) |
| A02 | Go 한 서버에서 웹·API, 깊은 링크 새로고침 | `internal/web` | `internal/web/embed_test.go` (깊은 링크→index, 없는 자산 404) | 진행 |
| A03 | 프롬프트·직접 작성·CSV/JSON·자동 저장·검색 | | | 대기 |
| A04 | 세 운영 모드, 텍스트·이미지 독립 | | | 대기 |
| A05 | 세 생성 시점·세 사전 생성 전략·부족분만 생성 | | | 대기 |
| A06 | 20묶음과 실제 게시물 수 구분 | | | 대기 |
| A07 | 추가 답글·가중 길이·원문 보존 | | | 대기 |
| A08 | 복수 구간·30분+추가 지연·영속화 | | | 대기 |
| A09 | 시간 부족·자정·고정·미준비·다운타임 | | | 대기 |
| A10 | 미승인·승인 후 변경분 게시 차단 | | | 대기 |
| A11 | AI 이미지·업로드·재사용·부분별 첨부, R2 공개/관리 구분 | | | 대기 |
| A12 | 답글부터 재개, 불명확 전송 재게시 차단 | | | 대기 |
| A13 | 워커 2개·즉시 게시·취소 경쟁 | | | 대기 |
| A14 | 앱 내부 알림 | | | 대기 |
| A15 | 사용량 추정·예산 한도·동시 예약 | | | 대기 |
| A16 | 게시 후 14일 정리·참조 보호 | | | 대기 |
| A17 | 라이트·다크 모바일 흐름·접근성 | | | 대기 |
| A18 | 재배포 후 유지·정상 종료·복구 | `internal/app` (DB 재시도·준비 상태), `internal/store/migrate.go`, `internal/settings` | `store/migrate_test.go`(재실행·동시 실행·체크섬·롤백), `settings_test.go`(자동 운영 상태 보존), SIGTERM 종료 스모크 | 진행 |
| A19 | Docker·ARM64·CI·환경변수·KoDeploy 문서 | | | 대기 |
| A20 | 14단계 결과·main 커밋·푸시·정직한 보고 | | | 대기 |

## 단계별 기록

### 01 저장소 조사와 실행 기반 — 완료

- 조사: 원격 `https://github.com/yuntyu01/x-automation`, 커밋 없는 빈 저장소(main). 기존 코드·지침 없음. 푸시 권한은 dry-run으로 확인
- 도구: Go 1.27.3(go.mod toolchain), Node 22.22, npm 10.9. 의존성은 정확한 버전으로 고정하고 `go.sum`·`package-lock.json`을 커밋
- 구성: Go 모듈, `cmd/server`(헬스·SPA 제공·종료 신호 처리), `internal/web`(embed + 깊은 링크), `web/`(React 19·TS 6·Vite 8·ESLint 10·Vitest 5), Makefile, `.env.example`, `.gitignore`
- 결정 기록: `docs/ARCHITECTURE.md`
- 검증: `go vet`·`staticcheck`·`go test ./...` 통과, `tsc -b`·`eslint`·`vite build` 통과, 서버 실행 후 `/healthz` 200, `/contents/abc` → index.html 200, `/assets/nope.js` 404
- 차단 요인: 이 개발 환경에서 Docker 데몬이 꺼져 있음(14단계에서 이미지 빌드 검증 시 재확인). docs.x.com 등 일부 문서 사이트는 셸 프록시에서 막혀 웹 조회 도구로 확인
- 다음: 02 PostgreSQL 스키마·설정 검증·마이그레이션

### 02 PostgreSQL·설정·마이그레이션 — 완료

- 스키마 `migrations/0001_init.sql`: 설정·프롬프트/버전·콘텐츠/revision·타래 부분·이미지/부분 연결·시간표 규칙/구간/예외일·예약·작업 큐·생성 요청·게시 실행/부분·외부 호출 기록·연결·OAuth state·세션·로그인 시도·알림·사용량·예산 예약. 생성·검토·예약·게시 상태를 별도 컬럼으로 분리, X ID는 text, 게시 이력 참조는 RESTRICT
- 마이그레이션 실행기 `internal/store/migrate.go`: embed SQL, `pg_advisory_lock`, 파일별 트랜잭션, 체크섬 불일치·모르는 버전이면 중단
- 설정 `internal/config`: 필수값(APP_ENV·DB·관리자·암호화 키)과 선택 연동값 구분, 여러 오류를 한 번에 한국어로 출력, 운영에서 mock·http·평문 비밀번호 거부, mock+실게시 동시 금지. KoDeploy 관리형 DB(`DB_*`, SQLAlchemy 형식 URL)와 오브젝트 스토리지(`S3_*`) 대체 경로
- 운영 설정 `internal/settings`: 자동 운영·생성/게시 정지 플래그와 정책(JSON) 분리, 낙관적 버전, 기본값은 "제안 초기값"
- 서버 `internal/app`: DB 연결 실패 시 프로세스를 죽이지 않고 backoff 재시도, `/readyz`는 DB·마이그레이션 준비 시에만 200, API는 준비 전 503 JSON. 구조화 로그와 비밀 가림(`internal/logx`)
- 시계·난수·외부 연동 인터페이스: `internal/clock`, `internal/randx`, `internal/integration/{xapi,ai,storage}`
- 테스트 DB: `internal/testutil/pgtest`가 `TEST_DATABASE_URL` 계정으로 테스트마다 임시 DB 생성·삭제. `make db-up`(compose) 후 `make test`
- 외부 연동 공식 자료 확인: `docs/INTEGRATIONS.md`
- 검증: `make lint`, `make test` 통과(설정 검증 9건, 마이그레이션 4건, 설정 저장 3건, 로그 가림). 스모크: 설정 누락 시 종료코드 2와 항목별 메시지, DB 불가 시 `/healthz` 200·`/readyz` 503·API 503, DB 준비 후 `/readyz` 200, SIGTERM 정상 종료
- 다음: 03 고정 관리자 인증

### 03 고정 관리자 인증 — 완료

- 비밀번호: Argon2id(m=64MiB·t=3·p=2, KoDeploy 256Mi 메모리 고려), PHC 문자열, 일정 시간 비교. 없는 아이디에도 같은 해시 검증을 돌려 응답 시간 차이를 줄임
- 관리 명령 `cmd/admin`: `hash-password`(터미널 무에코 입력·파이프 지원), `gen-key`(32바이트 base64), `migrate`
- 세션: 서버 저장(ID 원문 대신 SHA-256 해시), 32바이트 무작위 ID, HttpOnly·SameSite=Lax·(HTTPS 또는 운영이면) Secure, 서버 시계 기준 Max-Age, 로그인마다 새 세션(고정 방지), 로그아웃 즉시 무효화, 관리자 아이디 변경 시 기존 세션 무효
- CSRF: 세션별 동기화 토큰을 `X-CSRF-Token` 헤더로 검증 + Origin/Referer 동일 출처 검사. JSON 전용 본문
- 시도 제한: 15분 창에서 IP당 실패 20회·아이디당 10회 초과 시 잠금(429 + Retry-After). `TRUSTED_PROXY_CIDRS`에 든 프록시만 X-Forwarded-For를 따름
- 공개 경로: `/healthz`, `/readyz`, 정적 파일, `POST /api/auth/login`, `GET /api/auth/session`(비로그인도 200 `authenticated:false`). 나머지 `/api/*`는 서버 인증 필수, 모르는 API는 JSON 404(SPA 대체 안 함)
- 프론트: API 클라이언트(CSRF 자동 첨부, 네트워크 단절 시 명확한 오류), AuthProvider, RequireAuth(로그인 후 `next`로 복귀, 외부 주소 차단), 로그인 화면, 라이트·다크·시스템 테마 기반(디자인 토큰)
- 운영 기동 거부: 운영에서 관리자 아이디·해시·암호화 키가 없거나 평문이면 시작하지 않음(02단계 설정 검증)
- 검증: `make lint`·`make test` 통과. 통합 테스트 6건(미인증 차단, 잘못된 로그인, 쿠키 속성, CSRF 누락·타 출처 403, 로그아웃 후 401, 세션 만료, 시도 제한·XFF 우회 불가·창 경과 후 해제, 비로그인 세션 응답). 브라우저(Playwright) 390px 라이트·다크, 360px 오류 표시, 1440px 로그인 후 `/contents/123?q=1` 복귀 확인
- 다음: 04 모바일 앱 셸·디자인 시스템
