# Security model

## Intended use

Tailpreview exposes development HTTP services from a machine already enrolled
in a Tailscale tailnet to other devices permitted by that tailnet. Tailscale
provides peer authentication, encryption, DNS, and HTTPS termination.

Tailpreview is not an application authentication layer and is not designed to
publish services to the public Internet.

## Trust boundaries

Tailpreview trusts:

- the local operating-system account executing it;
- the installed Tailscale and Caddy binaries;
- the user's existing tailnet access policy;
- local services explicitly declared by the caller.

It does not trust project YAML to select arbitrary network targets. Strict
parsing and loopback validation happen before side effects.

## Enforced invariants

- only `http` and `https` loopback upstreams;
- no credentials embedded in upstream URLs;
- no Funnel command or public endpoint creation;
- no Tailscale ACL, grant, tag, key, identity, or device mutation;
- no global Tailscale Serve reset;
- no overwrite of an existing, non-Tailpreview Serve port;
- no successful handoff until every declared public path resolves through the
  exact Tailpreview HTTPS origin;
- no redirect handoff to loopback, another origin, or downgraded HTTP;
- no query strings or fragments in stored verification targets;
- dedicated Caddy state and a permission-restricted Unix admin socket;
- no application process, container, worktree, or data lifecycle management;
- no sensitive HTTP request material in retained logs.

## Access logs

Access logs exist only to compute `last_used_at` for expiry and LRU eviction.
Caddy filters request and response headers, cookies,
authorization, client IPs, ports, and query strings before writing. Bodies are
not logged. Files use mode `0600`, rotate after 24 hours or 10 MB, and rolled
segments are kept for one day. All segments are deleted immediately when a
preview is removed or evicted. The registry retains only the last-use timestamp.

## Transaction model

Tailpreview serializes mutations and validates candidate Caddy JSON before
loading it. Caddy's `/load` operation is atomic. Because Caddy and Tailscale do
not share one transaction, Tailpreview uses compensating rollback:

1. preserve old registry and Caddy state;
2. apply candidate Caddy state;
3. configure the exact Tailscale Serve port;
4. verify every declared public path through the final HTTPS origin;
5. persist the new registry;
6. restore prior Caddy and Serve state if a later step fails.

Tailpreview never calls `tailscale serve reset` during rollback.

## Safe handoff boundary

Local health and final-origin verification answer different questions. A local
check proves that an already-running process is reachable on loopback. It does
not prove that the MagicDNS hostname is accepted, Caddy routes the public path,
or the application keeps navigation and authentication on the preview origin.

Before `up` succeeds, Tailpreview requests every configured `verify` path over
the exact Tailscale HTTPS origin. Redirects may proceed for at most five hops
and only when scheme, hostname, and effective port remain identical. A
terminal redirect, loopback redirect, cross-origin redirect, HTTPS downgrade,
unexpected status, or timeout aborts the handoff and triggers compensating
rollback.

`tailpreview check` repeats the local and final checks for a registered
preview. It also confirms the current MagicDNS hostname, the exact Serve port,
and that Funnel is not enabled on that port. It does not modify unrelated
Serve or Funnel configuration.

Tailpreview reports only the rejected origin for unsafe redirects. Paths,
queries, fragments, credentials, cookies, headers, bodies, and client IPs are
excluded from stored reports and structured errors.

Application-specific origin configuration stays outside Tailpreview. For
example, a Vite hostname allowlist, OAuth callback, or WebAuthn relying-party
origin must be set by the application's own ignored runtime configuration.
Tailpreview diagnoses the mismatch and gives bounded remediation; it never
rewrites application startup settings or broadens a hostname allowlist.

## Known limitation: cookies across ports

HTTP cookies are scoped by hostname rather than port. Two authenticated
worktrees using the same cookie names can interfere. Tailpreview warns when it
detects multiple previews from the same project, but framework-specific cookie
configuration remains the application's responsibility.

Rewriting arbitrary cookies at the reverse proxy is intentionally excluded:
it would break CSRF libraries and JavaScript cookie access in unpredictable
ways. Distinct Tailscale Service hostnames are a possible future backend.
