GO ?= go
NPM ?= npm
COMPOSE ?= docker compose

# compose.yaml의 로컬 폐기용 dummy credential과 같은 값이다. 실제 Secret이 아니다.
LABBIT_DEV_DB_PORT ?= 5432
LABBIT_DEV_DATABASE_DSN ?= postgres://labbit:labbit-local-dummy@127.0.0.1:$(LABBIT_DEV_DB_PORT)/labbit?sslmode=disable
# Browser가 실제로 보는 origin(Vite dev server)이다. unsafe method Origin 검증의 trusted origin으로 사용한다.
LABBIT_DEV_PUBLIC_ORIGIN ?= http://localhost:5173
# Integration Test는 이 server에 test별 database를 만들고 삭제한다. 개발 DB(labbit) 상태에 의존하지 않는다.
LABBIT_TEST_DATABASE_DSN ?= postgres://labbit:labbit-local-dummy@127.0.0.1:$(LABBIT_DEV_DB_PORT)/postgres?sslmode=disable
# `//go:build integration` test가 있는 package만 둔다. 일반 unit test 전체는 go-test가 한 번만 실행한다.
GO_INTEGRATION_PACKAGES ?= ./internal/postgres ./internal/postgres/postgrestest ./internal/server/app ./internal/server/auth ./internal/server/httpapi

.PHONY: setup test go-fmt-check go-vet go-test go-build go-integration-test web-install web-typecheck web-lint web-test web-build dev-db-up dev-db-down dev-db-migrate server connector web

# 개발 시작 전에 필요한 최소 의존성을 설치한다.
setup: web-install
	$(GO) mod download

# 저장소 전체의 기본 검증 진입점이다.
test: go-fmt-check go-vet go-test go-build web-typecheck web-lint web-test web-build

go-fmt-check:
	@files="$$(gofmt -l $$(find . -type f -name '*.go' -not -path './vendor/*'))"; \
	if [ -n "$$files" ]; then \
		echo "gofmt가 필요한 파일:"; \
		echo "$$files"; \
		exit 1; \
	fi

go-vet:
	$(GO) vet ./...

go-test:
	$(GO) test ./...

go-build:
	$(GO) build ./...

# 실제 PostgreSQL이 필요한 명시적 entrypoint다. DB가 준비되지 않았으면 skip하지 않고 실패한다.
go-integration-test:
	$(GO) vet -tags integration $(GO_INTEGRATION_PACKAGES)
	LABBIT_TEST_DATABASE_DSN='$(LABBIT_TEST_DATABASE_DSN)' $(GO) test -tags integration -count=1 $(GO_INTEGRATION_PACKAGES)

web-install:
	cd web && $(NPM) ci

web-typecheck:
	cd web && $(NPM) run typecheck

web-lint:
	cd web && $(NPM) run lint

web-test:
	cd web && $(NPM) run test

web-build:
	cd web && $(NPM) run build

# Local PostgreSQL 16을 시작하고 healthcheck 통과까지 기다린다.
dev-db-up:
	LABBIT_DEV_DB_PORT=$(LABBIT_DEV_DB_PORT) $(COMPOSE) up -d --wait postgres

# container만 내리고 개발 DB volume은 유지한다.
dev-db-down:
	$(COMPOSE) down

# Application startup이 아닌 별도 runner로 개발 DB에 Migration을 적용한다.
dev-db-migrate:
	LABBIT_ENVIRONMENT=development \
	LABBIT_DATABASE_DSN='$(LABBIT_DEV_DATABASE_DSN)' \
	$(GO) run ./cmd/labbit-migrate up

# Runtime Contract의 필수 값을 개발용으로 주입한다.
# api role의 /readyz는 dev-db-up, dev-db-migrate 이후 성공한다.
server:
	LABBIT_ENVIRONMENT=development \
	LABBIT_RUNTIME_ROLES=api \
	LABBIT_DATABASE_DSN='$(LABBIT_DEV_DATABASE_DSN)' \
	LABBIT_PUBLIC_ORIGIN='$(LABBIT_DEV_PUBLIC_ORIGIN)' \
	$(GO) run ./cmd/labbit-server

# Connector 기능은 아직 스켈레톤이며 public inbound listener를 열지 않는다.
connector:
	LABBIT_ENVIRONMENT=development \
	LABBIT_CONNECTOR_ID=dev-connector \
	$(GO) run ./cmd/labbit-connector

web:
	cd web && $(NPM) run dev
