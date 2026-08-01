# Launch guide

RepoMeteor should launch only after the fresh demo stack and live collection path have been verified from empty state. Do not substitute marketing copy for test evidence.

## Prelaunch checklist

- [ ] Check product/repository name, npm package name, domains, and trademarks.
- [ ] Create and secure the GitHub organization; configure branch protection and private vulnerability reporting.
- [ ] Configure production GitHub token, database backups/restore drill, CORS, trusted proxies, shared rate-limit key, and status monitoring.
- [ ] Complete privacy, security, dependency, accessibility, keyboard, reduced-motion, mobile, and overflow reviews.
- [x] Capture deterministic demo dashboard/detail screenshots and Open Graph images with no secrets or personal data. (`docs/assets/dashboard.png`, `docs/assets/repository.png`; regenerate with `pnpm capture:screenshots`.)
- [ ] Verify public API/canonical web URLs, badge URL, Atom feed, CLI package contents, and package name ownership.
- [ ] Run the full CI/fresh-start verification and archive exact results.
- [ ] Prepare the GitHub v0.1 release, issue labels, contributor docs, and good-first issues.
- [ ] Decide whether analytics are necessary; if added, document consent, data flow, retention, and privacy changes.

The maintainer must complete domain, npm-name, GitHub-organization, and trademark checks before public launch. No domain or package ownership is assumed by this repository.

## Repository metadata

Description: **Open-source GitHub momentum rankings using aggregate snapshots, transparent scoring, a dashboard, API, CLI, feeds, and badges.**

Topics: `github`, `open-source`, `developer-tools`, `analytics`, `trending`, `golang`, `nextjs`, `postgresql`, `cli`, `data-visualization`.

## Draft v0.1.0 release notes

RepoMeteor v0.1.0 is the first complete transparent momentum vertical slice. It includes scheduled aggregate GitHub collection, deterministic 6h/24h/7d scoring and confidence, Hidden Gems and New Entrants, a responsive dashboard, public API, TypeScript CLI, SVG badges, Atom feeds, an offline fictional demo, Docker Compose, tests, and self-hosting documentation. It stores no individual stargazer identities and is independent of GitHub.

Known limits: useful history accumulates after deployment; Search API budgets bound discovery; rankings are early signals rather than predictions; package/domain/organization names remain subject to the completed launch checks.

## Launch posts

Hacker News title: **Show HN: RepoMeteor – transparent momentum rankings from aggregate GitHub snapshots**

Hacker News / Reddit body: We built RepoMeteor to find projects whose movement is hidden by lifetime star totals. It stores scheduled repository-level counts, calculates a public versioned score from velocity, relative growth, acceleration, forks, activity, freshness, and observed-history confidence, then serves the same data through a dashboard, API, CLI, feed, and badge. There is no account, no stargazer identity collection, no Trending HTML scraping, and no AI ranking. The repository includes a fictional offline demo and self-hosting stack. We would value feedback on the formula, confidence presentation, and operations model.

LinkedIn: **Early open-source movement deserves an explainable signal. RepoMeteor is an open-source dashboard/API/CLI that ranks aggregate repository snapshots with a published deterministic formula. It is self-hostable, account-free, and does not track individual stargazers. v0.1 feedback is especially welcome on scoring transparency and contributor onboarding.**

X: **Introducing RepoMeteor: transparent GitHub repository momentum from scheduled aggregate snapshots—dashboard, API, CLI, Atom feeds, badges, and self-hosting. No individual stargazer tracking. No opaque AI ranking. Open-source v0.1.**

## Dev.to outline

1. The lifetime-stars problem and discovering projects early.
2. Why RepoMeteor stores its own aggregate snapshot history.
3. `momentum-v1`: features, cohort normalization, confidence, and deterministic explanations.
4. Privacy boundaries and prohibited data sources.
5. Go/Next.js/PostgreSQL architecture and durable job queue.
6. Demo mode, API/CLI/feed/badge consumption, and self-hosting.
7. Honest limitations and the roadmap.
8. How to reproduce scores and contribute fixtures.

All launch material must say scheduled or periodic—not real-time—and must avoid claims such as “best,” “guaranteed trending,” or affiliation with GitHub.
