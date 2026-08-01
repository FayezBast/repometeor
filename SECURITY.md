# Security policy

## Supported versions

Until the first stable release, only the latest tagged `0.x` release and the default branch receive security fixes. After v1, this table will be updated with explicit support windows.

## Private reporting

Do not open a public issue for a suspected vulnerability, leaked token, exploitable deployment detail, private data, or working proof of concept.

Report privately through GitHub: open the repository's **Security** tab and choose **Report a vulnerability**. This opens a private advisory visible only to you and the maintainers. Include affected version, impact, reproduction steps, and a minimal proof without real credentials or personal data.

If you cannot use GitHub private reporting, open a public issue that says only that you have a security report and asks for a private channel — never include the details themselves.

Maintainers will acknowledge reports when they are able, assess severity and affected releases, coordinate a fix and disclosure, and credit reporters who want attribution. This policy does not promise a fixed response deadline because maintainer availability may vary.

## Token exposure

If a GitHub or deployment token is exposed: revoke it immediately at the provider, rotate dependent secrets, inspect access and audit logs, remove it from current files and reachable history using an appropriate history-rewrite procedure, invalidate caches/artifacts, and notify affected operators. Deleting a visible line or closing an issue is not sufficient. Never ask a reporter to email the active secret.

## Scope notes

High-value areas include URL/input validation, trusted-proxy handling, public tracking limits, SQL parameters, SVG/XML escaping, dependency/build integrity, secrets in logs, job duplication, and cross-origin behavior. Availability problems caused solely by unsupported configuration are usually operations issues, but a reliable resource-exhaustion path is security-relevant.
