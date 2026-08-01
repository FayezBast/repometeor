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
- Stripped terminal escape sequences, C1 controls, bidirectional overrides and zero-width characters from GitHub-supplied display text. A repository description is written by whoever owns the repository, and the CLI printed it verbatim, so a crafted description could erase and repaint the surrounding terminal lines — enough to forge an instruction to run a command. Sanitisation runs both on ingest and at CLI render, because `--api-url` points the CLI at an arbitrary server.
- Rejected repository payloads carrying control characters in an owner or name, which cannot be cleaned up without changing the identity they address.
- Raised Go to 1.25.12 and quic-go to v0.59.1, clearing all 14 vulnerabilities `govulncheck` reported as reachable.
- Pinned patched floors for `postcss`, `sharp` and `esbuild`, which arrive through `next` and `tsup` at vulnerable versions with no upstream release that lifts them.
