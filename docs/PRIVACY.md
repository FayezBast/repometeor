# Privacy

RepoMeteor stores public repository-level metadata and aggregate counts such as stars, forks, issues, releases, languages, and topics over time. It does not need or collect individual stargazer identities, does not call stargazer-list endpoints, and does not sell user data.

Tracking submissions are operational requests, not accounts. The public rate limiter hashes client addresses with an HMAC key and stores only time-window counters; raw IP addresses are not stored in application tables. Reverse proxies and infrastructure may still create access logs, so operators should minimize personal data, restrict log access, and configure the shortest retention compatible with abuse prevention and incident response.

If browser-local watchlists are added, they must remain local unless a future release obtains explicit consent and updates this policy. RepoMeteor does not use cookies or require authentication in v0.1.

Self-hosters control their database, logs, backups, and GitHub token. They are responsible for their applicable privacy obligations and for documenting any analytics they add.
