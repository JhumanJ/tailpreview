# Changelog

All notable changes to Tailpreview are documented here. Releases follow
[Semantic Versioning](https://semver.org/).

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

[0.1.0]: https://github.com/JhumanJ/tailpreview/releases/tag/v0.1.0
