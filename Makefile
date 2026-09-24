GO ?= go
NPM ?= npm

.PHONY: setup test go-fmt-check go-vet go-test go-build web-install web-typecheck web-lint web-test web-build server connector web

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

# Runtime Contract의 필수 값을 개발용으로 주입한다.
server:
	LABBIT_ENVIRONMENT=development \
	LABBIT_RUNTIME_ROLES=api \
	$(GO) run ./cmd/labbit-server

# Connector 기능은 아직 스켈레톤이며 public inbound listener를 열지 않는다.
connector:
	LABBIT_ENVIRONMENT=development \
	LABBIT_CONNECTOR_ID=dev-connector \
	$(GO) run ./cmd/labbit-connector

web:
	cd web && $(NPM) run dev
