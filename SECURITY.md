# Security policy

## Supported versions

Until the first stable release, only the latest tagged `0.x` release and the default branch receive security fixes. After v1, this table will be updated with explicit support windows.

## Private reporting

Do not open a public issue for a suspected vulnerability, leaked token, exploitable deployment detail, private data, or working proof of concept. Before launch, maintainers must replace `security@project.invalid` with a monitored private address and enable GitHub private vulnerability reporting. Include affected version, impact, reproduction steps, and a minimal proof without real credentials or personal data.

Maintainers will acknowledge reports when they are able, assess severity and affected releases, coordinate a fix and disclosure, and credit reporters who want attribution. This policy does not promise a fixed response deadline because maintainer availability may vary.

## Token exposure

If a GitHub or deployment token is exposed: revoke it immediately at the provider, rotate dependent secrets, inspect access and audit logs, remove it from current files and reachable history using an appropriate history-rewrite procedure, invalidate caches/artifacts, and notify affected operators. Deleting a visible line or closing an issue is not sufficient. Never ask a reporter to email the active secret.

## Scope notes

High-value areas include URL/input validation, trusted-proxy handling, public tracking limits, SQL parameters, SVG/XML escaping, dependency/build integrity, secrets in logs, job duplication, and cross-origin behavior. Availability problems caused solely by unsupported configuration are usually operations issues, but a reliable resource-exhaustion path is security-relevant.
