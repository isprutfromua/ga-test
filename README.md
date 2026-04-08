# GitHub Release Notifier

Go service that tracks GitHub repositories and emails subscribers when a new stable release is published.

Users can subscribe from a static web UI or via HTTP API, confirm via tokenized email link, and later unsubscribe with a dedicated tokenized link.

## 1. Project Overview

The application runs as a single Go service with:

- HTTP API and static frontend served by the same process.
- PostgreSQL for subscription persistence.
- Redis for GitHub API response caching.
- SMTP integration for confirmation and notification emails.
- Background scanner that polls GitHub releases on an interval.
- Prometheus metrics endpoint for observability.

Main runtime flow:

1. User submits email + repository.
2. Service validates repo format and existence on GitHub.
3. Subscription is persisted as unconfirmed and confirmation email is queued.
4. User confirms via token link.
5. Scanner periodically checks confirmed subscriptions and sends notification emails for new stable tags.

## 2. Tech Stack

- Language: Go 1.23
- HTTP Router/Middleware: chi v5
- Database: PostgreSQL 16 (in local compose)
- DB Driver: pgx stdlib
- Cache: Redis 7 (in local compose)
- Metrics: Prometheus Go client
- Mail testing (local): Mailpit
- Containerization: Docker multi-stage build + distroless runtime image
- CI: GitHub Actions workflow at .github/workflows/ci.yml

## 3. Project Structure

Current repository tree (relevant files):

```text
.
├── .github/
│   └── workflows/
│       └── ci.yml
├── cmd/
│   └── server/
│       └── main.go
├── internal/
│   ├── api/
│   │   ├── contract_test.go
│   │   ├── handler.go
│   │   ├── handler_test.go
│   │   ├── middleware.go
│   │   └── router.go
│   ├── cache/
│   │   └── redis.go
│   ├── config/
│   │   └── config.go
│   ├── db/
│   │   ├── db.go
│   │   └── migrations/
│   │       └── 000001_initial.up.sql
│   ├── github/
│   │   ├── client.go
│   │   └── client_test.go
│   ├── mailer/
│   │   ├── mailer.go
│   │   └── mailer_test.go
│   ├── metrics/
│   │   └── metrics.go
│   ├── models/
│   │   └── models.go
│   ├── repository/
│   │   ├── subscription.go
│   │   └── subscription_test.go
│   ├── scanner/
│   │   ├── scanner.go
│   │   └── scanner_test.go
│   └── service/
│       ├── subscription.go
│       └── subscription_test.go
├── static/
│   ├── error.html
│   ├── index.html
│   └── subscription.html
├── .env.example
├── Dockerfile
├── README.md
├── docker-compose.yml
├── go.mod
├── go.sum
├── handler.go
├── index.html
├── scanner.go
└── subscription.go
```

Module responsibilities:

- cmd/server: application bootstrap and graceful shutdown.
- internal/config: environment loading, defaults, and required variable checks.
- internal/db: database connection setup and SQL migration execution on startup.
- internal/repository: PostgreSQL data access for subscriptions.
- internal/github: GitHub API client, repo validation, and release lookup with Redis cache.
- internal/service: business logic, token generation, confirmation mail queue workers.
- internal/scanner: periodic release scanning with bounded worker pool.
- internal/api: HTTP handlers, auth middleware, metrics middleware, route wiring.
- internal/mailer: SMTP email delivery for confirmation and release notifications.
- internal/metrics: Prometheus instruments registration.
- static: browser UI pages and state pages.

Note on root files:

- handler.go, scanner.go, subscription.go at repository root are marked with go:build ignore and are not compiled into runtime binaries.

## 4. Installation and Setup

Prerequisites:

- Docker
- Docker Compose plugin

Local setup:

```bash
cp .env.example .env
docker compose up --build
```

Default local endpoints:

- App: http://localhost:8080
- Health: http://localhost:8080/healthz
- Metrics: http://localhost:8080/metrics
- Mailpit UI: http://localhost:8025

Deployed website (Production):

- App: https://ga-test-app-82466e574c85.herokuapp.com/
- Metrics: https://ga-test-app-82466e574c85.herokuapp.com/metrics
- Swagger Editor: https://editor.swagger.io/?url=https://raw.githubusercontent.com/isprutfromua/ga-test/main/swagger.yaml

## 5. Development Workflow

Recommended flow:

1. Start dependencies and app with docker compose.
2. Make code changes in internal modules.
3. Run lint and tests locally:

```bash
golangci-lint run --timeout=3m
go test ./...
```

4. Install repository Git hooks (one-time) to run tests before every push:

```bash
git config core.hooksPath .githooks
```

If hooks do not run on push, verify:

```bash
git config --get core.hooksPath
```

Expected: `.githooks`

5. Run focused contract checks before opening PR:

```bash
go test ./internal/api -run 'TestSwaggerContractStatusMatrix|TestAuthBoundaries'
```

6. Open PR and let GitHub Actions run the same checks.

Using the deployed website:

1. Open https://ga-test-app-82466e574c85.herokuapp.com/
2. On first visit, you will be prompted to enter the API key (from your Heroku app config).
   - The key is stored securely in your browser cookie and persists across sessions.
   - You can update it anytime using the "Set API key" button near the lookup section.
3. Submit your email and GitHub repository in owner/repo format.
4. Open the confirmation email and follow the confirmation link.
5. Use the "Your subscriptions" lookup to view and manage subscriptions — requires a valid API key.
6. Keep the unsubscribe link from any received email to stop notifications later.

Using the deployed API:

```bash
curl -X POST https://ga-test-app-82466e574c85.herokuapp.com/api/subscribe \
  -H "X-API-Key: <HEROKU_API_KEY>" \
  -d "email=you@example.com&repo=golang/go"

curl "https://ga-test-app-82466e574c85.herokuapp.com/api/subscriptions?email=you@example.com" \
  -H "X-API-Key: <HEROKU_API_KEY>"
```

Current email delivery status:

- Outbound SMTP is configured through CloudMailin and messages are accepted by CloudMailin.
- Final delivery to recipient inboxes is not working yet because DNS records for the sending domain are not fully configured.
- Until DNS is fixed (SPF/DKIM/return-path as required by CloudMailin/domain provider), email flow is effectively limited to CloudMailin processing.

Useful API calls:

```bash
curl -X POST http://localhost:8080/api/subscribe \
  -H "X-API-Key: dev-api-key-change-in-production" \
  -d "email=you@example.com&repo=golang/go"

curl "http://localhost:8080/api/subscriptions?email=you@example.com" \
  -H "X-API-Key: dev-api-key-change-in-production"

curl "http://localhost:8080/api/confirm/<token>"
curl "http://localhost:8080/api/unsubscribe/<token>"
```

Protected endpoints:

- POST /api/subscribe
- GET /api/subscriptions

Public endpoints:

- GET /api/confirm/{token}
- GET /api/unsubscribe/{token}
- GET /healthz
- GET /metrics

## 6. Environment Configuration

Primary environment template: .env.example

Required variables (enforced in config loader):

- API_KEY
- DATABASE_URL
- SMTP_FROM

SMTP transport configuration:

- Preferred on Heroku with CloudMailin add-on: `CLOUDMAILIN_SMTP_URL` (auto-injected by Heroku).
- Fallback/manual SMTP: `SMTP_HOST` (required when `CLOUDMAILIN_SMTP_URL` is not set), plus optional `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_TLS`.

Important optional variables:

- GITHUB_TOKEN: strongly recommended for higher GitHub API rate limits.
- BASE_URL: used when constructing confirmation/unsubscribe links in emails.
- SCANNER_INTERVAL, SCANNER_WORKERS: controls scanner cadence and concurrency.
- GITHUB_CACHE_TTL: Redis cache TTL for GitHub responses.
- REDIS_URL: preferred on Heroku and other managed Redis services; supports `redis://` and `rediss://`.
- REDIS_TLS_URL: if present, takes precedence over REDIS_URL and is recommended for Heroku Redis TLS.
- REDIS_TLS_SERVER_NAME: optional TLS SNI/hostname override for certificate validation.
- REDIS_TLS_INSECURE_SKIP_VERIFY: set to true only as a temporary workaround when provider CA chains are unavailable in runtime trust store.

Heroku + CloudMailin notes:

- CloudMailin exposes `CLOUDMAILIN_SMTP_URL` in format like `smtp://usr:pswd@host.name.example.net:587?starttls=true`.
- The app parses this URL automatically and uses it for outbound emails.
- `SMTP_FROM` is still required and should be set to your sender address.

Docker compose overrides to use container hostnames:

- DOCKER_DATABASE_URL
- DOCKER_REDIS_ADDR
- DOCKER_SMTP_HOST

## 7. Linting and Code Quality

Linting is configured via `.golangci.yml` using golangci-lint v2 schema.

Enabled linters:

- govet
- ineffassign
- staticcheck

To run the same lint check locally:

```bash
golangci-lint run --timeout=3m
```

Practical quality checks currently available:

```bash
golangci-lint run --timeout=3m
go test ./...
go test -race -count=1 ./...
go vet ./...
```

## 8. Testing and Checkers

Present test coverage includes:

- API handler and contract tests in internal/api
- GitHub client tests in internal/github
- Mailer tests in internal/mailer
- Repository tests in internal/repository
- Scanner tests in internal/scanner
- Service tests in internal/service

Run all tests:

```bash
go test ./...
```

Run with race detector:

```bash
go test -race -count=1 ./...
```

Run coverage:

```bash
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

Contract safety checks:

- TestSwaggerContractStatusMatrix
- TestAuthBoundaries

These are executed in CI via internal/api/contract_test.go.

## 9. CI/CD Pipelines

CI exists via .github/workflows/ci.yml.

Trigger:

- push
- pull_request

Pipeline stages currently implemented:

1. Run lint job with golangci-lint
2. Run contract-focused API tests
3. Run full go test ./...

The CI workflow uses Node 24-compatible GitHub Actions versions and the golangci-lint v2 configuration in `.golangci.yml`.

On CI success, Heroku automatically deploys the latest push to the `main` branch.

## 10. Deployment Process

Current deployment automation:

- **Auto-deploy to Heroku**: The app is connected to GitHub and automatically deploys whenever changes are pushed to the `main` branch.
- Build and test steps run via GitHub Actions CI before any changes are deployed.

Heroku deployment flow:

1. Push changes to `main` branch on GitHub.
2. GitHub Actions runs lint and test suite.
3. On CI success, Heroku automatically detects the push and rebuilds the image.
4. New dyno processes start with updated code and current environment variables.
5. Zero-downtime deployment (old dyno drains connections before shutdown).

Alternatively, manual deployment strategy with existing assets:

1. Build image with Dockerfile.
2. Provide runtime environment variables.
3. Run app container together with Postgres and Redis (or managed equivalents).

Example (single host using compose file as baseline):

```bash
docker compose up -d --build
```

For production hardening, ensure:

- Strong API_KEY.
- Valid SMTP credentials.
- Persistent PostgreSQL storage.
- Restricted network exposure for Redis/PostgreSQL.

## 11. Monitoring and Logging

Monitoring:

- Prometheus metrics exposed on GET /metrics.
- Metrics include subscription lifecycle, scan duration/errors, GitHub request outcomes, email send outcomes, and HTTP request latency.

Logging:

- Application uses standard library logging and prints operational errors/events.
- No structured logging stack or log shipping configuration is defined in this repository.

## 12. Scripts and Automation

Repository automation currently available:

- Dockerfile for container image build.
- docker-compose.yml for local multi-service orchestration.
- GitHub Actions workflow for CI test automation.
- Git pre-push hook at .githooks/pre-push (runs go test ./... and blocks push on failure).