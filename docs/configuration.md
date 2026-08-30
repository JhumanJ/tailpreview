# Configuration reference

Tailpreview discovers `.tailpreview.yml` by walking from the current directory
to the Git root. It never searches above the Git root. Without a file, provide
all routes on the command line.

## Schema

```yaml
version: 1
name: optional-preview-name

routes:
  - path: /api/*
    upstream: ${API_URL}
    strip_prefix: false
    insecure_skip_verify: false

  - path: /*
    upstream: ${FRONTEND_URL}

health:
  - ${FRONTEND_URL}
  - url: ${API_URL}/health
    min_code: 200
    max_code: 399
    insecure_skip_verify: false

ttl:
  idle: 24h
  max_age: 168h
```

Unknown YAML fields are rejected. Missing `${VARIABLE}` values fail before any
Caddy or Tailscale change.

## Routes

Routes are evaluated in declaration order. Every configuration must finish
with `/` or `/*`.

`upstream` must be an HTTP(S) URL whose host is loopback:

```text
http://localhost:3000
http://127.0.0.1:3000
http://[::1]:3000
```

Credentials and remote IPs are rejected. Paths are preserved unless
`strip_prefix: true` is declared.

Caddy proxies WebSocket upgrades without a separate route type. Declaring a
path such as `/ws/*` is enough when it points to the WebSocket server.

## Health checks

If `health` is omitted, Tailpreview probes every unique route upstream and
accepts `100–499`, which proves that the local HTTP server is reachable without
requiring a project-specific endpoint.

A scalar check accepts `200–399`:

```yaml
health:
  - ${API_URL}/health
```

Mapping form configures a status range. Redirects are not followed outside the
declared URL. HTTPS certificates remain verified unless
`insecure_skip_verify` is explicitly enabled for a loopback URL.

Tailpreview waits up to 30 seconds before failing. `--skip-health` is available
for deliberate troubleshooting, but final tailnet URL verification still
runs.

## Variables and overrides

`${NAME}` placeholders use `--set NAME=value` first, then the current process
environment. Values are resolved before YAML parsing and validation.

Any config file can be replaced entirely:

```bash
tailpreview up \
  --name my-preview \
  --route '/api/*=http://127.0.0.1:3001' \
  --route '/*=http://127.0.0.1:3000' \
  --health http://127.0.0.1:3001/health \
  --ttl 12h \
  --max-age 72h
```

Advanced route and health fields also have CLI equivalents:

```bash
tailpreview up \
  --route '/api/*=https://127.0.0.1:3001' \
  --insecure-upstream '/api/*' \
  --strip-prefix '/api/*' \
  --route '/*=http://127.0.0.1:3000' \
  --health https://127.0.0.1:3001/health \
  --health-range 'https://127.0.0.1:3001/health=200-499' \
  --insecure-health https://127.0.0.1:3001/health \
  --optional-health http://127.0.0.1:3000/ready
```

`--insecure-upstream` selects a configured route by path.
`--insecure-health` and `--health-range` select a configured health check by
its exact URL. These flags are explicit because disabling TLS verification is
never inferred.

## Port pool

The default HTTPS pool is `8443–8452`. Tailpreview associates the absolute
worktree path with its preferred port for 30 days. A requested port must fall
inside the configured pool and must be free in both local Caddy and current
Tailscale Serve state.

The public CLI will expose global pool configuration before the first stable
release. Until then, the default pool is fixed deliberately.
