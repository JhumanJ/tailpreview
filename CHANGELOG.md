# Changelog

All notable changes to Tailpreview are documented here. Releases follow
[Semantic Versioning](https://semver.org/).

## [0.2.0] - 2026-09-01

- Add explicit public-path verification with same-origin-only redirect
  following and rollback on unsafe or unhealthy final handoffs.
- Add `preview.handoff_url`, verification timestamps, and structured JSON
  diagnostics for hostname rejection, loopback redirects, origin leaks, and
  unexpected final responses.
- Add `tailpreview check` to revalidate local health, current MagicDNS
  identity, the exact Tailscale Serve listener, absence of Funnel on that
  port, and all declared public paths.
- Add repeatable YAML/CLI `verify` configuration and fake-binary integration
  coverage for failed handoff rollback.

## [0.1.1] - 2026-08-30

- Replace the unsigned Homebrew cask with a cross-platform formula so macOS
  installs do not trigger a Gatekeeper quarantine prompt.
- Update GitHub Actions to their current Node 24-compatible major versions.
- Add detailed public release notes.

## [0.1.0] - 2026-08-30

Initial public release.

- Private HTTPS previews through Tailscale Serve, never Funnel.
- Ordered frontend, API, asset, and WebSocket routing through dedicated Caddy.
- Strict localhost-only upstream validation and privacy-filtered logs.
- Ten-preview pool with stable worktree reuse, TTL, maximum age, LRU eviction,
  pinning, active-request protection, and automatic garbage collection.
- Transactional Caddy/Tailscale updates with rollback on failure.
- Stable JSON output and non-interactive lifecycle commands for coding agents.
- macOS and Linux binaries for Intel and ARM.
- Homebrew cask and bundled Codex Tailpreview skill/plugin.

[0.1.1]: https://github.com/JhumanJ/tailpreview/releases/tag/v0.1.1
[0.1.0]: https://github.com/JhumanJ/tailpreview/releases/tag/v0.1.0
[0.2.0]: https://github.com/JhumanJ/tailpreview/compare/v0.1.1...v0.2.0
