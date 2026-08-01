SHELL := /bin/sh

.PHONY: bootstrap dev up down migrate seed ingest score test test-unit test-integration test-e2e lint typecheck format build smoke verify

bootstrap:
	pnpm install --frozen-lockfile
	cd backend && go mod download

dev:
	pnpm --filter @repometeor/web dev

up:
	docker compose up --build

down:
	docker compose down

migrate:
	cd backend && go run ./cmd/migrate up

seed:
	cd backend && go run ./cmd/seed

# Collect one repository immediately. Live mode only.
#   make ingest REPOSITORY=owner/name
ingest:
	@test -n "$(REPOSITORY)" || (echo "usage: make ingest REPOSITORY=owner/name" >&2; exit 1)
	cd backend && go run ./cmd/ingest collect $(REPOSITORY)

# Recompute rankings once and exit. The worker also does this on a schedule;
# this target is the single-shot form used by the smoke test and by operators.
score:
	cd backend && go run ./cmd/ingest score

test: test-unit
	pnpm test

test-unit:
	cd backend && go test ./...

# Integration packages share one database and isolate themselves by truncating
# every table on setup, so they must not run concurrently: Go tests different
# packages in parallel by default, which would let one suite truncate while
# another is mid-seed. -p 1 serialises them.
test-integration:
	@test -n "$(REPOMETEOR_TEST_DATABASE_URL)" || (echo "REPOMETEOR_TEST_DATABASE_URL is required for PostgreSQL integration tests" >&2; exit 1)
	cd backend && go test -tags=integration -p 1 ./internal/repository ./internal/api

test-e2e:
	pnpm test:e2e

lint:
	cd backend && go vet ./...
	pnpm lint
	pnpm exec redocly lint contracts/openapi.yaml

typecheck:
	pnpm typecheck

format:
	cd backend && gofmt -w $$(find . -name '*.go' -type f)
	pnpm format

build:
	cd backend && go build ./cmd/...
	pnpm build

smoke:
	./scripts/smoke.sh

verify: format lint typecheck test test-integration build smoke test-e2e
