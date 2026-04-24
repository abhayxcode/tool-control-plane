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

This repo is scaffolded for later extraction. Phase 1 local demo uses an in-process mock inside `../claude-tag`.

Planned stack:

- Go service
- Postgres
- Redis for approvals/tasks
- OpenAPI + MCP-compatible tool surface

## First API Targets

- `GET /healthz`
- `GET /v1/capabilities`
- `POST /v1/tool-calls`
- `GET /v1/audit`
