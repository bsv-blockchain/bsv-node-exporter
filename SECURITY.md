# Security policy

## Reporting a vulnerability

Report vulnerabilities privately through GitHub's private vulnerability reporting: **Security → Report a vulnerability** on this repository. Do not open a public issue.

Private vulnerability reporting is switched on when the repository becomes public. Until then only its maintainers can see the repository at all; report findings to them directly.

Include the version (`bsv_exporter_build_info`), the configuration involved (without credentials), and steps to reproduce.

## Supported versions

Only the latest release receives security fixes.

## Scope

- Credential exposure: the RPC password or URL secrets appearing in logs, metrics or errors.
- Making the exporter call RPC methods outside its allowlist.
- Using the exporter to load or disrupt the node beyond one bounded set of calls per scrape.
- Supply-chain issues in the release pipeline or published images.

`/metrics` is unauthenticated by design. Restrict who can reach it at the network level.
