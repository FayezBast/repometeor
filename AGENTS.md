# RepoMeteor contributor guide

## Repository map

- `backend/`: Go API, worker, migrations, demo seeder, GitHub provider, scoring, and storage.
- `apps/web/`: Next.js App Router dashboard. Server components are the default.
- `apps/cli/`: publishable TypeScript CLI; it depends only on the public HTTP API.
- `contracts/`: OpenAPI 3.1 contract and response examples.
- `docs/`: architecture, operations, privacy, deployment, scoring, and launch material.
- `scripts/`: smoke and deterministic screenshot helpers.
- `.github/`: CI, security, release, Dependabot, and contribution templates.

Architectural boundaries are deliberate: `internal/scoring` is pure and performs no I/O; HTTP handlers call the service layer; the service layer composes repository operations; only repository/database packages issue SQL. The web and CLI consume the public API and must not connect to PostgreSQL.

## Environment

Use Go 1.25+, Node 24+, pnpm 10.30.1, Docker 29+/Compose 5+, and PostgreSQL 16+. Copy `.env.example` to `.env` for Compose. Demo mode is the default and must never contact GitHub. Live mode requires `DEMO_MODE=false`, `NEXT_PUBLIC_DEMO_MODE=false`, and `GITHUB_TOKEN`.

Never commit `.env`, tokens, database credentials, production URLs, real IP addresses, or downloaded GitHub response bodies. Never call GitHub stargazer-list endpoints, collect individual stargazer identities, or scrape GitHub Trending HTML.

## Commands

Run from the repository root:

- `make bootstrap`: frozen pnpm install and Go module download.
- `make up` / `make down`: start or stop the local Compose stack.
- `make migrate`, `make seed`, `make score`: database lifecycle and scoring.
- `make format`, `make lint`, `make typecheck`, `make test`, `make build`: normal quality gates.
- `make test-integration`: PostgreSQL-backed tests; set `REPOMETEOR_TEST_DATABASE_URL`.
- `make test-e2e`: Playwright tests; the demo stack must be running.
- `make smoke`: smoke the URLs configured by `WEB_SMOKE_URL` and `API_SMOKE_URL`.
- `make verify`: the complete local gate.

If the host sandbox blocks the default Go cache, set `GOCACHE=/tmp/repometeor-go-cache`.

## Style and change rules

Format Go with `gofmt` and TypeScript/CSS/Markdown with Prettier. Keep TypeScript strict, SQL parameterized, API payloads bounded, metrics low-cardinality, timestamps UTC, and errors actionable without leaking secrets. Use an injected clock for time-sensitive backend logic. Add forward migrations; never edit an applied migration without an explicit compatibility plan.

Update `contracts/openapi.yaml` and examples whenever a public response changes. Bump `scoring.FormulaVersion` and update `docs/SCORING.md`, `docs/DECISIONS.md`, and tests when scoring behavior changes. `pnpm-lock.yaml`, Next build output, coverage output, screenshots, and generated OpenAPI artifacts are generated; do not hand-edit build output.

## Definition of done

A change is done when the relevant formatters, linters, unit tests, integration tests, builds, and smoke tests pass; docs and OpenAPI match behavior; demo mode remains visibly labeled and network-isolated; accessibility and security implications are covered; and no placeholder production integration or critical TODO remains.
