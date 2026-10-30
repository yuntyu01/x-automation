# 배포 이미지: 프론트 빌드 → Go 빌드(프론트를 바이너리에 포함) → 최소 실행 이미지
# KoDeploy는 저장소 루트의 이 파일을 BuildKit으로, 빌드 인자 없이 빌드한다 (ARM64). 아래 ARG는 모두 기본값으로 동작한다.
# 프론트·Go 빌드 단계는 빌드하는 기계의 아키텍처에서 돌고 Go가 대상 아키텍처로 교차 컴파일한다 (에뮬레이션 불필요).
#   docker build -t x-automation .                                   # 지금 기계 아키텍처
#   docker buildx build --platform linux/arm64,linux/amd64 -t x-automation .
ARG REGISTRY=docker.io/library
ARG RUNTIME_IMAGE=gcr.io/distroless/static-debian12:nonroot

# ── 1. 프론트 (Vite) ───────────────────────────────────────────────────
FROM --platform=$BUILDPLATFORM ${REGISTRY}/node:22.22.0-alpine AS web
# TLS를 가로채는 사내·샌드박스 프록시 뒤에서 빌드할 때만 쓰는 공개 CA 인증서(base64). 비우면 아무 일도 하지 않고, 실행 이미지에는 들어가지 않는다
ARG EXTRA_CA_CERT_B64=""
RUN if [ -n "$EXTRA_CA_CERT_B64" ]; then echo "$EXTRA_CA_CERT_B64" | base64 -d >> /etc/ssl/certs/ca-certificates.crt; fi
ENV NODE_EXTRA_CA_CERTS=/etc/ssl/certs/ca-certificates.crt
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
# 결과물은 ../internal/web/dist 로 나온다 (Go embed 위치)
RUN mkdir -p /src/internal/web/dist && npm run build

# ── 2. 서버 (Go, CGO 없음 → 정적 바이너리) ──────────────────────────────
FROM --platform=$BUILDPLATFORM ${REGISTRY}/golang:1.27.3-alpine AS server
ARG EXTRA_CA_CERT_B64=""
RUN if [ -n "$EXTRA_CA_CERT_B64" ]; then echo "$EXTRA_CA_CERT_B64" | base64 -d >> /etc/ssl/certs/ca-certificates.crt; fi
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY migrations ./migrations
COPY --from=web /src/internal/web/dist ./internal/web/dist
# BuildKit이 주는 대상 플랫폼 (비어 있으면 linux + 빌드하는 기계 아키텍처)
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w -X github.com/yuntyu01/x-automation/internal/app.Version=${VERSION}" -o /out/server ./cmd/server \
 && CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/admin ./cmd/admin \
 && mkdir -p /out/data

# ── 3. 실행 이미지 (셸 없음, CA 인증서 포함, 시간대 데이터는 바이너리에 포함) ──
FROM ${RUNTIME_IMAGE}
WORKDIR /app
COPY --from=server /out/server /app/server
COPY --from=server /out/admin /app/admin
# 개발·목업용 로컬 파일 위치. 운영 이미지는 R2를 쓰고, 컨테이너 디스크에 영속 데이터를 두지 않는다
COPY --from=server --chown=1000:1000 /out/data /data
# GOMEMLIMIT: 메모리 한도(256Mi) 전에 GC가 먼저 돌게 한다. 한도를 늘렸으면 그 80% 정도로 바꾼다
ENV APP_ENV=production \
    GOMEMLIMIT=200MiB \
    PORT=8080 \
    DATA_DIR=/data \
    INTEGRATION_MODE=live \
    PUBLISHING_ENABLED=false
# KoDeploy는 UID 1000으로 실행한다
USER 1000:1000
EXPOSE 8080
# 종료 신호(SIGTERM)는 서버가 직접 받아 진행 중 작업을 저장하고 끝낸다
ENTRYPOINT ["/app/server"]
