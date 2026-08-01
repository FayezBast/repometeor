# Contributing to RepoMeteor

Thank you for helping make transparent momentum data useful and trustworthy.

## Prerequisites and setup

Install Go 1.25+, Node 24+, pnpm 10.30.1, Docker 29+/Compose 5+, and Git. Fork the repository, create a focused branch, then run:

```bash
cp .env.example .env
make bootstrap
docker compose up --build
```

Demo mode is the default and needs no token. For live-mode work, set `DEMO_MODE=false`, `NEXT_PUBLIC_DEMO_MODE=false`, and a read-only `GITHUB_TOKEN`; never paste a token into code, logs, issues, fixtures, screenshots, or test output.

The repository layout and architectural boundaries are documented in [AGENTS.md](AGENTS.md). Small, reviewable commits with clear imperative subjects are preferred. Do not mix unrelated reformatting with behavior changes.

## Quality gates

Run the narrow tests while iterating and the complete relevant gate before a pull request:

```bash
make format
make lint
make test
make test-integration
make build
make smoke
```

Database integration tests require `REPOMETEOR_TEST_DATABASE_URL`. E2E tests require the demo stack. Add behavior-based tests for every bug fix and meaningful branch. Accessibility, mobile behavior, error/empty states, and JSON output are part of correctness.

## Database and public contracts

Add a numbered forward and down migration; never rewrite an applied migration. Make snapshot writes idempotent, preserve historical data, use constraints for invariants, and parameterize every SQL value. Explain rollback/data-retention implications in [DECISIONS.md](docs/DECISIONS.md).

Update [openapi.yaml](contracts/openapi.yaml), examples, API/CLI/web schemas, and docs together when a route or payload changes. Validate bounded inputs and preserve `application/problem+json` errors.

## Scoring changes

Scoring must remain pure, deterministic, explainable, and based only on aggregate snapshots. Add fixed-clock fixtures covering small and large repositories, missing history, ties, outliers, and anomalies. If behavior changes, bump `FormulaVersion`, update [SCORING.md](docs/SCORING.md), the methodology endpoint, migrations if necessary, and the decision log. Do not tune against individual identities or scrape GitHub Trending.

## Languages, topics, and demo data

Languages are GitHub-reported strings, not a hard-coded backend enum. Sanitize them before constructing Search queries and add fixture/provider tests for new discovery partitions. Demo records must be fictional, deterministic, clearly labeled, and exercise real edge cases without resembling actual maintainers.

## Pull request checklist

- [ ] Scope is focused and commits explain intent.
- [ ] Format, lint, relevant tests, and builds pass with exact results in the PR.
- [ ] API contract/examples and docs match behavior.
- [ ] Migrations include rollback/backup guidance.
- [ ] Scoring changes bump/version/document the formula.
- [ ] Demo mode remains visibly labeled and cannot contact GitHub.
- [ ] No tokens, personal data, raw IPs, or real production URLs are present.
- [ ] Accessibility, security, performance, and mobile impact were considered.

Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md). By participating, you agree to [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).
