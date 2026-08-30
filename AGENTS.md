# AGENTS.md

## Mission

Tailpreview exposes already-running localhost HTTP services privately through
Tailscale Serve. It does not start application stacks and it never uses
Tailscale Funnel.

## Non-negotiable safety boundaries

- Never introduce `tailscale funnel` support in the v1 command path.
- Never modify tailnet ACLs, grants, tags, keys, or device identity.
- Only accept loopback upstreams (`localhost`, `127.0.0.1`, or `::1`) unless a
  future, separately reviewed security design explicitly changes this rule.
- Never use `tailscale serve reset`; remove only ports owned by Tailpreview.
- Never stop application processes, containers, worktrees, or databases.
- Keep request bodies, headers, cookies, query strings, credentials, and client
  IPs out of persistent logs.
- Preserve unrelated user Caddy and Tailscale configuration.

## Architecture

- `cmd/tailpreview`: thin CLI entrypoint.
- `internal/config`: strict YAML discovery, substitution, and validation.
- `internal/state`: atomic registry and cross-process locking.
- `internal/caddy`: generation and lifecycle of the dedicated Caddy instance.
- `internal/tailscale`: narrowly scoped Serve adapter.
- `internal/app`: orchestration, transactions, TTL/LRU, and output contracts.

Keep OS/process calls behind interfaces so tests use fakes and never mutate the
developer's real tailnet.

## Quality gates

Run before handing work back:

```bash
go test ./...
go test -race ./...
go vet ./...
gofmt -w .
go build ./cmd/tailpreview
```

When integration behavior changes, add a test with fake `tailscale` and `caddy`
binaries. Real-tailnet tests must be opt-in and must allocate only an explicitly
provided test port.

## CLI compatibility

- JSON output is a public, versioned agent contract.
- `--non-interactive` must never prompt.
- Commands should be idempotent whenever the desired state is unchanged.
- New flags override environment values, which override project YAML, which
  override global defaults.
- Preserve exit-code meanings documented in `docs/agent-contract.md`.

## Documentation

Update the README, example configuration, and agent contract whenever a public
command, field, default, security boundary, or output shape changes.
