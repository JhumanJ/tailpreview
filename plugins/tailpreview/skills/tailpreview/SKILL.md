---
name: tailpreview
description: Expose already-running localhost frontends, APIs, and WebSocket services through temporary tailnet-only HTTPS URLs with Tailpreview. Use for remote testing, local worktree previews, Tailscale Serve previews, preview lifecycle, or requests to open a local app from another device. Do not use for public Internet deployment or Tailscale Funnel.
---

# Tailpreview

Use the installed `tailpreview` CLI to create and manage private previews. Keep
application startup and application-specific configuration separate from the
preview lifecycle.

## Create a preview

1. Inspect the repository status and preserve unrelated changes. Locate
   `.tailpreview.yml` from the current directory upward and identify the actual
   loopback ports used by the already-running services.
2. If the required service is not running, report the missing endpoint. Start
   it only when the user's request also authorizes starting the local stack,
   and use the repository's own lifecycle commands.
3. Run `tailpreview doctor --json`. If the CLI is missing, recommend
   `brew install jhumanj/tap/tailpreview`. Do not replace a failed
   dependency or Tailscale consent check with an unsafe workaround.
4. Declare the public paths required for a usable handoff in project
   `verify` configuration or repeatable `--verify` flags. Include the initial
   page and lightweight API/readiness routes that the remote test depends on.
   Verification paths must not contain queries, fragments, credentials, or
   signed values.
5. Run `tailpreview up --json --non-interactive`. Prefer the committed project
   configuration plus worktree-specific `--set` values. Otherwise pass ordered
   `--route` flags, with specific API/WebSocket routes before `/*`.
6. Parse the JSON result instead of scraping human output. Treat non-zero exit
   status or an error object as failure. Branch on a structured `code` and use
   its bounded `remediation` when present.
7. Use exactly `preview.handoff_url` for the handoff. Never substitute
   `preview.url`, reconstruct the URL, or expose a route's localhost upstream.
8. Immediately before reporting or reusing the preview, run
   `tailpreview check <name> --json --non-interactive`. It rechecks local
   health, Tailscale identity and listener state, absence of Funnel on the
   preview port, and every declared public path.
9. Treat `up` or `check` success as the CLI's safe-handoff proof. For browser
   QA beyond the declared checks, keep every navigation on the exact
   `handoff_url` origin and treat any loopback, cross-origin, downgrade, `4xx`,
   or `5xx` result as a failed handoff.
10. Return `handoff_url`, preview name, expiry, routes, verification results,
    and warnings to the user.

If `code` is `external_forbidden`, inspect both the exact development-server
hostname allowlist and the application's route or authentication
authorization. If the hostname is the cause, allow only the exact `hostname`
using an ignored local runtime setting supported by that application, restart
that already-authorized local service, then rerun `up` or `check`. Never set a
wildcard host allowlist or commit a machine-specific hostname without an
explicit project decision.

If `code` reports a loopback or cross-origin redirect, configure the
application's external origin, OAuth callback, authentication, and WebAuthn
settings to use the exact Tailpreview HTTPS origin. Tailpreview must not edit
those application settings. After correcting a permanent redirect, use a
private browser window for manual QA because browsers may cache `301`/`308`
responses.

## Lifecycle

Use `list`, `status`, `check`, `logs`, `pin`, `unpin`, `down`, and `gc` with
`--json` and `--non-interactive` where supported. Do not remove a preview
merely because the current task ends; leave it available for remote testing
unless the user asks to stop it. `down` may remove only the requested
Tailpreview preview.

## Safety boundaries

- Never invoke or recommend Tailscale Funnel.
- Never modify tailnet ACLs, grants, tags, keys, or device identity.
- Accept only `localhost`, `127.0.0.1`, or `::1` upstreams.
- Never expose databases, caches, mail tools, admin consoles, or debug panels
  unless the user explicitly requests that exact service and understands it is
  visible to permitted tailnet peers.
- Never stop application processes, containers, databases, or worktrees as
  part of Tailpreview cleanup.
- Do not put credentials, cookies, headers, bodies, query strings, signed URLs,
  or client IPs into logs or responses.
- Preserve unrelated Caddy and Tailscale Serve configuration.

For exact JSON and exit-code behavior, read the repository's
`docs/agent-contract.md` only when the task needs contract details.
