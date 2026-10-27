# API

Go 서버 하나가 화면(SPA)과 `/api/*`를 함께 제공한다. 모든 응답은 JSON이고, 화면 문구·오류 메시지는 한국어다.

## 공통 규칙

- **인증**: `POST /api/auth/login`이 HttpOnly·SameSite=Lax 세션 쿠키를 준다(운영은 Secure). `/api/auth/*`, `GET /api/x/oauth/callback`, `/healthz`, `/readyz`, 공개 이미지(`/media/*`, 목업 저장소일 때)만 로그인 없이 열린다. 그 밖은 `401 unauthorized`
- **CSRF**: GET이 아닌 요청은 `X-CSRF-Token` 헤더(로그인·세션 응답의 `csrf_token`)와 같은 출처(Origin/Referer)가 필요하다. 없으면 `403 csrf_failed`·`bad_origin`
- **오류 형식**: `{"error": {"code", "message", "fields"?, "details"?, "request_id"}}`
  - `422 validation_failed`: `fields`에 입력 칸별 이유
  - `409 needs_confirm`: 규칙과 다른 작업(구간 밖 시간 고정·지금 게시 등). `details.warnings`를 보여 주고 `confirm: true`로 다시 보낸다
  - `409 conflict`·`busy`·`budget_exceeded`·`x_other_account`·`x_not_connected`·`x_config`, `404 not_found`, `413 too_large`, `503 not_ready`(DB 준비 중)
- **시각**: RFC 3339(UTC). 날짜(`YYYY-MM-DD`)와 시:분은 설정 시간대(기본 Asia/Seoul) 기준
- **금액**: 통화 단위 × 1,000,000 정수(`*_micros`). 모두 앱이 계산한 **추정치**
- 비밀값(토큰·키·비밀번호 해시)은 어떤 응답에도 나오지 않는다

## 상태 확인

| 메서드·경로 | 설명 |
|---|---|
| `GET /healthz` | 프로세스 생존 (`{"status":"ok"}`) |
| `GET /readyz` | DB 연결·마이그레이션 준비 완료 여부 (준비 전 503) |
| `GET /api/runtime` | 실행 환경·목업 여부·실게시 허용·버전 |

## 인증

| 메서드·경로 | 설명 |
|---|---|
| `POST /api/auth/login` | `{username, password}` → 세션 쿠키 + `csrf_token`. 같은 IP·아이디의 반복 실패는 잠시 막는다 |
| `GET /api/auth/session` | 현재 세션 (`authenticated`, `csrf_token`, `expires_at`) |
| `POST /api/auth/logout` | 세션 삭제 |

## 설정·운영

| 메서드·경로 | 설명 |
|---|---|
| `GET /api/settings`, `PUT /api/settings` | 운영 모드·시간대·정책(생성·분할·이미지·보관·예산·요율표). `version`으로 동시 수정 충돌을 막는다 |
| `POST /api/setup/complete` | 첫 설정 완료 표시 (자동 운영은 켜지 않음) |
| `POST /api/automation` | `{automation_enabled?, generation_paused?, publishing_paused?}`. 처음 켤 때 설정에 따라 부족분 채우기를 시작 |
| `GET /api/home` | 홈 요약: 다음 게시, 오늘 묶음·X 게시물 수, 필요한 조치, 자동 운영과 멈춘 이유, 가까운 예약, 재고, 이번 달 사용량 |
| `GET /api/integrations` | X·AI·R2 설정 여부와 X 연결 상태(계정·권한·구독 등급) |
| `GET /api/usage` | 이번 달 분류별 추정 사용량·횟수·예약 중 금액·한도 |

## 콘텐츠

| 메서드·경로 | 설명 |
|---|---|
| `GET /api/contents` | 목록·검색·필터 (`stage`, `q`, `category`, `from`, `to`, `sort`, `limit`, `offset`). 단계별 개수 포함 |
| `POST /api/contents` | 직접 등록 (원문·분할 방식·이미지 방식) |
| `POST /api/contents/batch` | 여러 글 붙여넣기 등록 |
| `POST /api/contents/bulk` | 일괄 보관·보관 해제·카테고리·삭제 |
| `GET /api/contents/categories` | 카테고리 목록 |
| `POST /api/contents/split-preview` | 저장 없이 분할·가중 길이 미리보기 |
| `GET /api/contents/export` | CSV·JSON 내보내기 (수식 주입 방어) |
| `GET /api/contents/import/template` · `POST …/import/parse` · `…/preview` · `…/commit` | 가져오기: 양식 → 필드 연결 → 미리보기 → 등록 |
| `GET /api/contents/{id}`, `PUT /api/contents/{id}` | 상세·자동 저장 (`edit_version`으로 충돌 감지) |
| `POST /api/contents/{id}/approve` · `/reject` | revision 단위 승인·반려 |
| `POST /api/contents/{id}/duplicate` | 복제 |
| `GET /api/contents/{id}/revisions` · `/revisions/{rev}` · `POST …/restore` | 버전 기록·복원 |
| `PUT /api/contents/{id}/parts/{index}/media` | 부분별 이미지 목록·순서 |
| `POST /api/contents/{id}/regenerate` | 다시 만들기 (결과는 비교 후 적용) |
| `GET /api/contents/{id}/generation-results` · `POST …/{rid}/apply` · `…/discard` | 재생성 결과 적용·버리기 |

## 프롬프트

| 메서드·경로 | 설명 |
|---|---|
| `GET /api/prompts`, `POST /api/prompts` | 목록·등록 |
| `POST /api/prompts/example` | 예시 프롬프트 추가 |
| `GET /api/prompts/{id}`, `PUT /api/prompts/{id}` | 상세·수정(본문이 바뀌면 새 버전) |
| `GET /api/prompts/{id}/versions` | 버전 기록 |
| `POST /api/prompts/{id}/active` · `/duplicate` · `/render` | 사용 여부·복제·변수 넣어 미리보기 |

## 생성

| 메서드·경로 | 설명 |
|---|---|
| `POST /api/generation` | 생성 요청 (수량·프롬프트·글/이미지·생성 후 배치). 화면이 만든 고유 키로 중복 방지 |
| `POST /api/generation/estimate` | 예상 사용량·추정 비용 |
| `GET /api/generation` · `GET /api/generation/{id}` · `POST …/{id}/cancel` | 요청 목록·진행·대기 중 취소 |
| `GET /api/generation/plan` | 자동 생성 계획(다음 실행·부족분·멈춘 이유) |
| `POST /api/generation/fill` | 지금 부족분 채우기 |

## 시간표·예약

| 메서드·경로 | 설명 |
|---|---|
| `GET /api/schedule?from=&days=` | 날짜별 예약(구간·수용량·예상 X 게시물 수·시작 못 한 이유·게시 실행 링크) |
| `GET /api/schedule/settings` · `PUT` · `POST …/preview` | 규칙·허용 구간·쉬는 날. 적용 범위와 씨앗이 같으면 미리보기와 적용 결과가 같다 |
| `POST /api/schedule/reservations/{id}/assign` · `unassign` · `move` · `pin` · `unpin` · `skip` | 예약 조작 (규칙 밖이면 409 확인) |
| `POST /api/schedule/swap` | 두 예약 순서 바꾸기 |
| `POST /api/schedule/pinned` | 날짜·시각을 정해 새 고정 예약 |
| `POST /api/schedule/place` | 여러 글을 다음 빈 예약에 배치 |
| `POST /api/schedule/exceptions` · `DELETE …/{date}` | 쉬는 날 추가·해제 |

## X 연결·게시

| 메서드·경로 | 설명 |
|---|---|
| `POST /api/x/oauth/start` | OAuth 2.0 + PKCE 인가 주소 (`{url}`). state는 로그인 세션에 묶이고 10분·1회용 |
| `GET /api/x/oauth/callback` | X가 돌려보내는 주소. 결과를 `/settings/connections?x=connected` 또는 `?x_error=`로 넘긴다 |
| `POST /api/x/check` · `POST /api/x/disconnect` | 연결 확인(게시하지 않음)·해제 |
| `POST /api/publish/now` | `{content_id, confirm}` 지금 게시(수동 예외). 구간 밖·간격·하루 수량 경고는 409 확인 |
| `GET /api/publish/runs` · `GET …/{id}` | 진행 중·확인 필요·최근 실행과 부분별 상태·X 링크 |
| `POST /api/publish/runs/{id}/resolve` | 결과 불명확 부분 정리: `{position, posted, post_ref}` (올라갔으면 주소·ID) |
| `POST /api/publish/runs/{id}/retry` | 실패·불명확 부분부터 다시. 불명확 부분은 `accept_duplicate_risk: true`가 있어야 한다 |
| `POST /api/publish/runs/{id}/abandon` | 남은 부분을 보내지 않고 끝내기 (글은 자동 배치에서 빠짐) |

## 이미지

| 메서드·경로 | 설명 |
|---|---|
| `GET /api/media` · `POST /api/media` | 목록(`usage=all|used|unused|cleaned|failed`)·업로드(검사·EXIF 제거·축소·중복 재사용) |
| `GET /api/media/{id}` · `PATCH` · `DELETE` | 상세·대체 텍스트·삭제(쓰는 글이 없을 때만) |
| `POST /api/media/{id}/crop` | 비율로 잘라 새 이미지 |
| `GET /api/media/{id}/file` · `/download` · `/usage` | 원본·다운로드·쓰인 글 |
| `POST /api/media/{id}/retry-delete` | 정리 실패한 이미지 다시 지우기 |
| `POST /api/media/retention/run` | 보관 기간 정리 지금 실행 (평소 매시간) |
| `GET /api/media/orphans` · `POST …/orphans/clean` | 저장소에만 남은 파일 확인·정리 (앱 접두사 안, 하루 유예) |
| `GET /media/{key...}` | 목업 로컬 저장소의 공개 이미지 (운영은 R2 공개 주소) |

## 알림

| 메서드·경로 | 설명 |
|---|---|
| `GET /api/notifications?unread=1` · `GET …/counts` | 알림함·읽지 않은 수(중대 포함) |
| `POST /api/notifications/{id}/read` · `POST …/read-all` | 읽음 처리 |
