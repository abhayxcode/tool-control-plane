# Tool Control Plane v1 Milestone

This checklist defines the first standalone v1 boundary for Tool Control Plane. v1 should be useful without Majdoor: an agent or service can discover capabilities, request governed tool execution, require approvals for risky actions, audit decisions, and use GitHub-backed code/CI tools.

## v1 Goal

Ship a governed tool gateway for AI agents with:

- stable HTTP API
- OpenAPI contract
- typed Go client
- provider adapter boundary
- policy and approval enforcement
- audit trail
- local durable storage
- operational controls for auth, rate limiting, request IDs, and graceful shutdown
- mock provider for local demos
- GitHub provider for code-host and CI workflows

## Current v1 Status

Implemented:

- Capability registry with risk/provider metadata
- Static policy layer with `allowed`, `denied`, `invalid`, `approval_required`, and `approved_executed` decisions
- Approval lifecycle:
  - create pending approval
  - list/get approvals
  - grant/deny approvals
  - execute granted approvals once
- Audit entries for tool decisions and approved executions
- In-memory store
- SQLite store for local durable state
- Provider adapter interface
- Mock provider
- GitHub provider:
  - `code_host.get_recent_changes`
  - `code_host.create_draft_pr`
  - `ci.get_checks`
  - `ci.get_logs`
- Request validation for required metadata and capability-specific arguments
- OpenAPI contract at `api/openapi.yaml`
- `GET /openapi.yaml`
- Typed Go client package
- Optional bearer-token authentication
- Optional fixed-window rate limiting
- Request IDs and structured access logs
- Graceful shutdown

## v1 Completion Criteria

- All current tests pass with `go test ./...`
- README documents local run, config, API surface, GitHub provider usage, and storage modes
- OpenAPI contract matches server routes
- Go client can call health, capabilities, tool calls, audit, approvals, approval decisions, approval execution, and OpenAPI
- SQLite mode persists audit and approvals across restarts
- GitHub adapter tests cover request shape and response normalization through fake HTTP servers
- Dangerous tool actions remain behind policy and approval boundaries

## Known v1 Limitations

- Policy engine is static; no per-org or per-environment policy files yet
- Approval store is local only; no external approval notification system yet
- SQLite store is intended for local durable dev mode, not HA production
- GitHub draft PR creation expects the head branch to already exist
- GitHub provider does not create commits or push branches
- GitHub provider does not yet support deployment tracking
- Rate limiting is in-memory per process
- Authentication is a single bearer token, not multi-tenant identity
- Audit redaction is not implemented yet

## Post-v1 Candidates

- Per-org policy config with environment-specific risk rules
- Approval notifications through Slack, GitHub comments, or webhooks
- Postgres store
- Redis-backed rate limiting
- Deployment provider adapters:
  - GitHub Actions deployments
  - Argo CD
  - Kubernetes
  - Vercel
- Observability adapters:
  - Grafana
  - Datadog
  - Prometheus
  - Sentry
- Ticketing adapters:
  - Linear
  - Jira
- Secret brokering instead of direct provider tokens in process env
- Audit redaction and retention policies
- MCP-compatible tool surface

## Handoff To Org Context Graph

Tool Control Plane now has enough v1 foundation for another service to consume it. The next product should be `org-context-graph`, focused on resolving user intent into:

- service ID
- environment
- repository
- runtime/deployment targets
- observability targets
- ownership metadata
- provider-specific tool arguments

Majdoor should eventually call Org Context Graph first, then call Tool Control Plane with explicit service, environment, capability, action, and arguments.
