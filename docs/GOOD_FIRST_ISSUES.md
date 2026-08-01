# Good first issues

These are scoped contribution candidates, not claims that they are already filed. Confirm the current branch and open an issue before implementation.

## 1. Add a repository-card keyboard test

Context: momentum cards must be fully usable without a pointer. Likely files: `apps/web/components/MomentumCard.tsx`, `apps/web/tests`. Acceptance: visible focus, meaningful link name, no nested interactive controls, RTL test. Difficulty: easy. Test: `pnpm --filter @repometeor/web test`.

## 2. Improve an empty filtered collection

Context: filters should explain how to recover from zero results. Likely files: `apps/web/app/explore/page.tsx`, `components/EmptyState.tsx`. Acceptance: preserves URL filters, offers a clear reset, works on mobile, no nonfunctional button. Difficulty: easy. Test: web unit test plus Playwright empty-state case.

## 3. Add NDJSON CLI output

Context: scripts may prefer one JSON object per result. Likely files: `apps/cli/src/index.ts`, `format.ts`, CLI tests. Acceptance: `--ndjson` conflicts cleanly with `--json`, stdout is valid line-delimited JSON, errors remain on stderr. Difficulty: medium. Test: `pnpm --filter @repometeor/cli test && pnpm --filter @repometeor/cli build`.

## 4. Add shell completion generation

Context: frequent CLI users benefit from command/option completion. Likely files: `apps/cli/src`, CLI README/docs. Acceptance: Bash, Zsh, or Fish completion is generated from Commander definitions and documented without runtime shell execution. Difficulty: medium. Test: CLI unit tests and inspect generated completion syntax.

## 5. Expand Atom feed escaping fixtures

Context: repository text must never break XML. Likely files: `backend/internal/api/feed.go`, API tests. Acceptance: ampersands, angle brackets, quotes, control characters, and Unicode yield well-formed XML with no markup injection. Difficulty: easy. Test: `go test ./internal/api`.

## 6. Add scoring tie fixtures

Context: deterministic ordering is a product guarantee. Likely files: `backend/internal/scoring/scoring_test.go`. Acceptance: several exact-feature ties retain stable rank order across shuffled input and repeated runs. Difficulty: easy. Test: `go test ./internal/scoring -count=20`.

## 7. Add another fictional demo cohort

Context: demos should show realistic edge cases without real maintainers. Likely files: `backend/internal/demo/dataset.go`, dataset tests. Acceptance: at least eight new clearly fictional repos add a documented language/topic and preserve deterministic counts/ranking coverage. Difficulty: medium. Test: demo and API integration suites.

## 8. Improve badge high-contrast themes

Context: badges must stay readable in light/dark README renderers. Likely files: `backend/internal/api/badge.go`, badge tests. Acceptance: named theme, WCAG-informed contrast documentation, bounded width, valid self-contained SVG, existing themes unchanged. Difficulty: medium. Test: Go tests plus render fixtures in two backgrounds.

## 9. Add a card accessibility E2E assertion

Context: automated axe coverage should include actual ranking content. Likely files: `apps/web/tests/e2e/home.spec.ts`. Acceptance: seeded card grid passes axe, headings remain ordered, sparkline has a text alternative, mobile viewport has no horizontal overflow. Difficulty: easy. Test: `make test-e2e` with demo stack.

## 10. Document another deployment target

Context: generic instructions benefit from one provider-specific mapping. Likely files: new `docs/deploy/*.md`, `DEPLOYMENT.md`. Acceptance: PostgreSQL/API/worker/web, migrations, health checks, internal/public URLs, CORS, backups, persistent worker, and verification are covered without fake one-click links. Difficulty: easy. Test: peer-review every command against current images/env names.

## 11. Add a snapshot export command design

Context: operators need aggregate-data portability without personal data. Likely files: design issue, `backend/cmd`, `docs/PRIVACY.md`. Acceptance: proposal covers bounded CSV/NDJSON, UTC ranges, pagination, memory limits, provenance, no raw cache/body/token fields, and tests before code begins. Difficulty: medium. Test: approved design plus command integration fixtures when implemented.

## 12. Add a language discovery fixture

Context: GitHub Search query construction must remain injection-safe. Likely files: `backend/internal/github/search.go`, `search_test.go`. Acceptance: a real language name with punctuation/spaces is either safely encoded or deliberately rejected, partition queries remain public-only and disjoint. Difficulty: easy. Test: `go test ./internal/github`.
