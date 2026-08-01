# API guide

The RepoMeteor API is versioned under `/v1`; operations endpoints remain unversioned. The authoritative machine-readable contract is [contracts/openapi.yaml](../contracts/openapi.yaml).

## Routes

- `GET /healthz`, `GET /readyz`, `GET /metrics`
- `GET /v1/trending`, `/search`, `/languages`, `/topics`, `/methodology`
- `GET /v1/repositories/{owner}/{repository}` and `/history`
- `GET /v1/compare?repositories=owner/a,owner/b`
- `POST /v1/repositories/track`
- `GET /v1/badges/{owner}/{repository}.svg`
- `GET /v1/feeds/trending.xml`

All read routes also accept `HEAD`. Collection responses include `meta.demoMode`; ranking responses include the formula version, generation time, confidence, reasons, and an opaque `links.nextCursor`. Lists, history ranges, request bodies, and comparison counts are bounded.

## Errors and rate limits

Errors use RFC 9457 `application/problem+json` with stable relative types:

- `/problems/validation-failed`
- `/problems/not-found`
- `/problems/rate-limited`
- `/problems/upstream-unavailable`
- `/problems/internal-error`
- `/problems/not-ready`
- `/problems/unsupported`
- `/problems/payload-too-large`

Every problem includes HTTP `status`; most include `detail`, `instance`, and `requestId`. Validation errors add field-level `errors`. Rate/upstream errors may add `retryAfterSeconds` and a `Retry-After` header. Quote the request ID to operators rather than asking for internal logs.

Read throttling is per API instance. Tracking submission limits are durable across restarts and return `X-RateLimit-Limit`, `X-RateLimit-Remaining`, and `X-RateLimit-Reset`. Production replicas must share `RATE_LIMIT_KEY`.

## Caching and clients

Bounded GET responses use endpoint-specific public cache policies and ETags; send `If-None-Match` to receive `304 Not Modified`. Errors and tracking responses use `no-store`. Exact CORS origins are configured by operators; this is a public HTTP API, but browsers do not automatically receive wildcard access.

```bash
curl 'http://localhost:8080/v1/trending?window=24h&limit=20'
curl 'http://localhost:8080/v1/repositories/orbit-labs/signal-kit/history?range=7d'
curl -H 'Content-Type: application/json' \
  --data '{"repository":"owner/repository"}' \
  http://localhost:8080/v1/repositories/track
```

Use the CLI for shell automation and `--json` for stable machine output. Badge and Atom responses are escaped, self-contained, and served with their correct XML/SVG media types.
