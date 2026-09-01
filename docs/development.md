# Development guide

## Toolchain

- Go 1.21 or newer;
- Caddy 2 for real reverse-proxy integration tests;
- Tailscale is needed only for `doctor` and explicitly opted-in manual tests.

## Commands

```bash
make fmt
make vet
make test
make test-race
make build
make check
```

The default tests never alter real Tailscale state. Tailscale behavior is
covered by fake adapters, executable fake `tailscale`/`caddy` binaries, and
argument assertions. Safe-handoff integration tests exercise final HTTPS
hostname rejection and compensating rollback without touching the real
tailnet. Caddy integration tests use temporary paths, random loopback ports,
and a private Unix admin socket.

## Package design

Business logic depends on interfaces for Caddy, Tailscale, health checks, final
URL verification, and time. Keep it that way: OS or network calls in the core
make safe testing much harder.

The registry is intentionally a small JSON document rather than a database. A
cross-process flock and atomic rename provide the required concurrency and
crash consistency for ten previews.

## Manual local validation

Use a disposable upstream and a dedicated `TAILPREVIEW_HOME`. Do not run the
real `up` command unless you intend to create a private Serve listener on the
current tailnet.

```bash
TAILPREVIEW_HOME="$(mktemp -d)" ./bin/tailpreview doctor
```

For a real opt-in validation:

```bash
python3 -m http.server 3000 --bind 127.0.0.1
TAILPREVIEW_HOME=/tmp/tailpreview-manual ./bin/tailpreview up \
  http://127.0.0.1:3000 --name manual-test
TAILPREVIEW_HOME=/tmp/tailpreview-manual ./bin/tailpreview check manual-test
TAILPREVIEW_HOME=/tmp/tailpreview-manual ./bin/tailpreview down manual-test
```

Always confirm `tailscale serve status` contains no leftover manual test port.
If Tailscale reports that Serve is not enabled, complete its one-time tailnet
consent with the private test command printed by Tailpreview, then run its
matching exact-port `off` command before retrying. Never substitute Funnel.

## Release preparation

Releases are built by GoReleaser for macOS and Linux on `amd64` and `arm64`.
The GitHub repository needs a fine-grained `HOMEBREW_TAP_TOKEN` Actions secret
with Contents read/write access only to `JhumanJ/homebrew-tap`. Tags matching
`v*` publish GitHub release archives and regenerate the Homebrew formula from
the release checksums.

Validate the release configuration without publishing:

```bash
goreleaser check
goreleaser release --snapshot --clean
```
