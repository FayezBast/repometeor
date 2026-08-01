# Architectural decisions

This log records decisions that materially affect RepoMeteor. New entries are appended; changed decisions should explain the migration path.

## 2026-08-01 — Monorepo and service boundaries

RepoMeteor uses one repository with a Go module under `backend` and a pnpm workspace under `apps`. The API and worker are separate processes sharing PostgreSQL. The CLI is independent of the web app and both consume the public API. This keeps release surfaces separable without introducing microservices or Redis.

## 2026-08-01 — Aggregate snapshots only

The collector stores repository-level counts and metadata from supported GitHub REST endpoints. It does not call stargazer-list endpoints, retain individual identities, or infer historical timestamps GitHub does not provide. Momentum is computed only from RepoMeteor's own periodic snapshots. This trades instant history for a simpler, privacy-conscious, auditable system.

## 2026-08-01 — PostgreSQL as the only stateful dependency

PostgreSQL 16 stores metadata, snapshots, scores, conditional-request validators, discovery audit rows, rate-limit counters, and the durable job queue. Jobs use `FOR UPDATE SKIP LOCKED`, partial unique deduplication, bounded retries, dead-letter state, and stale-lock recovery. Avoiding Redis reduces operational burden and keeps transactional state consistent.

## 2026-08-01 — Stable GitHub identity and aliases

Repositories are keyed by GitHub's numeric ID. Renames update the canonical name and create a case-insensitive alias, preserving old detail URLs and history. Repeated missing responses move a repository to `missing` rather than deleting it.

## 2026-08-01 — Idempotent UTC snapshot buckets

Snapshots are unique on `(repository_id, bucket_start)`. The application computes the UTC bucket using the configured interval, so retrying collection inside a bucket updates the aggregate observation rather than duplicating it.

## 2026-08-01 — Deterministic `momentum-v1`

Scoring lives in a pure package. It uses 6-hour, 24-hour, and 7-day windows, cohort percentile normalization, bounded outliers, explicit feature weights, confidence adjustment, deterministic tie breaking, neutral anomaly flags, and stored explanations. Provisional repositories are normalized against—but do not alter—the fully observed cohort. See `SCORING.md`.

## 2026-08-01 — Demo mode is network-isolated

Demo mode seeds deterministic fictional repositories and selects the fixture GitHub provider. The worker does not start the live collection dispatcher. The API and UI report `demoMode`; the UI displays a permanent demo label. This is stronger than relying only on an empty token.

## 2026-08-01 — Migrations and rollback

Forward migrations are embedded into the API image and serialized with a PostgreSQL advisory lock. Down migrations are retained for local recovery, but production rollback should restore a tested database backup and redeploy the previous application version when a migration is destructive. Never run a down migration against production without reviewing data loss.

## 2026-08-01 — Public rate limiting

Read limits are in-process and per API instance because they protect bounded database reads. Tracking submissions are durable PostgreSQL counters keyed by an HMAC of the client address so limits survive restarts without storing raw IP addresses. Multi-instance production should configure a shared `RATE_LIMIT_KEY`.

## 2026-08-01 — Toolchain compatibility over newest-number wins

Dependencies remain exactly pinned, but versions must satisfy the complete toolchain. TypeScript 6.0.3 and ESLint 9.39.5 are used because the current Next.js lint dependencies do not support TypeScript 7 or ESLint 10. The original incompatible pins were corrected after lockfile peer validation.

## 2026-08-01 — Defects found and fixed during full-stack verification

Running the whole pipeline against a live stack surfaced defects that no
single-layer check would have caught. Recorded here because each was a real
behavioural bug, not a test artefact.

**HEAD returned 405 on read routes.** Gin does not derive HEAD from GET.
Badges are fetched by GitHub's image proxy, and `scripts/smoke.sh` probes the
badge and feed with HEAD, so every embedded badge would have failed to
revalidate. All read routes are now registered for both methods via a `get()`
helper.

**Retry-After was computed from the wall clock.** The 429 handlers used
`time.Until` while the limiters used the injectable clock. When the two
disagreed the header was non-positive and therefore omitted entirely, leaving a
throttled client with no guidance. Both sites now use the service clock through
`retryAfterSeconds`, which floors at one second.

**`UPDATE ... RETURNING` row order is unspecified.** The job queue's claim
query ordered its subquery by priority, but PostgreSQL gives no ordering
guarantee for the returned rows, so a high-priority job could be executed last
within its batch. The claimed slice is now sorted in Go.

**Momentum cards omitted their explanation.** The API returned `reasons` and
`flags`; the card rendered neither, along with the last-push time. A ranking
without its "why" undercuts the product's central claim, so the card now shows
the primary explanation, a text-carried anomaly notice, and the push recency.

**Mobile navigation was unreachable.** Below 980px the header nav was
`display: none` with no replacement, so mobile visitors had no route to
Explore, Compare, Methodology or the API docs. The header now wraps to a second
row; no JavaScript disclosure menu was introduced.

**Two accessibility violations.** Explore jumped from `h1` straight to the
cards' `h3` (`heading-order`), and the badge snippet was a scrollable `<pre>`
that could not receive keyboard focus (`scrollable-region-focusable`, serious).
Both are fixed and covered by the axe sweep.

**JSX was constructed inside try/catch.** All 195 web lint errors were one
rule. Pages now load data through `lib/load.ts`, which returns a result, and
render outside the catch — so a rendering bug can no longer be swallowed by the
error path intended for the fetch.

**Makefile targets did not match the binaries.** `make score` invoked
`cmd/worker --once=score`, a flag the worker does not parse, so it started the
long-running worker and never exited; `make ingest` passed a bare repository
name where `cmd/ingest` expects a subcommand. Both now call `cmd/ingest`
correctly.

**Integration packages raced.** `go test` runs packages in parallel, and both
integration suites truncate the same database, producing deadlocks and foreign
key violations. `make test-integration` now passes `-p 1`.

**The Open Graph image fetched a font at build time.** A decorative `✦` is
absent from the default font, so Satori attempted a network download that
failed with HTTP 400 and left the glyph blank. The mark is now an inline SVG.

**Vitest collected the Playwright specs.** `apps/web` ran its E2E files under
Vitest and failed on Playwright's `test()`. The Vitest config now scopes
collection and excludes `tests/e2e`.

## 2026-08-01 — Playwright runs on Chromium only

Both projects use `channel: "chromium"`, and the mobile project uses the Pixel 5
descriptor rather than an iPhone. The mobile assertions concern viewport
behaviour — layout, overflow, reachability — which Chromium exercises
faithfully, so requiring WebKit would add a second engine download to every CI
run for no additional signal. `channel: "chromium"` also avoids the separate
chrome-headless-shell artefact, leaving `playwright install chromium` as the
only download the suite needs. Add a WebKit project when a test genuinely
depends on Safari rendering.

## 2026-08-01 — CodeQL and dependency review require a public repo or GHAS

Code scanning uploads and dependency review need GitHub Advanced Security on a
private repository. On the first push to a private repo the CodeQL analysis ran
to completion and then failed at the upload step with "Resource not accessible
by integration", turning the Security workflow permanently red for a reason
that has nothing to do with the code.

Both jobs are therefore gated on `github.event.repository.private == false`.
They skip cleanly while the repository is private and run in full the moment it
becomes public or GHAS is enabled. `govulncheck` and `pnpm audit` continue to
run in every configuration as advisory, non-blocking reports.

## 2026-08-01 — Canonical location is the maintainer's personal account

The Go module path declared `github.com/repometeor/repometeor/backend`, but no
`repometeor` user or organization exists on GitHub and the repository actually
lives at `github.com/FayezBast/repometeor`. In Go the module path is the fetch
URL, so `go install github.com/repometeor/repometeor/backend/cmd/api@latest`
would have returned 404 for every user, even though building from a clone
worked fine and hid the problem.

Rather than register an organization to match the aspirational path, the module
path was rewritten to `github.com/FayezBast/repometeor/backend` — 116 lines
across 37 files, plus the `NEXT_PUBLIC_GITHUB_REPO_URL` default, which pointed
at the same nonexistent repository and rendered a dead "Source" link in the
footer of the demo stack.

This was settled before going public deliberately: once the module is public and
imported, changing its path is a breaking change for every consumer. Moving to an
organization later remains possible at the cost of a second rewrite.

## Environment verification status

Verified 2026-08-01 on macOS 15 (arm64) with Go 1.25.6, Node 26.4.0, pnpm
10.30.1 and a local PostgreSQL 14.19 instance. Every stage of `make verify`
passes: format, lint (go vet, ESLint for web and CLI, Redocly OpenAPI),
typecheck, Go unit tests, 125 Go integration tests against PostgreSQL, web and
CLI unit tests, Go/CLI/Next.js builds, the six-assertion API smoke test plus the
web home page, and 38 Playwright E2E tests across desktop and mobile with zero
axe violations.

Two notes on the environment rather than the code:

- **PostgreSQL 14, not 16.** The local server available for integration testing
  is 14.19, while `compose.yaml` pins `postgres:16.10-alpine`. The schema uses
  nothing newer than PostgreSQL 12 semantics — `FOR UPDATE SKIP LOCKED`,
  partial unique indexes, expression indexes, `jsonb`, arrays, `DISTINCT ON` —
  so it runs unchanged on both. Integration coverage is therefore genuine, and
  the 16 pin remains the supported deployment target.

- **Docker Compose fresh start verified in CI, not locally.** The base image
  pulls did not complete on the development connection, so the stack was never
  started there. It is verified instead by the `containers-and-smoke` CI job,
  which runs `cp .env.example .env`, `docker compose up --build --detach --wait`
  and `./scripts/smoke.sh` from an empty state on a clean runner. That job
  passed on the first push, so the documented first-run sequence is confirmed
  working end to end on a machine that had never seen the project.
