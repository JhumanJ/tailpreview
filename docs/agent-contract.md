# Agent contract

Tailpreview is safe to call from multiple agents on the same machine. Registry
mutations use a cross-process lock, generated state is written atomically, and
the Caddy/Tailscale update sequence has compensating rollback.

## Required invocation style

```bash
tailpreview <command> --json --non-interactive
```

`--non-interactive` guarantees that Tailpreview itself never prompts.
Tailscale is invoked with its non-interactive approval flag. Missing tailnet
prerequisites cause an error rather than a hidden browser workflow.

## JSON version

Every command response contains:

```json
{
  "schema_version": 1
}
```

Successful `up` responses include:

```json
{
  "schema_version": 1,
  "action": "up",
  "preview": {
    "id": "2d1af06c975b6bb1",
    "name": "opnform-pr-1281",
    "external_port": 8443,
    "gateway_port": 18080,
    "url": "https://dev-mini.example.ts.net:8443",
    "idle_ttl": "24h0m0s",
    "max_age": "168h0m0s",
    "status": "active"
  },
  "warnings": []
}
```

Errors are written to stderr as:

```json
{
  "schema_version": 1,
  "error": "human-readable error"
}
```

## Idempotency

- `up` for the same absolute worktree updates the existing preview in place.
- Reapplying identical Caddy state does not reload Caddy.
- `pin` and `unpin` set desired state rather than toggling it.
- `gc` can run repeatedly.
- `down` returns not-found after the preview is removed; agents should treat
  that response explicitly rather than assume success.

## Exit codes

- `0`: command succeeded;
- `1`: validation, dependency, health, conflict, or runtime failure;
- `2`: no command was provided.

The first public release may introduce more specific non-zero codes, but it
will retain the meanings above.

## Safe agent workflow

1. Start the project using its repository-native scripts.
2. Read the actual allocated localhost ports.
3. Run `tailpreview doctor --json`.
4. Run `tailpreview up ... --json --non-interactive`.
5. Request the returned HTTPS URL and verify every required route through that
   final origin. Tailpreview accepts only `2xx` and `3xx` during its own final
   URL verification; agents must likewise treat final `4xx` and `5xx`
   responses as failed handoffs.
6. Confirm `tailscale serve status` still describes the listener as tailnet
   only, then report the URL, expiry, routes, and warnings verbatim.
7. Do not use Tailscale Funnel as a fallback.
8. Use `tailpreview down ... --json --non-interactive` when the preview is no
   longer needed. Do not delete the worktree unless separately requested.
