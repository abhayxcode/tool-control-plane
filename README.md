# Tool Control Plane

Standalone governed tool gateway for AI agents.

Owns:

- provider adapters
- secrets and token brokering
- policy decisions
- risk levels
- approvals
- audit logs
- redaction

## Phase 0 Status

This repo now exposes the first real Tool Control Plane HTTP boundary for Majdoor. It uses mock adapters and an in-memory audit log while preserving the production API shape.

Tool execution is routed through provider adapters. The current built-in provider is `mock`; future providers such as GitHub, Grafana, Datadog, Kubernetes, and Jira should implement the same adapter boundary instead of changing policy or approval logic.

GitHub adapter skeleton:

- default behavior keeps all capabilities on `mock`
- set `TOOL_CONTROL_PLANE_CODE_PROVIDER=github` to route `code_host.*` and `ci.*` capabilities to the GitHub adapter
- set `GITHUB_TOKEN` before using the GitHub adapter
- optional `GITHUB_API_BASE_URL` supports GitHub Enterprise later
- live GitHub execution is intentionally not implemented yet; the skeleton only establishes the adapter boundary and config path

Planned stack:

- Go service
- Postgres
- Redis for approvals/tasks
- OpenAPI + MCP-compatible tool surface

## Run

```bash
go run ./cmd/server
```

The service listens on `:4100`.

## APIs

- `GET /healthz`
- `GET /v1/capabilities` returns stable capability IDs plus risk/provider metadata.
- `POST /v1/tool-calls`
- `GET /v1/audit`
- `GET /v1/approvals`
- `GET /v1/approvals/{id}`
- `POST /v1/approvals/{id}/grant`
- `POST /v1/approvals/{id}/deny`
- `POST /v1/approvals/{id}/execute`

Capability metadata includes:

- `id`
- `capability`
- `action`
- `risk_level`
- `provider`
- `description`
- `approval_required`

Tool call decisions:

- `allowed`: action is registered and can execute immediately.
- `approval_required`: action is registered but cannot execute until an approval workflow grants it.
- `denied`: action is unknown or blocked by policy.

When a tool call returns `approval_required`, the response includes `approval_request_id`.
Granted approvals can be executed once with `POST /v1/approvals/{id}/execute`.
Pending, denied, missing, and already executed approvals are blocked.

## Test

```bash
go test ./...
```
