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

v1 milestone checklist: [`docs/v1-milestone.md`](docs/v1-milestone.md)

Tool execution is routed through provider adapters. The current built-in provider is `mock`; future providers such as GitHub, Grafana, Datadog, Kubernetes, and Jira should implement the same adapter boundary instead of changing policy or approval logic.

Audit and approval state is routed through a storage interface. The default implementation is in-memory. SQLite can be enabled for local durable dev mode; Postgres can be added behind the same store boundary later.

SQLite store:

- set `TOOL_CONTROL_PLANE_STORE=sqlite`
- optional `TOOL_CONTROL_PLANE_SQLITE_PATH=/path/to/controlplane.sqlite3`
- if no path is set, the service uses `tool-control-plane.sqlite3` in the current directory

API authentication:

- local dev is open by default
- set `TOOL_CONTROL_PLANE_API_TOKEN` to require `Authorization: Bearer <token>` on all endpoints except `GET /healthz`
- Go clients can pass the same token with `client.WithBearerToken`

Request tracing:

- every HTTP response includes `X-Request-ID`
- callers can supply `X-Request-ID`; otherwise the server generates one
- access logs are emitted as JSON lines with method, path, status, duration, and request ID
- tool-call audit entries include `request_id`

Rate limiting:

- disabled by default
- set `TOOL_CONTROL_PLANE_RATE_LIMIT_PER_MINUTE` to limit requests per bearer token, or per client IP when no bearer token is present
- `GET /healthz` is never rate limited

GitHub adapter:

- default behavior keeps all capabilities on `mock`
- set `TOOL_CONTROL_PLANE_CODE_PROVIDER=github` to route `code_host.*` and `ci.*` capabilities to the GitHub adapter
- set `GITHUB_TOKEN` before using the GitHub adapter
- optional `GITHUB_API_BASE_URL` supports GitHub Enterprise later
- `code_host.get_recent_changes` is implemented against recent merged GitHub pull requests
- `code_host.create_draft_pr` is implemented against GitHub pull request creation and expects the head branch to already exist
- `ci.get_checks` is implemented against GitHub REST check runs
- `ci.get_logs` is implemented for direct `logs_url` and GitHub Actions `job_id` logs

`code_host.get_recent_changes` accepts:

- `repository`: `owner/repo`
- or `owner` and `repo`
- optional `branch`
- optional `limit`, capped at 20

`code_host.create_draft_pr` accepts:

- `repository`: `owner/repo`
- or `owner` and `repo`
- `title`
- `head`, `head_branch`, or `branch`
- optional `base` or `base_branch`, defaulting to `main`
- optional `body`
- optional `draft`, defaulting to `true`

`ci.get_checks` accepts either:

- `repository`: `owner/repo`
- `owner` and `repo`

It also needs one target:

- `ref`
- `commit_sha`
- `sha`
- `head_sha`
- `pr_number`, which resolves the pull request head SHA first

`ci.get_logs` accepts either:

- `logs_url`
- `repository`: `owner/repo` plus `job_id`

The response includes `summary`, `log_excerpt`, `truncated`, `source_url`, and `evidence`.
Log excerpts are bounded to keep agent traces small.

Planned stack:

- Go service
- Postgres
- Redis for approvals/tasks
- OpenAPI + MCP-compatible tool surface

## Run

```bash
go run ./cmd/server
```

The service listens on `:4100` by default. Set `TOOL_CONTROL_PLANE_ADDR` to override the bind address.

Configuration:

- `TOOL_CONTROL_PLANE_ADDR`
- `TOOL_CONTROL_PLANE_SHUTDOWN_TIMEOUT`
- `TOOL_CONTROL_PLANE_API_TOKEN`
- `TOOL_CONTROL_PLANE_RATE_LIMIT_PER_MINUTE`
- `TOOL_CONTROL_PLANE_STORE`
- `TOOL_CONTROL_PLANE_SQLITE_PATH`
- `TOOL_CONTROL_PLANE_CODE_PROVIDER`
- `GITHUB_TOKEN`
- `GITHUB_API_BASE_URL`

## APIs

OpenAPI contract: [`api/openapi.yaml`](api/openapi.yaml)
Go client package: [`client`](client)

- `GET /openapi.yaml`
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
- `invalid`: action is registered, but the request is missing required metadata or arguments.

Validation currently checks common request metadata plus capability-specific arguments for draft PR creation, rollback approvals, and GitHub CI reads.

When a tool call returns `approval_required`, the response includes `approval_request_id`.
Granted approvals can be executed once with `POST /v1/approvals/{id}/execute`.
Pending, denied, missing, and already executed approvals are blocked.

## Test

```bash
go test ./...
```
