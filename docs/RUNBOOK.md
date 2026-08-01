# Operations runbook

Start every incident by recording UTC time, deployed version, affected service, recent change, request/job IDs, and user-visible impact. Preserve evidence without copying tokens, authorization headers, database passwords, raw IPs, or full GitHub response bodies.

## GitHub rate limit exhausted

Check the core/search remaining metrics and reset timestamp. Confirm `GITHUB_RATE_BUDGET_PERCENT`, token validity, and discovery cadence. Pause or reduce background discovery/collection; user submissions already use the reserved budget. Do not rotate tokens merely to bypass provider limits. Resume after reset and confirm conditional-request cache hits.

## Worker backlog or stuck jobs

Inspect queue depth by status, oldest `run_after`, active worker logs, database health, and GitHub rate state. A restarted worker recovers locks older than `STALE_JOB_TIMEOUT`; do not manually requeue still-running jobs. For genuinely stuck rows, stop workers, identify ownership, preserve `last_error`, and use an audited SQL update or a maintenance tool. Dead jobs require root-cause review before replay.

## Database unavailable

Expect `/readyz` to fail while `/healthz` stays live. Check provider status, DNS/TLS, pool exhaustion, disk, connection limits, and credentials. Restore connectivity before restarting processes; restart loops do not repair PostgreSQL. Validate migrations and a read-only query after recovery.

## Failed migration

Stop new application versions, capture the migration error and current `schema_migrations`, and compare the deployed binary with repository migrations. Do not repeatedly apply or run down migrations without reviewing partial effects. Restore the pre-deploy backup if correctness is uncertain, deploy the previous image set, then reproduce against a database copy.

## Missing snapshots or score-run failure

Check worker liveness, due repositories, queue depth, newest snapshot age, tier schedule, GitHub budget, and collection error classes. For scoring, verify a completed `scoring_runs` row for each window and review excluded/provisional counts. Repair collection first, then enqueue one scoring run; never fabricate historical counts.

## API latency

Compare route-template duration metrics, database query duration, pool utilization, payload limits, and cache/ETag behavior. Check ranking/history indexes with `EXPLAIN (ANALYZE, BUFFERS)` on a sanitized production-like copy. Avoid repository names as metric labels. Scale API instances only after using a shared `RATE_LIMIT_KEY` and verifying database capacity.

## Demo mode accidentally enabled

Treat displayed fictional data as an incident. Set `DEMO_MODE=false` and `NEXT_PUBLIC_DEMO_MODE=false`, configure the token, redeploy API/worker/web from the same version, and remove demo rows only through a reviewed migration/maintenance procedure. Verify response `demoMode`, banner absence, live collection, and public messaging.

## Token rotation

Create a least-privilege replacement, deploy it without logging the value, verify authenticated rate capacity and one aggregate repository request, then revoke the old token. If exposure is suspected, follow [SECURITY.md](../SECURITY.md) immediately.

## Backup and restore

Use provider snapshots plus periodic logical backups, encrypted and access-controlled. Test restore into an isolated PostgreSQL 16 database, run migrations, compare table counts/newest timestamps, start a disposable API/worker pair, and smoke key routes. Record recovery time and point objectives; an untested backup is not a recovery plan.

## Repository rename

Confirm GitHub's stable numeric ID and canonical owner/name. The collector should update the canonical row and create an alias; old RepoMeteor URLs should resolve with canonical metadata. Investigate only if history split across IDs or aliases collide—do not merge by name alone.

## Recovery after downtime

Restore PostgreSQL first, run migrations once, then start API and worker. Monitor stale-lock recovery, rate budget, backlog, and gradual tiered collection. Do not enqueue every repository at maximum priority. Once fresh snapshots exist, run scoring and verify timestamps/confidence communicate the gap honestly.
