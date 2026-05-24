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

Tool execution is routed through provider adapters. Current built-in providers are `mock`, `github`, `sentry`, and `prometheus`; future providers such as Datadog, Kubernetes, and Jira should implement the same adapter boundary instead of changing policy or approval logic.

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
- set `TOOL_CONTROL_PLANE_DEPLOY_PROVIDER=github` to route `deploy.get_recent_deploys` to the GitHub adapter
- set `GITHUB_TOKEN` before using the GitHub adapter, or configure GitHub App auth with `GITHUB_APP_ID`, `GITHUB_APP_INSTALLATION_ID`, and `GITHUB_APP_PRIVATE_KEY`/`GITHUB_APP_PRIVATE_KEY_PATH`
- optional `GITHUB_API_BASE_URL` supports GitHub Enterprise later
- optional `TOOL_CONTROL_PLANE_GITHUB_MAX_ATTEMPTS` controls retry attempts for retryable GitHub read requests, default `3`
- optional `TOOL_CONTROL_PLANE_GITHUB_RETRY_BACKOFF` controls linear retry backoff, default `200ms`
- `code_host.get_recent_changes` is implemented against recent merged GitHub pull requests
- `code_host.get_file` is implemented against the GitHub Contents API and returns decoded text content for patch planning
- `code_host.get_pull_request` is implemented against GitHub pull request details and returns merge state plus head/base metadata
- `code_host.create_draft_pr` is implemented against GitHub pull request creation; when `files` are provided it creates the head branch from the base branch and upserts file contents before opening the PR
- `code_host.update_pull_request` is implemented against an existing GitHub pull request; when `files` are provided it writes to the PR head branch and can add a PR comment
- `code_host.mark_ready_for_review` is implemented against GitHub GraphQL and converts an existing draft pull request to ready-for-review
- `ci.get_checks` is implemented against GitHub REST check runs
- `ci.get_checks` also attempts to discover the failed GitHub Actions job and includes `job_id`/`logs_url` when available
- `ci.get_logs` is implemented for direct `logs_url` and GitHub Actions `job_id` logs
- `deploy.get_recent_deploys` is implemented against GitHub Actions workflow runs

Sentry adapter:

- default behavior keeps error capabilities on `mock`
- set `TOOL_CONTROL_PLANE_ERRORS_PROVIDER=sentry` to route `errors.get_recent_errors` to the Sentry adapter
- set `SENTRY_AUTH_TOKEN` before using the Sentry adapter; the token needs Sentry `event:read` access for the selected org/project
- optional `SENTRY_ORG` and `SENTRY_PROJECT` provide defaults when tool calls do not include organization/project arguments
- optional `SENTRY_BASE_URL` supports self-hosted Sentry later
- `errors.get_recent_errors` is implemented against the Sentry project issues API and returns normalized service status, top errors, evidence, and source URLs

Prometheus adapter:

- default behavior keeps metrics capabilities on `mock`
- set `TOOL_CONTROL_PLANE_METRICS_PROVIDER=prometheus` to route `metrics.get_service_health` to the Prometheus adapter
- set `PROMETHEUS_BASE_URL`, for example `http://localhost:9090`
- optional `PROMETHEUS_BEARER_TOKEN` is sent as a bearer token for authenticated Prometheus-compatible gateways
- optional `PROMETHEUS_SERVICE_LABEL`, `PROMETHEUS_ENVIRONMENT_LABEL`, and `PROMETHEUS_STATUS_LABEL` customize the default label matchers
- `metrics.get_service_health` queries Prometheus instant queries for `up`, p95 latency, and error-rate signals, then returns normalized service status, thresholds, evidence, and source URLs

Demo provider configs:

- `examples/demo.mock.env` keeps all code, CI, deployment, errors, and metrics calls on mock providers.
- `examples/demo.github.env.example` documents the real GitHub and optional Sentry/Prometheus provider variables. Copy it to a private ignored file before adding credentials.

`GET /v1/capabilities` includes a safe `provider_config` block with selected code/deploy/errors/metrics providers, GitHub auth mode, whether token/App credentials are configured, Sentry and Prometheus readiness flags, GitHub retry settings, store mode, readiness, and warnings. It intentionally does not return secret values.

`GET /v1/readiness` returns the same non-secret provider readiness plus capability count, store/auth/rate-limit checks, optional demo repository access check, and blockers. Set `TOOL_CONTROL_PLANE_DEMO_REPOSITORY=owner/repo` to let readiness verify that the configured GitHub token/App can read the pushed demo repository. Majdoor uses this endpoint for demo and internal-alpha preflight.

From the workspace root, the local demo runner can load a provider env file:

```bash
MAJDOOR_DEMO_ENV_FILE=tool-control-plane/examples/demo.mock.env ./scripts/run-local-demo.sh
```

Real GitHub mode:

```bash
MAJDOOR_DEMO_ENV_FILE=tool-control-plane/examples/demo.github.env ./scripts/run-local-demo.sh
```

`code_host.get_recent_changes` accepts:

- `repository`: `owner/repo`
- or `owner` and `repo`
- optional `branch`
- optional `limit`, capped at 20

`code_host.get_file` accepts:

- `repository`: `owner/repo`
- or `owner` and `repo`
- `path`: relative repository file path
- optional `ref`, `branch`, or `base`, defaulting to the provider default branch

`code_host.get_pull_request` accepts:

- `repository`: `owner/repo`
- or `owner` and `repo`
- `pr_number` or `number`

It returns PR metadata including `state`, `merged`, `merged_at`, `merge_commit_sha`, `branch`, `base`, `head_sha`, `url`, and `source_url` when the provider includes those values.

`code_host.create_draft_pr` accepts:

- `repository`: `owner/repo`
- or `owner` and `repo`
- `title`
- `head`, `head_branch`, or `branch`
- optional `base` or `base_branch`, defaulting to `main`
- optional `body`
- optional `draft`, defaulting to `true`
- optional `commit_message`, used when writing files
- optional `files` as `{ "path/to/file": "content" }` or `[{ "path": "path/to/file", "content": "content" }]`
- optional `file_path` plus `file_content` for a single file
- optional `reviewers` as GitHub usernames
- optional `team_reviewers` as GitHub team slugs
- optional `labels` as issue label names

It returns PR metadata including `pr_number`, `repository`, `branch`, `base`, `head_sha`, `url`, `source_url`, and best-effort reviewer/label routing metadata when GitHub includes those values.

`code_host.update_pull_request` accepts:

- `repository`: `owner/repo`
- or `owner` and `repo`
- `pr_number` or `number`
- optional `commit_message`, used when writing files
- optional `comment`, posted as a pull request comment
- optional `files` as `{ "path/to/file": "content" }` or `[{ "path": "path/to/file", "content": "content" }]`
- optional `file_path` plus `file_content` for a single file
- optional `reviewers`, `team_reviewers`, and `labels` to refresh PR routing

It returns updated PR metadata including `pr_number`, `repository`, `branch`, `base`, `head_sha`, `url`, `source_url`, `comment_url`, and best-effort routing metadata when available.

`code_host.mark_ready_for_review` accepts:

- `repository`: `owner/repo`
- or `owner` and `repo`
- `pr_number` or `number`

It returns updated PR metadata including `pr_number`, `repository`, `state`, `merged`, `branch`, `base`, `head_sha`, `url`, `source_url`, `draft`, and `ready_for_review`. Callers should enforce their own policy gates before invoking this write action, for example “CI passed” plus explicit human approval.

`ci.get_checks` accepts either:

- `repository`: `owner/repo`
- `owner` and `repo`

It also needs one target:

- `ref`
- `commit_sha`
- `sha`
- `head_sha`
- `pr_number`, which resolves the pull request head SHA first

When checks fail, the response may include top-level `job_id`, `logs_url`, and `failed_job` fields. Matching failed check entries may also include `job_id` and `logs_url`, allowing callers to fetch logs without provider-specific discovery.

`ci.get_logs` accepts either:

- `logs_url`
- `repository`: `owner/repo` plus `job_id`

The response includes `summary`, `log_excerpt`, `truncated`, `source_url`, and `evidence`.
Log excerpts are bounded to keep agent traces small.

`deploy.get_recent_deploys` accepts:

- `repository`: `owner/repo`
- or `owner` and `repo`
- optional `workflow` or `workflow_id`, such as `deploy-backend.yml`
- optional `branch`
- optional `commit_sha`, `sha`, or `head_sha`
- optional `limit`, capped at 20

The GitHub response includes normalized deployment `status`, workflow run `deploys`, `source_url`, and evidence.

`errors.get_recent_errors` accepts:

- `organization`, `organization_slug`, or `org`, defaulting to `SENTRY_ORG`
- `project` or `project_slug`, defaulting to `SENTRY_PROJECT`
- optional `environment` or `env`, appended to the query when the query does not already include `environment:`
- optional `query`, defaulting to `is:unresolved`
- optional `stats_period` or `statsPeriod`, defaulting to `24h`
- optional `sort`, defaulting to `freq`
- optional `limit`, capped at 20

The Sentry response includes normalized `status`, `top_errors`, `source_url`, and evidence. The adapter currently uses Sentry's documented project issues endpoint: `GET /api/0/projects/{organization_id_or_slug}/{project_id_or_slug}/issues/`.

`metrics.get_service_health` accepts:

- optional `service` or `service_id`, defaulting to the request `service_id`
- optional `environment` or `env`, defaulting to the request `environment`
- optional `window` or `range`, defaulting to `5m`
- optional `service_label`, `environment_label`, and `status_label`
- optional `request_metric`, defaulting to `http_requests_total`
- optional `duration_bucket_metric`, defaulting to `http_request_duration_seconds_bucket`
- optional `up_query`, `latency_p95_query`, and `error_rate_query` for full PromQL override
- optional `latency_unit`, defaulting to seconds for p95 latency query output; set to `ms` when a custom query already returns milliseconds
- optional `up_threshold`, `latency_p95_ms_threshold`, and `error_rate_percent_threshold`

The Prometheus response includes normalized `status`, `up`, `latency_p95_ms`, `error_rate_percent`, query metadata, sample counts, thresholds, `source_url`, and evidence when data is available. The adapter uses Prometheus's stable HTTP API under `/api/v1`, specifically instant queries at `GET /api/v1/query`.

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
- `TOOL_CONTROL_PLANE_DEPLOY_PROVIDER`
- `TOOL_CONTROL_PLANE_ERRORS_PROVIDER`
- `TOOL_CONTROL_PLANE_METRICS_PROVIDER`
- `TOOL_CONTROL_PLANE_GITHUB_MAX_ATTEMPTS`
- `TOOL_CONTROL_PLANE_GITHUB_RETRY_BACKOFF`
- `GITHUB_TOKEN`
- `GITHUB_APP_ID`
- `GITHUB_APP_INSTALLATION_ID`
- `GITHUB_APP_PRIVATE_KEY`
- `GITHUB_APP_PRIVATE_KEY_PATH`
- `GITHUB_API_BASE_URL`
- `SENTRY_AUTH_TOKEN`
- `SENTRY_ORG`
- `SENTRY_PROJECT`
- `SENTRY_BASE_URL`
- `PROMETHEUS_BASE_URL`
- `PROMETHEUS_BEARER_TOKEN`
- `PROMETHEUS_SERVICE_LABEL`
- `PROMETHEUS_ENVIRONMENT_LABEL`
- `PROMETHEUS_STATUS_LABEL`

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

Validation currently checks common request metadata plus capability-specific arguments for draft PR creation, PR updates, rollback approvals, and GitHub CI reads.

When a tool call returns `approval_required`, the response includes `approval_request_id`.
Granted approvals can be executed once with `POST /v1/approvals/{id}/execute`.
Pending, denied, missing, and already executed approvals are blocked.

## Test

```bash
go test ./...
```
