# x-automation

1인 운영자가 X 타래(본문 + 자기 답글)를 생성·편집·이미지 준비·예약·게시까지 관리하는, 모바일 앱처럼 쓰는 반응형 웹.
Go 서버 하나가 프론트와 API를 함께 제공하고 PostgreSQL·Cloudflare R2를 쓴다.

> 구현 진행 중이다. 단계별 상태는 [docs/PROGRESS.md](docs/PROGRESS.md), 요구사항은 [docs/BUILD_INSTRUCTIONS.md](docs/BUILD_INSTRUCTIONS.md).

## 빠른 시작

```bash
make setup   # Go 모듈·npm 의존성, .env 생성
make dev     # API(:8080) + Vite(:5173)
make test
make lint
make build   # bin/server (프론트 포함)
```
