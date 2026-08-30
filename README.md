# Tailpreview

[![CI](https://github.com/JhumanJ/tailpreview/actions/workflows/ci.yml/badge.svg)](https://github.com/JhumanJ/tailpreview/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/JhumanJ/tailpreview)](https://github.com/JhumanJ/tailpreview/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Private, temporary previews for already-running localhost services, powered by
[Tailscale Serve](https://tailscale.com/docs/features/tailscale-serve) and a
dedicated [Caddy](https://caddyserver.com/) reverse proxy.

Tailpreview is designed for remote development and coding agents. Start your
application however you already do, then expose its frontend, API, and
WebSocket routes through one HTTPS origin that is reachable only inside your
tailnet.

```text
https://dev-mini.example.ts.net:8443
                    │
                    ▼
       Tailpreview-managed Caddy
          ├── /*       → frontend
          ├── /api/*   → API
          └── /ws/*    → WebSocket server
```

Tailpreview never starts your application, never opens router ports, never
modifies tailnet policy, and never invokes Tailscale Funnel.

## How the private HTTPS URL works

[Tailscale Serve](https://tailscale.com/docs/features/tailscale-serve) publishes
a local service on the machine's MagicDNS name and provisions HTTPS with a
certificate managed by Tailscale. The resulting URL is reachable only by
devices that already have access to that machine through the same tailnet.

Tailpreview places a dedicated Caddy instance between Tailscale Serve and your
already-running services. This gives every preview one HTTPS origin while
still routing paths to different localhost ports:

```text
Phone or laptop on your tailnet
              │
              │ HTTPS through Tailscale Serve
              ▼
https://dev-mini.example.ts.net:8443
              │
              ▼
      Tailpreview-managed Caddy
         ├── /api/* → 127.0.0.1:30723
         ├── /ws/*  → 127.0.0.1:30724
         └── /*     → 127.0.0.1:36723
```

This is deliberately different from
[Tailscale Funnel](https://tailscale.com/kb/1223/funnel): Funnel exposes a
service to the public Internet, while Tailpreview only uses the tailnet-private
Serve path. Tailpreview contains no Funnel command or fallback.

## Why

Remote development often ends with a local stack running on a remote machine
and no safe, repeatable way to test it from a phone or laptop. Ad-hoc port
binding exposes too much, URLs drift between worktrees, and agents need a
machine-readable lifecycle.

Tailpreview provides:

- one private HTTPS origin per preview;
- multiple ordered routes for frontend, API, assets, and WebSockets;
- ten concurrent previews by default on ports `8443–8452`;
- stable port reuse per worktree;
- 24-hour idle expiry and a seven-day maximum lifetime;
- LRU eviction of the oldest inactive preview when the pool is full;
- pinning for previews that must not expire;
- atomic state, Caddy rollback, and safe concurrent agent calls;
- strict localhost-only upstream validation;
- stable JSON output and non-interactive commands;
- minimal privacy-filtered access logs for TTL/LRU tracking.

## Requirements

- macOS or Linux;
- Tailscale installed, connected, and available as `tailscale`;
- MagicDNS and HTTPS certificates enabled for the tailnet;
- Caddy 2 installed and available as `caddy`;
- the local service(s) already running on loopback addresses.

Tailpreview supports Tailscale Serve's private network behavior. It does not
make a preview reachable from the public Internet.

## Install

### macOS with Homebrew

Homebrew installs Caddy as a dependency and links `tailpreview` into the
Homebrew `bin` directory automatically. Tailpreview is distributed as a
formula so command-line installation does not require bypassing macOS
Gatekeeper:

```bash
brew install jhumanj/tap/tailpreview
tailpreview doctor
tailpreview service install
```

Confirm which binary your shell resolves:

```bash
command -v tailpreview
tailpreview version
```

On Apple Silicon the linked binary is normally
`/opt/homebrew/bin/tailpreview`; on Intel macOS it is normally
`/usr/local/bin/tailpreview`. If `brew` itself works but a newly installed
command is not found, initialize Homebrew in the login shell and open a new
terminal:

```bash
echo 'eval "$(/opt/homebrew/bin/brew shellenv)"' >> ~/.zprofile
eval "$(/opt/homebrew/bin/brew shellenv)"
```

Use `/usr/local/bin/brew` instead on Intel macOS.

Upgrade or uninstall with:

```bash
brew upgrade tailpreview
brew uninstall tailpreview
```

### Linux and manual installation

Linux `amd64` and `arm64` archives are attached to every
[GitHub release](https://github.com/JhumanJ/tailpreview/releases). Extract the
archive and copy `tailpreview` to a directory already on `PATH`, such as
`/usr/local/bin` or `~/.local/bin`. Install Caddy and Tailscale separately using
their platform documentation.

`service install` registers a five-minute garbage-collection timer using a
user LaunchAgent on macOS or a user systemd timer on Linux. It does not install
or modify Tailscale.

The first Serve use on a tailnet may require one-time consent from a Tailscale
admin. Tailpreview never hides or automates that approval: it exits cleanly,
rolls back Caddy, and tells you to complete the consent interactively once.
The private test command it reports is followed by an exact-port `off` command,
so no test listener needs to remain configured.

## Quick start

### One local service

Start the app first, then expose it:

```bash
tailpreview up http://127.0.0.1:3000
```

Tailpreview returns a URL similar to:

```text
Preview ready: https://dev-mini.example.ts.net:8443
```

Flags may appear before or after the positional upstream:

```bash
tailpreview up http://127.0.0.1:3000 --name landing-redesign --ttl 8h
```

### Frontend and API under one origin

```bash
tailpreview up \
  --name opnform-pr-1281 \
  --route '/api/*=http://127.0.0.1:30723' \
  --route '/*=http://127.0.0.1:36723' \
  --health http://127.0.0.1:30723/health
```

Route order is preserved. Paths are preserved by default. Add
`--strip-prefix '/api/*'` only when the upstream expects `/users` instead of
`/api/users`.

### Project configuration

Commit `.tailpreview.yml` when a project has repeatable routing:

```yaml
version: 1

routes:
  - path: /api/*
    upstream: ${API_URL}

  - path: /*
    upstream: ${FRONTEND_URL}

health:
  - ${API_URL}/health

ttl:
  idle: 24h
  max_age: 168h
```

Then an agent supplies worktree-specific values:

```bash
tailpreview up \
  --set API_URL=http://127.0.0.1:30723 \
  --set FRONTEND_URL=http://127.0.0.1:36723 \
  --name opnform-pr-1281 \
  --json --non-interactive
```

Every YAML field has a CLI equivalent, including route prefix stripping,
optional health checks, status ranges, and explicit loopback TLS exceptions.
The precedence is:

```text
CLI flags → --set/environment variables → .tailpreview.yml → defaults
```

See [configuration reference](docs/configuration.md).

## Lifecycle

```bash
tailpreview list
tailpreview status opnform-pr-1281
tailpreview logs opnform-pr-1281
tailpreview pin opnform-pr-1281
tailpreview unpin opnform-pr-1281
tailpreview down opnform-pr-1281
tailpreview gc
```

`down` and automatic garbage collection remove only the Tailpreview-owned
Tailscale Serve listener, Caddy route, and temporary access log. Application
processes, containers, worktrees, source files, databases, and application logs
are never touched.

If all ten slots are used, the least recently requested unpinned preview is
evicted. A preview with an active request or WebSocket is never selected. If
all previews are pinned or active, creation fails without changing anything.

## Agent usage

All lifecycle commands support stable JSON:

```bash
tailpreview up http://127.0.0.1:3000 --json --non-interactive
tailpreview list --json
tailpreview doctor --json
```

The JSON includes `schema_version`, preview ID, URL, local routes, status,
timestamps, TTL, and eviction details. See the
[agent contract](docs/agent-contract.md) for command semantics and exit codes.

### Install the Tailpreview skill in Codex

The repository includes a small instruction-only Codex plugin. It teaches an
agent how to discover project routes, invoke Tailpreview non-interactively,
verify the final frontend/API URL, and preserve Tailpreview's security
boundaries. The binary remains the source of truth for lifecycle behavior.

Add this repository as a plugin marketplace, then install the plugin:

```bash
codex plugin marketplace add JhumanJ/tailpreview
codex plugin add tailpreview@tailpreview
```

Restart Codex or start a new task, then invoke it explicitly with
`$tailpreview` or ask naturally to expose an already-running local project over
Tailscale. Codex can also select the skill automatically when the request
matches its description.

For standalone local authoring, Codex also discovers user skills under
`~/.agents/skills`. You can symlink the bundled skill without copying it:

```bash
mkdir -p ~/.agents/skills
ln -s "$PWD/plugins/tailpreview/skills/tailpreview" ~/.agents/skills/tailpreview
```

Do not install both forms unless you intentionally want both skill entries.

## Cookies and multiple worktrees

Browsers scope cookies by hostname, not port. Most projects require no special
handling, and Tailpreview does not block or prompt. If two authenticated
worktrees of the same application use identical cookie names, their sessions
can interfere. Tailpreview emits a one-time contextual warning when it sees
multiple previews from the same project.

If interference occurs, give each worktree a unique application cookie name —
for example, Laravel's `SESSION_COOKIE`. This remains application-specific and
is intentionally not hidden behind unsafe proxy rewriting. A future optional
Tailscale Services backend can provide one hostname per preview without
changing project manifests.

## Security model

Tailpreview's v1 boundaries are deliberately narrow:

- upstreams must be `localhost`, `127.0.0.1`, or `::1`;
- upstream URLs cannot contain credentials;
- HTTPS verification is required unless loopback-only
  `insecure_skip_verify` is explicitly set;
- Tailscale Serve is the only exposure mechanism;
- Funnel commands do not exist in the codebase;
- existing Serve ports are detected and never overwritten;
- `tailscale serve reset` is never used;
- the Caddy admin API uses a private Unix socket;
- the dedicated Caddy instance does not touch an existing Caddy service;
- tailnet ACLs, tags, identity, and key expiry are diagnostic-only;
- request/response headers, cookies, bodies, query strings, and client IPs are
  excluded from access logs.

Read [the full security model](docs/security-model.md) and report issues using
[SECURITY.md](SECURITY.md).

## Development

```bash
PATH=/opt/homebrew/bin:$PATH make check
```

The test suite uses fakes for Tailscale and never mutates the real tailnet. If
Caddy is installed, integration tests start a dedicated temporary Caddy
instance, proxy local test servers, validate privacy-filtered logs, and stop it
afterward.

See [development guide](docs/development.md) and [AGENTS.md](AGENTS.md).

## License

Tailpreview is open source under the [MIT License](LICENSE). You may use,
modify, redistribute, and sell software containing it subject to the license
notice and warranty disclaimer.
