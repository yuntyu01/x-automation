# 테스트

정책과 실패 복구를 중심으로 검사한다. 외부 연동(X·AI·R2)은 실제로 호출하지 않는다 — 목업 대역, 가짜 서버(httptest), 로컬 저장소로 검증하고, 실제 계정으로만 확인할 수 있는 항목은 아래 "실연동 미검증"에 따로 적는다.

## 명령

| 명령 | 내용 | 필요한 것 |
|---|---|---|
| `make lint` | `go vet`, staticcheck, `tsc -b`, ESLint | Go, Node 22.13+ |
| `make test` | Go 전체 테스트 + Vitest | DB 통합 테스트는 `TEST_DATABASE_URL`의 PostgreSQL(기본 `postgres://xauto:xauto@127.0.0.1:5432/postgres`, `make db-up`). 없으면 DB 테스트는 건너뛴다고 표시된다 |
| `make e2e` | 빌드 후 Playwright 브라우저 E2E | PostgreSQL, Chromium(`cd web && npx playwright install chromium`) |
| `make docker-build` | 배포 이미지 (`PLATFORM=linux/arm64` 등) | Docker BuildKit |

DB 통합 테스트는 테스트마다 임시 데이터베이스를 만들고 마이그레이션을 적용한 뒤 지운다(`internal/testutil/pgtest`). E2E는 이름에 `e2e`가 들어간 전용 DB(`xa_e2e`)만 지우고 다시 만든다(다른 이름이면 거부).

## 무엇을 검사하나

| 영역 | 위치 | 핵심 시나리오 |
|---|---|---|
| 인증·보안 | `internal/app/auth_test.go`, `internal/auth` | 미인증 401, CSRF·출처 검사, 세션 만료, 로그인 시도 제한(X-Forwarded-For 우회 불가), 로그아웃 |
| 설정·마이그레이션 | `internal/config`, `internal/store`, `internal/settings` | 필수값·형식, KoDeploy DB·S3 변수 해석, 마이그레이션 재실행·동시 실행·체크섬·롤백, 설정 버전 충돌 |
| 콘텐츠·프롬프트 | `internal/content`, `internal/prompt`, `internal/app/content_test.go` | 자동 저장·충돌, 검색(LIKE 이스케이프), CSV/JSON 왕복·수식 주입 방어, 승인 revision과 편집 후 승인 해제, 프롬프트 버전·변수 |
| 글자 수·분할 | `internal/thread`, `web/src/lib/thread.test.ts` | 공식 twitter-text 적합성 사례 전체, 라이브러리 결과와 같은 값(golden), 무작위 150개 × 모드: 누락·중복 없음·부분 ≤ 280·URL·이모지 비분할, 긴·악의적 입력 성능 |
| 생성 | `internal/generation`, `internal/integration/ai`, `internal/budget` | 세 시점·세 전략, 부족분만 생성(같은 날 재실행 중복 없음), 일일 상한, 모드별 허용, 예산 동시 예약(12건 중 정확히 10건), 한도 초과 시 외부 호출 없음, OpenAI 응답 해석(가짜 서버) |
| 시간표 | `internal/schedule` | 12:00→12:33→13:04→13:39 같은 간격, 구간 경계·자정·쉬는 날·고정 예약, 재시작해도 같은 시각, 미리보기 = 적용, 다운타임 뒤 지난 예약 정리, 늦은 게시 뒤 간격 |
| 게시 | `internal/publish`(실제 HTTP 클라이언트 + 가짜 X 서버 `xapi/xfake`) | 본문 성공 → 답글 실패 → 재시작 → 답글만 재개, 전송 후 시간 초과 → 불명확·자동 재전송 없음, 의도 기록 직후 종료, 디스패치 6개 동시·워커 2개·건너뛰기 경쟁, 실게시 스위치·자동 운영·일시정지·연결 게이트, 구간 밖 답글은 다음 구간, 토큰 갱신·만료, OAuth state 세션 결속·1회용, 예산 "새 게시까지 중지" |
| 운영 홈·알림 | `internal/dashboard`, `internal/notification` | 실제 DB 상태 요약, 반복 실패 묶음, 계속되는 상태 알림은 해결 전 1건, 콘텐츠 부족·예산 80%/초과 |
| 이미지·보관 | `internal/media`, `internal/integration/storage`, `internal/retention` | 이미지 검사·EXIF 제거·축소·중복·자르기, R2 어댑터(가짜 S3), 게시 완료 15일 뒤 정리·기록 보존, 미게시·초안·진행 중 보호, 재사용 시 기준 갱신, 붙이기 vs 정리 경쟁, 실패 재시도·중단 복구 |
| 작업 큐 | `internal/jobs` | 중복 등록 방지·배타적 선점, lease 손실·회수 정책, 재시도, 종료 신호 때 진행 중 작업 결과 저장·새 선점 중지 |
| 브라우저 E2E | `web/e2e` | 로그인·로그아웃, 첫 설정 → 등록 → 분할 → 예약 → 목업 게시 → 결과·X 링크 → 홈 수치, AI 생성(목업)·일괄 보관, 네트워크·세션·미연결·저장 실패 회복, 14개 화면 × 4개 폭 × 라이트·다크 가로 넘침, 하단 탭 가림·터치 크기·버튼 이름·키보드 시트 |
| 컨테이너 | CI `container` 작업 | arm64·amd64 빌드, amd64 이미지로 마이그레이션·준비 상태·로그인·정상 종료(코드 0) |

## 실연동 미검증 (실제 계정·키가 있어야 확인 가능)

- **X**: 실제 OAuth 동의 화면·토큰 교환, 실제 게시·자기 답글·미디어 업로드, 자기 답글이 2026-02 답글 제한의 예외인지, 계정 등급별 장문 게시, 실제 429 제한 값. 첫 운영 게시 때 게시 기록에서 확인한다
- **AI**: 실제 모델 이름·응답 품질·이미지 장당 요금
- **R2**: 실제 버킷·토큰 권한, 커스텀 도메인 캐시
- **KoDeploy**: 실제 빌드(ARM64 BuildKit)·프로브·환경 주입·관리형 DB 백업 경로

목업 성공은 실제 게시·저장·생성 성공이 아니다. 화면 상단 "목업 모드" 띠와 설정 → 정보의 실행 환경으로 구분한다.

## 화면 캡처

`E2E_SCREENSHOT_DIR=/경로 make e2e`로 실행하면 홈·편집·게시 기록·콘텐츠와 390px 라이트·다크 주요 화면을 저장한다. README의 화면 예시는 이 방법으로 새 테스트 DB(목업 데이터)에서 찍었다.
