# RepoMeteor — Implementation Tasks

Living checklist. Updated as phases complete.

Legend: `[ ]` todo · `[x]` done · `[~]` done with a documented limitation

---

## Phase 0 — Inspect and plan

- [x] Inspect repository (empty git repo on `master`, no commits)
- [x] Record environment versions (Go 1.25.6, Node 26.4.0, npm 11.17.0, pnpm 10.30.1,
      Docker 29.1.3 / Compose v5.0.1, local PostgreSQL 14.19)
- [x] Create `TASKS.md`
- [x] Create `docs/DECISIONS.md`
- [x] Create root `AGENTS.md`

## Phase 1 — Foundation

- [ ] pnpm workspace (`pnpm-workspace.yaml`, root `package.json`)
- [ ] Go module (`backend/go.mod`) with pinned dependencies
- [ ] Typed, validated configuration package (`backend/internal/config`)
- [ ] Injectable clock abstraction (`backend/internal/clock`)
- [ ] Structured logging + secret redaction (`backend/internal/telemetry`)
- [ ] `Makefile` with every documented target
- [ ] `compose.yaml` (postgres, migrate, api, worker, web)
- [ ] `.env.example`, `.editorconfig`, `.gitignore`
- [ ] API / worker binaries start, bind, and shut down gracefully
- [ ] Root documentation set (README, LICENSE, NOTICE, CONTRIBUTING, …)

## Phase 2 — Database

- [ ] Forward SQL migrations + embedded migration runner with advisory lock
- [ ] `repositories` table, constraints, indexes
- [ ] `repository_snapshots` table, idempotent bucket upsert
- [ ] `momentum_scores` table + windows/constraints
- [ ] `crawl_jobs` table + partial unique dedupe index
- [ ] `discovery_runs` table
- [ ] `github_request_cache` table
- [ ] `repository_aliases` table (rename/redirect handling)
- [ ] `schema_migrations` bookkeeping + rollback strategy doc
- [ ] Data-access layer (`internal/database`, `internal/repository`)
- [ ] Job queue with `FOR UPDATE SKIP LOCKED`, retries, dead-letter, stale-lock recovery
- [ ] Deterministic demo seeder (50+ fictional repositories)
- [ ] Integration tests (build-tagged `integration`)

## Phase 3 — GitHub provider

- [ ] `Provider` interface
- [ ] Fixture provider (no network) used by demo mode and tests
- [ ] Live REST client (auth, UA, API version, accept header, deadlines)
- [ ] Conditional requests (ETag / Last-Modified / 304) + request cache
- [ ] Error classification (14 classes)
- [ ] Retry with exponential backoff + jitter + `Retry-After`
- [ ] Rate-budget manager (core / search, reserve for user submissions)
- [ ] Partitioned search discovery (no HTML scraping)
- [ ] Snapshot collection job + tier scheduling
- [ ] Missing / renamed / archived handling
- [ ] Unit tests against `httptest` fixtures

## Phase 4 — Scoring

- [ ] Pure `internal/scoring` package (no HTTP, no DB)
- [ ] Window baselines with tolerance, midpoint acceleration
- [ ] Features: velocity, relative growth, acceleration, forks, activity, freshness
- [ ] Cohort normalization: `log1p`, winsorization, deterministic percentile ranks
- [ ] `momentum-v1` formula + confidence adjustment + clamping
- [ ] Confidence model and labels
- [ ] Eligibility rules
- [ ] Collections (rising / daily / weekly / hidden-gems / new-entrants / established)
- [ ] Anomaly flags (neutral wording)
- [ ] Deterministic explanations
- [ ] Persistence + ranking + tier recalculation
- [ ] `docs/SCORING.md`
- [ ] Extensive unit tests

## Phase 5 — API

- [ ] `/healthz`, `/readyz`, `/metrics`
- [ ] `GET /v1/trending` (filters, cursor pagination, ETag, cache headers)
- [ ] `GET /v1/search`
- [ ] `GET /v1/repositories/{owner}/{repo}`
- [ ] `GET /v1/repositories/{owner}/{repo}/history`
- [ ] `GET /v1/compare`
- [ ] `POST /v1/repositories/track`
- [ ] `GET /v1/languages`, `GET /v1/topics`
- [ ] `GET /v1/methodology`
- [ ] `GET /v1/badges/{owner}/{repo}.svg`
- [ ] `GET /v1/feeds/trending.xml`
- [ ] `application/problem+json` errors, request IDs, rate limiting, CORS
- [ ] `contracts/openapi.yaml` (OpenAPI 3.1) + examples
- [ ] API tests via `httptest`

## Phase 6 — Web

- [ ] Design system (tokens, dark/light, reduced motion)
- [ ] Layout, header, footer, skip link, demo banner
- [ ] `/` home
- [ ] `/explore`
- [ ] `/r/[owner]/[repository]`
- [ ] `/compare`
- [ ] `/methodology`
- [ ] `/docs/api`
- [ ] `/about`
- [ ] `/submit`
- [ ] `robots.txt`, `sitemap.xml`, `404`, global error page
- [ ] SEO metadata, Open Graph, dynamic share images
- [ ] Typed API client with Zod validation
- [ ] Vitest + React Testing Library tests
- [ ] Playwright E2E + `@axe-core/playwright` accessibility checks

## Phase 7 — CLI

- [ ] `trending`, `inspect`, `track`, `compare`, `config`
- [ ] Global options and env precedence
- [ ] Human table output + `--json` + `--no-color`
- [ ] Exit codes, stderr/stdout separation, friendly errors
- [ ] Tests against a local fake API server
- [ ] `tsup` build + publishable package metadata

## Phase 8 — Operations

- [ ] Structured logs, request/job correlation
- [ ] Prometheus metrics (low cardinality)
- [ ] Graceful shutdown for API and worker
- [ ] Multi-stage, non-root Dockerfiles (api, worker, web)
- [ ] GitHub Actions: CI, security, release
- [ ] Dependabot
- [ ] `docs/RUNBOOK.md`, `docs/DEPLOYMENT.md`, `docs/SELF_HOSTING.md`

## Phase 9 — Open-source launch quality

- [ ] Polished `README.md` with Mermaid architecture diagram
- [ ] Brand mark SVG + favicon
- [ ] `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md`, `ROADMAP.md`, `CHANGELOG.md`
- [ ] Issue templates + PR template
- [ ] `docs/GOOD_FIRST_ISSUES.md` (12 issues)
- [ ] `docs/LAUNCH.md` with prelaunch checklist and launch assets
- [ ] Screenshot capture script (`scripts/capture-screenshots.ts`)
- [ ] Screenshots captured into `docs/assets`

## Phase 10 — Final verification

- [ ] `make format`
- [ ] `make lint`
- [ ] `make test-unit`
- [ ] `make test-integration`
- [ ] `make test` (web + CLI unit tests)
- [ ] `make build`
- [ ] `make smoke`
- [ ] `make test-e2e`
- [ ] `make verify`
- [ ] `docker compose up --build` fresh-start — see `docs/DECISIONS.md` §Environment
      for the exact command, result, and manual verification steps.
