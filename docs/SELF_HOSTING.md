# Self-hosting

RepoMeteor needs PostgreSQL, the API process, the worker process, and the web process. The supported first-run path is Docker Compose.

```bash
cp .env.example .env
docker compose up --build
```

Verify `http://localhost:8080/healthz`, `http://localhost:8080/readyz`, and `http://localhost:3000`. Demo mode is the safest first run: it seeds deterministic fictional data, labels every response, and cannot contact GitHub.

For live collection, use a read-only GitHub token and set both backend and browser demo flags false. Configure public/internal URLs carefully: `API_PUBLIC_URL` and `WEB_PUBLIC_URL` are canonical external origins, `NEXT_PUBLIC_API_URL` must be reachable by browsers, and `INTERNAL_API_URL` is used only by server-side web requests. Restrict `CORS_ALLOWED_ORIGINS` to exact trusted web origins.

Put TLS and request-size limits at the edge, expose only API/web ports, and keep PostgreSQL and `/metrics` private. Configure `TRUSTED_PROXY_CIDRS` only for known proxies and use the same `RATE_LIMIT_KEY` on every API instance. Back up PostgreSQL, test restores, monitor queue depth/rate budgets/snapshot freshness, and follow [RUNBOOK.md](RUNBOOK.md).

Upgrades should back up the database, review migrations and `CHANGELOG.md`, run migrations once, deploy API/worker/web versions built from the same tag, then verify readiness, trending, a detail page, badge, feed, and CLI. Production rollback favors restoring the tested backup and previous image set over blindly applying down migrations.
