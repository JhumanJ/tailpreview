# Privacy

Tailpreview runs locally. It has no hosted control plane, analytics service,
telemetry endpoint, account system, or data collection service.

The CLI stores only the local preview registry and privacy-filtered access-log
metadata required for lifecycle management. It does not persist request or
response bodies, headers, cookies, query strings, credentials, or client IP
addresses. See the [security model](docs/security-model.md) for the exact local
state and trust boundaries.

The bundled Codex skill is instruction-only. It does not add a connector or
send data to a Tailpreview service. The agent host and Tailscale remain subject
to their own privacy policies and configuration.

Questions or responsible disclosure reports can be submitted through
[GitHub private vulnerability reporting](https://github.com/JhumanJ/tailpreview/security/advisories/new).
