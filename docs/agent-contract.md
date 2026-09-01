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
    "handoff_url": "https://dev-mini.example.ts.net:8443",
    "verify": [
      {"path": "/", "min_code": 200, "max_code": 399},
      {"path": "/api/health/ready", "min_code": 200, "max_code": 299}
    ],
    "last_verified_at": "2026-09-01T09:12:34Z",
    "idle_ttl": "24h0m0s",
    "max_age": "168h0m0s",
    "status": "active"
  },
  "warnings": []
}
```

`url` remains for compatibility. New integrations must hand the user exactly
`preview.handoff_url`; `preview.routes[].upstream` values are local diagnostic
data and are never handoff URLs.

Successful `check` responses include a fresh safe-handoff report:

```json
{
  "schema_version": 1,
  "action": "check",
  "preview": {
    "name": "opnform-pr-1281",
    "handoff_url": "https://dev-mini.example.ts.net:8443",
    "last_verified_at": "2026-09-01T09:15:00Z"
  },
  "verification": {
    "verified_at": "2026-09-01T09:15:00Z",
    "checks": [
      {
        "path": "/",
        "status_code": 200,
        "final_origin": "https://dev-mini.example.ts.net:8443"
      }
    ]
  }
}
```

The actual `preview` object contains the same full fields as `up`; it is
abbreviated above only for readability. Verification output retains origins,
never redirect paths or query strings.

Errors are written to stderr as:

```json
{
  "schema_version": 1,
  "error": "human-readable error"
}
```

Safe-handoff failures add stable machine-readable diagnostics while retaining
the human-readable `error` field:

```json
{
  "schema_version": 1,
  "error": "verify final preview URL: final preview returned HTTP 403 through the Tailpreview origin (path /, HTTP 403)",
  "code": "external_forbidden",
  "phase": "final_verification",
  "status_code": 403,
  "hostname": "dev-mini.example.ts.net",
  "target_path": "/",
  "remediation": "Check the exact development-server hostname allowlist and the route or authentication authorization, restart the local service if its runtime configuration changed, then retry."
}
```

Consumers should branch on `code` and show `remediation`. Currently defined
safe-handoff codes are:

- `external_forbidden` for a public-path `403`, which can be a development
  server hostname allowlist or application authorization failure;
- `loopback_redirect`, `cross_origin_redirect`,
  `https_downgrade_redirect`, and `credentialed_redirect` for unsafe redirect
  destinations;
- `invalid_redirect` and `redirect_limit_exceeded` for broken redirect flows;
- `unexpected_http_status` and `external_unreachable` for final-origin
  response failures;
- `local_health_failed`, `tailscale_unavailable`,
  `tailnet_hostname_changed`, `serve_listener_missing`, and
  `public_endpoint_detected` from `check`;
- `invalid_handoff_url` and `invalid_verification_check` for invalid stored
  handoff state.

`redirect_origin`, when present, contains only `scheme://host:port`. It never
contains a path, query string, fragment, or credentials.

## Idempotency

- `up` for the same absolute worktree updates the existing preview in place.
- Reapplying identical Caddy state does not reload Caddy.
- `pin` and `unpin` set desired state rather than toggling it.
- `gc` can run repeatedly.
- `check` is read-mostly and can run repeatedly; it updates only
  `last_verified_at` and current health metadata after every check passes.
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
4. Declare every public route required for handoff using project YAML or
   repeatable `--verify` flags, then run
   `tailpreview up ... --json --non-interactive`.
5. Treat success as a completed safe-handoff transaction. Tailpreview follows
   redirects only inside the exact HTTPS origin, rejects loopback,
   cross-origin, downgrade, terminal redirect, `4xx`, and `5xx` outcomes, and
   rolls back a newly created preview on failure.
6. Immediately before reporting or reusing an existing preview, run
   `tailpreview check <name> --json --non-interactive`. This rechecks local
   health, current Tailscale hostname, the exact Serve listener, absence of
   Funnel on that port, and every declared public path.
7. Report exactly `preview.handoff_url`, plus expiry, routes, and warnings.
   Never substitute a route's localhost upstream or reconstruct the URL.
8. If `code` is `external_forbidden`, inspect both the exact development-server
   hostname allowlist and application authorization. If the hostname is the
   cause, allow only the exact returned hostname in an ignored runtime setting,
   restart that local service, and retry. If a permanent redirect was
   corrected, use a private browser window to avoid a cached `308` during
   manual QA.
9. Do not use Tailscale Funnel as a fallback.
10. Use `tailpreview down ... --json --non-interactive` when the preview is no
   longer needed. Do not delete the worktree unless separately requested.
