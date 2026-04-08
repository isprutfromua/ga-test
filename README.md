# GitHub Release Notifier

Service that tracks GitHub repositories and emails subscribers when a new stable release appears.

## Overview

Main flow:

1. User subscribes with email + owner/repo.
2. Service validates repository and stores unconfirmed subscription.
3. User confirms via tokenized link.
4. Background scanner checks releases and sends notifications for new stable tags.

Tech stack:

- Go 1.23
- chi v5 (HTTP routing/middleware)
- PostgreSQL + pgx stdlib (persistence)
- Redis (GitHub response cache)
- SMTP / CloudMailin (email transport)
- Prometheus client (metrics)
- Docker + docker compose (local/dev)
- GitHub Actions CI + Heroku auto-deploy

## Installation and Setup

Prerequisites:

- Docker
- Docker Compose plugin

Run locally:

```bash
cp .env.example .env
docker compose up --build
```

Required environment variables:

- API_KEY
- DATABASE_URL
- SMTP_FROM

Local endpoints:

- App: http://localhost:8080
- Health: http://localhost:8080/healthz
- Metrics: http://localhost:8080/metrics
- Mailpit: http://localhost:8025

## API Endpoints

Protected (require `X-API-Key`):

- `POST /api/subscribe`
- `GET /api/subscriptions?email=...`

Public:

- `GET /api/confirm/{token}`
- `GET /api/unsubscribe/{token}`
- `GET /healthz`
- `GET /metrics`

Example local calls:

```bash
curl -X POST http://localhost:8080/api/subscribe \
  -H "X-API-Key: dev-api-key-change-in-production" \
  -d "email=you@example.com&repo=golang/go"

curl "http://localhost:8080/api/subscriptions?email=you@example.com" \
  -H "X-API-Key: dev-api-key-change-in-production"

curl "http://localhost:8080/api/confirm/<token>"
curl "http://localhost:8080/api/unsubscribe/<token>"
```

## How to Test Local

1. Start stack with docker compose.
2. Open app at http://localhost:8080 and set valid API key (env variable).
3. Subscribe with email + repo.
4. Open Mailpit at http://localhost:8025 and click confirmation link.
5. Verify subscription list using UI lookup or `GET /api/subscriptions`.

Code quality checks:

```bash
golangci-lint run --timeout=3m
go test ./...
go test -race -count=1 ./...
```

Focused contract checks:

```bash
go test ./internal/api -run 'TestSwaggerContractStatusMatrix|TestAuthBoundaries'
```

## How to Test in Prod

App URLs:

- App: https://ga-test-app-82466e574c85.herokuapp.com/
- Metrics: https://ga-test-app-82466e574c85.herokuapp.com/metrics
- Swagger view: https://editor.swagger.io/?url=https://raw.githubusercontent.com/isprutfromua/ga-test/main/swagger.yaml

Manual prod smoke test:

1. Open app and set valid API key (env variable).
2. Submit subscription.
3. Confirm via email link.
4. Check subscription lookup.
5. Optionally verify metrics endpoint is reachable.

Prod API check:

```bash
curl -X POST https://ga-test-app-82466e574c85.herokuapp.com/api/subscribe \
  -H "X-API-Key: <API_KEY>" \
  -d "email=you@example.com&repo=golang/go"

curl "https://ga-test-app-82466e574c85.herokuapp.com/api/subscriptions?email=you@example.com" \
  -H "X-API-Key: <API_KEY>"
```

Note: SMTP messages are accepted by CloudMailin, but inbox delivery is currently limited until sender-domain DNS (SPF/DKIM/return-path) is fully configured.

## High Level Structure

- `cmd/server`: bootstrap + graceful shutdown.
  Keep runtime wiring in one entrypoint for predictable startup and deploy behavior.
- `internal/api`: HTTP handlers, middleware, router.
  Isolate transport concerns from business logic.
- `internal/service`: subscription business rules, tokens, async work.
  Central place for use cases and orchestration.
- `internal/repository`: PostgreSQL data access.
  Keep SQL/storage concerns separate and testable.
- `internal/github`: GitHub client + repo/release checks (with Redis cache support).
  Hide third-party API details behind internal abstraction.
- `internal/scanner`: periodic release polling with worker pool.
  Bounded concurrency to reduce risk of API spikes.
- `internal/mailer`: email sending implementation.
  Isolate provider/config specifics from core logic.
- `internal/config`, `internal/db`, `internal/metrics`: shared infrastructure modules.
  Explicit infrastructure boundaries improve maintainability.
- `static`: browser UI pages served by same process.
  Single binary deploy (API + UI) keeps operations simple.

## Deployments / Monitoring / Automation

Deployments:

- GitHub Actions runs lint + tests on push/PR.
- Heroku auto-deploys updates from `main` after CI passes.

Monitoring:

- Prometheus metrics at `/metrics` (HTTP latency, scan results, GitHub calls, email outcomes, subscription lifecycle).
- Health endpoint at `/healthz`.

Automation:

- `.github/workflows/ci.yml` for CI.
- `.githooks/pre-push` runs `go test ./...`.
- Dockerfile + `docker-compose.yml` provide reproducible local/prod-like setup.