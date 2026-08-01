# Changelog

All notable changes follow Keep a Changelog and semantic versioning. The project is unreleased; interfaces may change before v0.1.0.

## [Unreleased]

### Added

- Go API, worker, migration and seed commands.
- PostgreSQL migrations for repositories, aliases, aggregate snapshots, momentum scores, scoring runs, durable jobs, discovery audits, request validators, and hashed rate counters.
- Live and fixture GitHub providers with conditional requests, retries, discovery, and rate budgeting.
- Deterministic `momentum-v1` scoring for 6h, 24h, and 7d windows.
- Public dashboard routes, typed API client, TypeScript CLI, SVG badges, Atom feeds, demo dataset, and Docker Compose stack.
- Unit and PostgreSQL-backed integration test suites.
- Open-source, operations, deployment, privacy, architecture, and launch documentation.

### Changed

- Centralized the collection job payload shared by API producers and worker consumers.
- Pinned TypeScript and ESLint to versions compatible with the current Next.js lint toolchain.

### Security

- Exact-origin CORS, bounded bodies and queries, structured redacted logs, HMAC-based durable tracking limits, XML escaping, and non-root/read-only-compatible containers.
