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

## Test

```bash
go test ./...
```
