# Production deployment

RepoMeteor deploys as three stateless images—API, worker, web—plus PostgreSQL 16. Build every image from one tag. The API and web need inbound HTTP; the worker is a persistent process with no public port.

## Generic container platform

1. Provision PostgreSQL 16 with encrypted storage, automatic backups, connection limits, and a tested restore path.
2. Run the migration binary once as a release job using the same API image and `migrate up` command. Never run migrations concurrently from every replica.
3. Deploy API on port 8080 with `/healthz` liveness and `/readyz` readiness. Keep `/metrics` internal or protect it at the edge.
4. Deploy one or more workers from the worker image. Keep at least one persistent instance; do not configure scale-to-zero.
5. Build/deploy web on port 3000. `NEXT_PUBLIC_*` values are build-visible and contain no secrets.
6. Configure TLS, exact CORS origins, request/body limits, logs/metrics, alerts, and backup verification.

Minimum production settings include `APP_ENV=production`, `DEMO_MODE=false`, `DATABASE_URL`, `GITHUB_TOKEN`, `API_PUBLIC_URL`, `WEB_PUBLIC_URL`, `NEXT_PUBLIC_API_URL`, `INTERNAL_API_URL`, `CORS_ALLOWED_ORIGINS`, and a strong shared `RATE_LIMIT_KEY`. Set `TRUSTED_PROXY_CIDRS` only to the platform proxy networks; when left empty, forwarded headers are ignored.

Never expose the GitHub token to the web build. Keep API/worker database URLs secret. The public API URL must be browser reachable; the internal API URL may be a private service address. Set the canonical public URLs before generating badge/feed snippets.

## Railway-oriented example

Create one Railway project with a PostgreSQL 16 service and three services from this repository:

- `repometeor-api`: Dockerfile `backend/Dockerfile.api`, start command `/usr/local/bin/api`, port 8080, health path `/readyz`.
- `repometeor-worker`: Dockerfile `backend/Dockerfile.worker`, start command `/usr/local/bin/worker`, no domain, always-on restart policy.
- `repometeor-web`: Dockerfile `apps/web/Dockerfile`, port 3000, health path `/`.

Create a one-shot migration service/job from `backend/Dockerfile.api` with `/usr/local/bin/migrate up`; make application rollout depend operationally on its success. Run `/usr/local/bin/seed` only for an intentional demo deployment, never on the live production database.

Reference Railway's PostgreSQL connection variable from API, worker, and migration job. Use Railway private DNS for `INTERNAL_API_URL`, and custom HTTPS domains for public API/web URLs. Set exact web origins in CORS. Railway proxy CIDRs can change; obtain the current authoritative ranges from the platform instead of copying an example here.

Horizontal API replicas share PostgreSQL and must share `RATE_LIMIT_KEY`. Queue claims support multiple workers, but scheduled discovery/scoring may create redundant work if cadence/dedupe configuration drifts; begin with one worker and scale after measuring backlog. Database connections are bounded per process, so account for replica count.

## Backups and verification

Enable platform backups and export a periodic logical backup to a separate retention boundary. Test restoration before launch and after schema changes. After every deployment verify health/readiness, demo flag, one ranking, one detail/history request, compare, badge content type, Atom XML, CLI JSON, newest snapshot age, scoring run time, queue depth, GitHub remaining budget, and mobile web rendering.

Custom domains, npm ownership, GitHub organization, and trademarks are launch decisions, not deployment defaults. See [LAUNCH.md](LAUNCH.md).
