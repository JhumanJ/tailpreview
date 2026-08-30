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
3. Run `tailpreview doctor --json`. If the CLI is missing on macOS, recommend
   `brew install --cask jhumanj/tap/tailpreview`. Do not replace a failed
   dependency or Tailscale consent check with an unsafe workaround.
4. Run `tailpreview up --json --non-interactive`. Prefer the committed project
   configuration plus worktree-specific `--set` values. Otherwise pass ordered
   `--route` flags, with specific API/WebSocket routes before `/*`.
5. Parse the JSON result instead of scraping human output. Treat non-zero exit
   status or an error object as failure.
6. Request the returned HTTPS URL and verify every required frontend/API route
   through the final Tailpreview origin. A local health check alone is not
   enough. Treat any final `4xx` or `5xx` response as a failed handoff.
7. Confirm `tailscale serve status` describes the listener as available only
   within the tailnet. Return the URL, preview name, expiry, routes, and any
   warnings to the user.

If a development server rejects the MagicDNS hostname, allow only the exact
returned hostname using an ignored local runtime setting supported by that
application. Never set a wildcard host allowlist or commit a machine-specific
hostname without an explicit project decision.

## Lifecycle

Use `list`, `status`, `logs`, `pin`, `unpin`, `down`, and `gc` with `--json`
and `--non-interactive` where supported. Do not remove a preview merely because
the current task ends; leave it available for remote testing unless the user
asks to stop it. `down` may remove only the requested Tailpreview preview.

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
