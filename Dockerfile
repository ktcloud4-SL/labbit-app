# 1. 빌드 스테이지
FROM golang:alpine AS builder

ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64 \
    GOTOOLCHAIN=auto

WORKDIR /app

# 의존성 다운로드 캐싱
COPY go.mod go.sum ./
RUN go mod download

# 소스 복사 및 순수 정적 바이너리 빌드 (디버그 심볼 제거)
COPY . .
RUN go build -ldflags="-s -w" -o /out/labbit-server ./cmd/labbit-server && \
    go build -ldflags="-s -w" -o /out/labbit-migrate ./cmd/labbit-migrate


# 2. 실행 스테이지
FROM alpine:3.21 AS runner

# SSL 인증서 및 타임존 설치
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

# 빌드 결과물 및 마이그레이션 SQL 복사
COPY --from=builder /out/labbit-server /app/labbit-server
COPY --from=builder /out/labbit-migrate /app/labbit-migrate
COPY --from=builder /app/db /app/db

# 보안용 non-root 계정 설정
RUN addgroup -S appgroup && adduser -S appuser -G appgroup
USER appuser

# 포트 오픈 및 기본 진입점 설정
EXPOSE 8080 9090
ENTRYPOINT ["/app/labbit-server"]