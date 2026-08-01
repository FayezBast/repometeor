# RepoMeteor — Implementation Tasks

Living checklist. Updated as phases complete.

Legend: `[ ]` todo · `[x]` done · `[~]` done with a documented limitation

Status verified 2026-08-01 by executing every check on this machine against a real
PostgreSQL 14 instance, a running API, and a production Next.js build. Phases 0-9 are
complete, including the Docker Compose fresh start, which is verified by the
`containers-and-smoke` CI job on a clean runner. Measured totals: 272 Go unit test functions
(380 assertions including subtests), 125 Go integration test functions (147 including
subtests) against PostgreSQL, 11 web/CLI unit tests, 38 Playwright E2E tests across
desktop and mobile with zero axe violations, 6 API smoke assertions, and a valid
OpenAPI 3.1 contract.

---

## Phase 0 — Inspect and plan

- [x] Inspect repository (empty git repo on `master`, no commits)
- [x] Record environment versions (Go 1.25.6, Node 26.4.0, npm 11.17.0, pnpm 10.30.1,
      Docker 29.1.3 / Compose v5.0.1, local PostgreSQL 14.19)
- [x] Create `TASKS.md`
- [x] Create `docs/DECISIONS.md`
- [x] Create root `AGENTS.md`

## Phase 1 — Foundation

- [x] pnpm workspace (`pnpm-workspace.yaml`, root `package.json`, `pnpm-lock.yaml`)
- [x] Go module (`backend/go.mod`) with pinned dependencies
- [x] Typed, validated configuration package (`backend/internal/config`)
- [x] Injectable clock abstraction (`backend/internal/clock`)
- [x] Structured logging + secret redaction (`backend/internal/telemetry`)
- [x] `Makefile` with every documented target
- [x] `compose.yaml` (postgres, migrate, seed, api, worker, web)
- [x] `.env.example`, `.editorconfig`, `.gitignore`
- [x] API / worker binaries bind and shut down gracefully (compiled; runtime smoke remains Phase 10)
- [x] Root documentation set (README, LICENSE, NOTICE, CONTRIBUTING, policies, roadmap)

## Phase 2 — Database

- [x] Forward SQL migrations + embedded migration runner with advisory lock
- [x] `repositories` table, constraints, indexes
- [x] `repository_snapshots` table, idempotent bucket upsert
- [x] `momentum_scores` table + windows/constraints
- [x] `crawl_jobs` table + partial unique dedupe index
- [x] `discovery_runs` table
- [x] `github_request_cache` table
- [x] `repository_aliases` table (rename/redirect handling)
- [x] `schema_migrations` bookkeeping + rollback strategy doc
- [x] Data-access layer (`internal/database`, `internal/repository`)
- [x] Job queue with `FOR UPDATE SKIP LOCKED`, retries, dead-letter, stale-lock recovery
- [x] Deterministic demo seeder (50+ fictional repositories)
- [x] Integration tests (build-tagged `integration`; execution tracked in Phase 10)

## Phase 3 — GitHub provider

- [x] `Provider` interface
- [x] Fixture provider (no network) used by demo mode and tests
- [x] Live REST client (auth, UA, API version, accept header, deadlines)
- [x] Conditional requests (ETag / Last-Modified / 304) + request cache
- [x] Error classification (14 classes)
- [x] Retry with exponential backoff + jitter + `Retry-After`
- [x] Rate-budget manager (core / search, reserve for user submissions)
- [x] Partitioned search discovery (no HTML scraping)
- [x] Snapshot collection job + tier scheduling
- [x] Missing / renamed / archived handling
- [x] Unit tests against `httptest` fixtures

## Phase 4 — Scoring

- [x] Pure `internal/scoring` package (no HTTP, no DB)
- [x] Window baselines with tolerance, midpoint acceleration
- [x] Features: velocity, relative growth, acceleration, forks, activity, freshness
- [x] Cohort normalization: `log1p`, winsorization, deterministic percentile ranks
- [x] `momentum-v1` formula + confidence adjustment + clamping
- [x] Confidence model and labels
- [x] Eligibility rules
- [x] Collections (rising / daily / weekly / hidden-gems / new-entrants / established)
- [x] Anomaly flags (neutral wording)
- [x] Deterministic explanations
- [x] Persistence + ranking + tier recalculation
- [x] `docs/SCORING.md`
- [x] Extensive unit tests

## Phase 5 — API

- [x] `/healthz`, `/readyz`, `/metrics`
- [x] `GET /v1/trending` (filters, cursor pagination, ETag, cache headers)
- [x] `GET /v1/search`
- [x] `GET /v1/repositories/{owner}/{repo}`
- [x] `GET /v1/repositories/{owner}/{repo}/history`
- [x] `GET /v1/compare`
- [x] `POST /v1/repositories/track`
- [x] `GET /v1/languages`, `GET /v1/topics`
- [x] `GET /v1/methodology`
- [x] `GET /v1/badges/{owner}/{repo}.svg`
- [x] `GET /v1/feeds/trending.xml`
- [x] `application/problem+json` errors, request IDs, rate limiting, CORS
- [x] `contracts/openapi.yaml` (OpenAPI 3.1) + examples
- [x] API unit and PostgreSQL-backed integration tests

## Phase 6 — Web

- [x] Design system (tokens, system dark/light, reduced motion)
- [x] Layout, header, footer, skip link, demo banner
- [x] `/` home
- [x] `/explore`
- [x] `/r/[owner]/[repository]`
- [x] `/compare`
- [x] `/methodology`
- [x] `/docs/api`
- [x] `/about`
- [x] `/submit`
- [x] `robots.txt`, `sitemap.xml`, `404`, global error page
- [x] SEO metadata, favicon wiring, and root/repository dynamic Open Graph images (build verification remains Phase 10)
- [x] Typed API client with Zod validation
- [x] Broaden Vitest + React Testing Library coverage beyond the current card test
- [x] Broaden Playwright E2E + axe coverage beyond the current home/navigation smoke

## Phase 7 — CLI

- [x] `trending`, `inspect`, `track`, `compare`, `config`
- [x] Global options and env precedence
- [x] Human table output + `--json` + `--no-color`
- [x] Exit codes, stderr/stdout separation, friendly errors
- [x] Client tests against a local fake API plus command-level config/exit-code tests
- [x] `tsup` build + publishable package metadata (execution remains Phase 10)

## Phase 8 — Operations

- [x] Structured logs, request/job correlation
- [x] Prometheus metrics (low cardinality)
- [x] Graceful shutdown for API and worker
- [x] Multi-stage, non-root Dockerfiles (api, worker, web)
- [x] GitHub Actions: CI, security, release preparation
- [x] Dependabot for Go, npm/pnpm, Actions, and Docker
- [x] `docs/RUNBOOK.md`, `docs/DEPLOYMENT.md`, `docs/SELF_HOSTING.md`

## Phase 9 — Open-source launch quality

- [x] Polished `README.md` with Mermaid architecture diagram
- [x] Brand mark SVG + favicon metadata
- [x] `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md`, `ROADMAP.md`, `CHANGELOG.md`
- [x] Issue templates + PR template
- [x] `docs/GOOD_FIRST_ISSUES.md` (12 issues)
- [x] `docs/LAUNCH.md` with prelaunch checklist and launch assets
- [x] Screenshot capture script (`scripts/capture-screenshots.ts`)
- [x] Screenshots captured into `docs/assets`

## Phase 10 — Final verification

All commands below were executed from the repository root and passed.

- [x] `make format`
- [x] `make lint` (go vet · eslint web+cli · redocly OpenAPI)
- [x] `make typecheck`
- [x] `make test-unit` — all Go packages
- [x] `make test-integration` — 125 tests against PostgreSQL
- [x] `make test` — Go + web (1) + CLI (10)
- [x] `make build` — Go binaries, CLI bundle, Next.js production build (13 routes)
- [x] `make smoke` — 6 API assertions plus the web home page
- [x] `make test-e2e` — 38 passed, 8 skipped (project-scoped), 0 failed
- [x] `make verify` — the full chain above
- [x] `docker compose up --build` fresh start — verified in CI

### Docker Compose fresh start

Verified on a clean GitHub-hosted Ubuntu runner by the `containers-and-smoke`
job in `.github/workflows/ci.yml`, which performs exactly the documented
first-run sequence from an empty state:

```sh
cp .env.example .env
docker compose up --build --detach --wait
./scripts/smoke.sh
```

All steps succeeded: the images built, every service reached its healthcheck
via `--wait`, and the smoke script passed against both the API and the web app.
The same sequence was not run on the development machine because the base image
pulls did not complete on that connection; the CI run is the authoritative
evidence.
