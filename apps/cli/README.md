# RepoMeteor CLI

Command-line access to transparent RepoMeteor momentum rankings.

```bash
pnpm add --global @repometeor/cli
repometeor trending --window 24h
repometeor inspect owner/repository
repometeor compare owner/first owner/second --json
```

Commands are `trending`, `inspect`, `track`, `compare`, and `config`. Global options include `--api-url`, `--json`, `--no-color`, and `--timeout`. `REPOMETEOR_API_URL` provides the API origin when no flag is supplied; the default is `http://localhost:8080`.

Human-readable output goes to stdout, operational errors go to stderr, and failures use non-zero exit codes. Use `--json` for automation.

The package is Apache-2.0 licensed. RepoMeteor is independent of and not endorsed by GitHub. It consumes repository-level aggregate data and does not require individual stargazer identities.

The unscoped `repometeor` npm name, organization, and trademark must be checked before public publication; this development package remains `@repometeor/cli` until ownership is confirmed.
