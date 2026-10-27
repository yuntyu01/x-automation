# KoDeploy 배포 안내

KoDeploy 저장소(github.com/kodeploy/kodeploy `38d1275`)의 코드·안내 화면을 읽고 정리한 순서다. KoDeploy에는 **CLI와 앱별 설정 파일이 없고**, 배포는 웹 화면(app.kodeploy.com)에서 한다. 이 문서에 없는 명령·파일을 만들어 쓰지 않는다.

> 현재 상태: **배포 준비 완료 / 실제 KoDeploy 배포는 미검증.** 이 저장소의 `Dockerfile`은 amd64에서 빌드·실행 확인, arm64는 교차 빌드로 이미지 생성과 바이너리 아키텍처 확인까지 했다(에뮬레이터가 없어 arm64 실행은 못 함). KoDeploy 계정 접근은 없었다.

## KoDeploy가 하는 일 (확인한 사실)

| 항목 | 내용 |
|---|---|
| 빌드 | 저장소에서 가장 얕은 `Dockerfile`(이 저장소 루트)을 **BuildKit**으로 빌드. 그 폴더가 빌드 컨텍스트. **빌드 인자 없음** → Dockerfile의 모든 ARG는 기본값으로 동작하게 만들어 두었다 |
| 아키텍처 | 클러스터가 **ARM64**(Ampere). 이미지 베이스(node·golang alpine, distroless static)는 모두 arm64를 제공한다 |
| 실행 | `PORT`(Go 기본 8080)를 주입하고 `0.0.0.0:$PORT`로 받아야 한다. **UID 1000**, 권한 제거, 메모리 256Mi·CPU 300m·임시 디스크 1Gi, replicas 1·Recreate(배포마다 잠깐 끊김). 컨테이너 디스크는 재시작 때 사라진다 |
| 헬스체크 | **TCP 프로브만**(시작 60초 안에 포트가 열려야 함). 앱은 DB가 늦게 떠도 포트를 먼저 열고 `/readyz`로 준비 상태를 따로 알린다 |
| 배포 시점 | **main에 푸시해도 자동 배포되지 않는다.** 푸시한 뒤 화면에서 재배포. 이전 이미지로 되돌리는 기능은 없다(예전 커밋을 다시 푸시해 재배포) |
| 환경변수 | 화면의 "실행 환경 → 환경변수"에 입력 → K8s Secret. 키 50개·값 4,096자 제한. 저장하면 재빌드 없이 재시작 |
| 관리형 DB | PostgreSQL 16, 앱당 DB 하나(5Gi). `DB_HOST·DB_PORT·DB_NAME·DB_USER·DB_PASSWORD`를 주입. 함께 주입되는 `DATABASE_URL`은 SQLAlchemy 형식(`postgresql+psycopg2://`)이라 **앱이 `postgresql://`로 바꿔 쓴다.** `DATABASE_URL`이 없으면 `DB_*`로 연결 문자열을 만든다 |
| 오브젝트 저장소 | 켜면 `kd-{앱}` 버킷과 `S3_*` 값을 주입. 공개 주소가 **r2.dev(속도 제한, 개발용)** 이고 **키가 재배포마다 바뀐다.** 앱은 `R2_*`가 비어 있을 때만 `S3_*`를 쓴다. 운영은 전용 R2 버킷 + 커스텀 도메인 권장 |
| 주소 | 서버 슬롯은 `https://{앱}.kodeploy.com`과 `https://{앱}-api.kodeploy.com`. **X 콜백은 `https://{앱}-api.kodeploy.com/api/x/oauth/callback`** (정적 슬롯을 켜도 바뀌지 않는 주소) |
| 미확인 | `pipeline v2` 사용자는 외부 Helm 차트로 배포돼 프로브·환경 주입이 다를 수 있다(저장소에 없음). 배포 후 화면에서 확인 |

## 배포 순서

### 1. 저장소 연결 확인
- KoDeploy 화면에서 앱을 만들고 GitHub 저장소 `yuntyu01/x-automation`, 브랜치 `main`을 연결한다. 서버 런타임은 Go, 빌드 방식은 자동 감지(`Dockerfile`) 또는 `dockerfile`
- 포트는 기본(8080)을 그대로 둔다. 헬스 경로를 넣는 칸이 있으면 비워 두거나 `/healthz`

### 2. PostgreSQL
- 앱 설정에서 관리형 PostgreSQL을 켠다. 주입되는 값으로 바로 연결되므로 `DATABASE_URL`을 따로 넣지 않는다
- 외부 DB를 쓰려면 `DATABASE_URL=postgres://사용자:비밀번호@호스트:5432/DB?sslmode=require`를 넣는다(사설 IP 대역은 KoDeploy 밖으로 나갈 수 없다)
- 마이그레이션은 시작할 때 자동 적용된다(`MIGRATE_ON_START=true`, advisory lock으로 한 번만, 체크섬이 다르면 시작 거부). 파괴적 마이그레이션은 없다

### 3. 이미지 저장소(R2)
- 권장: Cloudflare에서 전용 버킷(또는 공유 버킷 + 앱 전용 접두사)과 **그 버킷만 쓰기 가능한 API 토큰**, 커스텀 공개 도메인을 만든다
- 환경변수 `R2_ACCOUNT_ID`(또는 `R2_ENDPOINT`), `R2_ACCESS_KEY_ID`, `R2_SECRET_ACCESS_KEY`, `R2_BUCKET`, `R2_PREFIX`(예: `x-automation/`), `R2_PUBLIC_BASE_URL`(https://)
- KoDeploy 오브젝트 저장소만 켜도 동작하지만 r2.dev 주소라 X가 이미지를 받아 가지는 않는다(게시는 앱이 X에 직접 업로드). 화면 미리보기·보관함 속도 제한에 주의
- 이미지는 공개 주소로 저장된다. 아직 게시하지 않은 이미지도 주소를 아는 사람은 볼 수 있다

### 4. 관리자 비밀번호 해시·암호화 키
로컬에서 만든다(값은 저장소에 커밋하지 않는다).

```bash
go run ./cmd/admin hash-password      # 비밀번호 입력 → ADMIN_PASSWORD_HASH
go run ./cmd/admin gen-key            # TOKEN_ENCRYPTION_KEY (한 번 만들고 계속 같은 값)
# Go가 없으면 이미지로: docker run --rm -i --entrypoint /app/admin <이미지> hash-password
```

환경변수 화면에 넣을 값:

| 변수 | 값 |
|---|---|
| `APP_BASE_URL` | `https://{앱}-api.kodeploy.com` (또는 쓰는 커스텀 도메인) |
| `ADMIN_USERNAME` | 관리자 아이디 |
| `ADMIN_PASSWORD_HASH` | 위에서 만든 `$argon2id$…` |
| `TOKEN_ENCRYPTION_KEY` | 위에서 만든 base64 키. **바꾸면 저장된 X 연결을 다시 해야 한다** |
| `TRUSTED_PROXY_CIDRS` | 앞단 프록시 대역을 알면 지정(로그인 시도 제한이 실제 접속 IP를 쓰도록). 모르면 비움 |
| `PUBLISHING_ENABLED` | 처음에는 `false` |
| `INTEGRATION_MODE` | `live` (이미지 기본값). 키가 없다고 목업으로 바뀌지 않는다 |
| `SESSION_TTL`, `LOG_LEVEL` | 선택 (기본 720h, info) |

### 5. 빌드
- 화면에서 배포(빌드)를 실행한다. 빌드 단계: 프론트(Vite) → Go 교차 컴파일 → distroless 실행 이미지(셸 없음 → 웹 터미널은 붙지 않는다)
- 로컬에서 같은 이미지를 미리 확인: `make docker-build PLATFORM=linux/arm64` (또는 `linux/amd64`로 실제 실행까지)

### 6. 배포 확인
- `https://{앱}-api.kodeploy.com/healthz` → `{"status":"ok"}`, `/readyz` → `{"status":"ready"}`
- 로그인 화면이 HTTPS로 뜨고 로그인되는지, 화면 상단에 "목업 모드" 띠가 **없는지**(운영은 live) 확인
- 설정 → 정보에서 실행 환경 `production · 실제 연동`, 실제 게시 허용 `차단`

### 7. X 앱·AI 연결
- X 개발자 콘솔: 앱 종류 **Web App(기밀 클라이언트)**, OAuth 2.0 켬, 콜백 `https://{앱}-api.kodeploy.com/api/x/oauth/callback`, 권한 `tweet.read tweet.write users.read media.write offline.access`
- 환경변수 `X_CLIENT_ID`, `X_CLIENT_SECRET`, `X_REDIRECT_URI`(위 콜백과 정확히 같게) → 저장(재시작)
- 앱 → 설정 → 연결 → **X 계정 연결** → X에서 허용 → 계정·구독 등급·이미지 업로드 권한 확인
- AI: `AI_API_KEY`, `AI_TEXT_MODEL`, `AI_IMAGE_MODEL`(공급자 콘솔에서 쓸 수 있는 모델 이름). 없어도 직접 쓴 글 게시는 된다

### 8. 운영 전 확인
- 프롬프트 등록·기본 프롬프트, 운영 모드, 시간표(구간·하루 수량·간격), 예산·요율표(확인일·출처), 이미지 보관 일수
- 글 하나를 등록해 분할 미리보기(X 게시물 수), 예약 화면에서 배치 확인

### 9. 실제 게시 시작
1. 환경변수 `PUBLISHING_ENABLED=true` → 저장(재시작)
2. 앱 홈 → **자동 운영 시작**(확인 시트). 세 가지(실게시 허용·X 연결·자동 운영)가 모두 있어야 예약 게시가 시작된다
3. 첫 게시가 끝나면 게시 기록에서 본문·답글·X 링크 확인. 자기 답글이 X의 답글 제한(2026-02 변경)에 걸리는지 이때 확인한다(`docs/INTEGRATIONS.md` 미확인 항목)

### 10. 재배포·중지·복구
- 재배포: 코드 푸시 → 화면에서 재배포. Recreate라 잠깐 끊긴다. 진행 중 작업은 종료 신호를 받아 결과를 저장하고, 새 컨테이너가 DB 상태에서 이어 간다. 보내던 중 끊긴 게시는 "결과 확인 필요"로 남고 자동으로 다시 보내지 않는다
- 급히 멈추기: 앱 홈의 게시 일시정지·자동 운영 멈추기(즉시), 또는 `PUBLISHING_ENABLED=false`(재시작)
- 되돌리기: 예전 커밋을 main에 다시 올리고 재배포(이미지 되돌리기 기능 없음). 마이그레이션은 앞으로만 간다 — 되돌린 코드가 새 스키마와 맞는지 확인
- DB 백업·복원, 키 보관은 `docs/OPERATIONS.md`
