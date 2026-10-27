# CLAUDE.md

작업을 시작하거나 세션을 재개하면 아래 두 문서를 먼저 읽는다.

1. `docs/BUILD_INSTRUCTIONS.md` — 전체 구현 지시서(요구사항·14단계·인수 조건). 정본
2. `docs/PROGRESS.md` — 단계별 진행 상태·검증·차단 요인·다음 작업. 미완료 단계부터 이어 간다

## 작업 규칙 요약

- 사용자 화면·오류·도움말은 한국어, 코드 식별자는 영어
- 단계마다 관련 검증 통과 → 진행 기록 갱신 → main 커밋·푸시. 강제 푸시·이력 재작성 금지
- 비밀값(.env, 토큰, 실제 사용자 콘텐츠)은 커밋하지 않는다
- 목업 성공을 실제 X 게시·R2 저장·유료 AI 생성 성공으로 보고하지 않는다
- 실제 게시는 `PUBLISHING_ENABLED=true` + X 연결 + 앱의 "자동 운영 시작"이 모두 필요하다. 기본은 꺼짐
- 구조·결정은 `docs/ARCHITECTURE.md`, 외부 API 확인 내용은 `docs/INTEGRATIONS.md`

## 자주 쓰는 명령

```bash
make setup   # 의존성 설치·개발 설정 안내
make dev     # 로컬 DB + API + Vite 개발 서버
make test    # Go·프론트 테스트
make lint    # go vet·staticcheck·tsc·eslint
make build   # 프론트 빌드 후 Go 바이너리(bin/server)
```
