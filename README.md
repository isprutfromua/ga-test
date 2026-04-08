# GitHub Release Notifier

A production-grade Go monolith that sends email notifications when new GitHub repository releases are published. Users subscribe via a clean web UI or API, confirm via email, and receive release alerts automatically.

---

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                      HTTP Server (chi)                       │
│                                                              │
│  POST /api/subscribe      GET /api/confirm/{token}           │
│  GET  /api/unsubscribe/{token}  GET /api/subscriptions       │
│  GET  /metrics            GET /healthz                       │
│  GET  /  (static HTML — htmx + Tailwind)                    │
├──────────────┬──────────────────────────────────────────────┤
│   API Layer  │              Service Layer                    │
│  (handlers,  │  (subscribe, confirm, unsubscribe, lookup)   │
│  middleware) │                                              │
├──────────────┴──────────────────────────────────────────────┤
│            Repository Layer (Postgres)                       │
├────────────────┬────────────────────────────────────────────┤
│  GitHub Client │  Redis Cache (10-min TTL)                  │
│  (rate-limit   │  Mailer (SMTP / Mailpit in dev)            │
│   handling)    │                                            │
├────────────────┴────────────────────────────────────────────┤
│         Background Scanner (bounded worker pool)             │
│         runs every SCANNER_INTERVAL, SCANNER_WORKERS goroutines │
└─────────────────────────────────────────────────────────────┘
```

### Component responsibilities

| Component | Package | Responsibility |
|---|---|---|
| **Config** | `internal/config` | Reads all settings from environment variables; fails fast on missing required values. |
| **Database** | `internal/db` | Opens a connection pool with retry; runs `golang-migrate` migrations on startup. |
| **Repository** | `internal/repository` | Postgres data-access layer behind a Go interface for testability. |
| **Cache** | `internal/cache` | Redis wrapper (Get/Set/Delete). Used by GitHub client to cache API responses. |
| **GitHub Client** | `internal/github` | Calls GitHub REST API. Handles 429/403 rate-limit responses. Results cached in Redis for 10 min. |
| **Mailer** | `internal/mailer` | Sends HTML confirmation and release-notification emails via SMTP. |
| **Metrics** | `internal/metrics` | Declares all Prometheus counters, histograms, and gauges. |
| **Service** | `internal/service` | Business logic: token generation, validation, email dispatch, error mapping. |
| **Scanner** | `internal/scanner` | Background loop. Fetches confirmed subscriptions, checks latest GitHub release, notifies if new, updates `last_seen_tag`. Uses bounded goroutine pool. |
| **API** | `internal/api` | Chi router, middleware (API key auth, Prometheus, request logger), HTTP handlers. |

---

## Key Design Decisions

### Token security
`crypto/rand` generates 32-byte (64 hex char) confirm and unsubscribe tokens. They are stored in Postgres with `UNIQUE` constraints. Every DB lookup is preceded by a fast format check (`isValidToken`) to prevent timing attacks on malformed input.

### GitHub rate limiting
- Without a token: **60 requests/hour**
- With `GITHUB_TOKEN`: **5,000 requests/hour**
- All GitHub API responses (repo existence + latest release) are **Redis-cached for 10 minutes**, so repeated subscriptions to the same repo cost zero extra API calls within the cache window.
- The scanner's bounded worker pool (`SCANNER_WORKERS`, default 5) prevents burst-exhaustion during large scan cycles.
- Rate-limited responses are skipped and retried at the next scan interval; the error is recorded in the `notifier_github_rate_limit_hits_total` Prometheus counter.

### Scanner — bounded worker pool
Instead of spawning one goroutine per subscription (unbounded goroutine explosion), a fixed-size pool reads from a buffered job channel:

```
subscriptions → [job channel] → [worker 1]
                              → [worker 2]   → GitHub API → DB update → email
                              → [worker N]
```

Draft and pre-release tags are filtered out — only stable releases trigger notifications.

### Idiomatic error propagation
Sentinel errors (`ErrInvalidRepo`, `ErrRepoNotFound`, `ErrAlreadyExists`, `ErrRateLimited`, `ErrTokenNotFound`) are defined at the domain boundary and flow upward through service → handler. Each handler maps them to the exact HTTP status codes specified in `swagger.yaml`.

### Graceful shutdown
1. SIGINT/SIGTERM received
2. Scanner context cancelled → current scan cycle finishes cleanly
3. HTTP server given 30s to drain in-flight requests
4. Process exits

### Observability
| Metric | Type | Description |
|---|---|---|
| `notifier_subscriptions_created_total` | Counter | Subscriptions created |
| `notifier_confirmations_total` | Counter | Subscriptions confirmed |
| `notifier_unsubscribes_total` | Counter | Unsubscriptions |
| `notifier_active_subscriptions` | Gauge | Currently active subscriptions |
| `notifier_scan_duration_seconds` | Histogram | Full scan cycle duration |
| `notifier_scan_errors_total` | Counter | Scan errors |
| `notifier_emails_sent_total` | Counter | Emails sent successfully |
| `notifier_email_errors_total` | Counter | Email failures |
| `notifier_github_requests_total` | Counter (by status) | GitHub API call outcomes |
| `notifier_github_rate_limit_hits_total` | Counter | Rate-limit responses |
| `notifier_http_request_duration_seconds` | Histogram (method, route, status) | HTTP request latency |

All metrics are served at `GET /metrics` (no API key required — standard for Prometheus scraping).

---

## Running locally

### Prerequisites
- Docker and Docker Compose

### Start the stack

```bash
cp .env.example .env
# Edit .env — set GITHUB_TOKEN for higher rate limits

docker compose up --build
```

| Service | URL |
|---|---|
| **App** | http://localhost:8080 |
| **Mailpit UI** (inspect emails) | http://localhost:8025 |
| **Prometheus metrics** | http://localhost:8080/metrics |
| **Health check** | http://localhost:8080/healthz |

### API usage

All `/api/*` endpoints require the `X-API-Key` header:

```bash
# Subscribe
curl -X POST http://localhost:8080/api/subscribe \
  -H "X-API-Key: dev-api-key-change-in-production" \
  -d "email=you@example.com&repo=golang/go"

# List subscriptions
curl "http://localhost:8080/api/subscriptions?email=you@example.com" \
  -H "X-API-Key: dev-api-key-change-in-production"

# Confirm (token from email)
curl "http://localhost:8080/api/confirm/{token}" \
  -H "X-API-Key: dev-api-key-change-in-production"

# Unsubscribe
curl "http://localhost:8080/api/unsubscribe/{token}" \
  -H "X-API-Key: dev-api-key-change-in-production"
```

---

## Running tests

```bash
# Unit tests (no external services required)
go test ./internal/service/... ./internal/github/... ./internal/scanner/... -v

# All tests with race detector
go test -race -count=1 ./...

# With coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

---

## Deploying to DigitalOcean

### Required GitHub Actions secrets

| Secret | Description |
|---|---|
| `DIGITALOCEAN_ACCESS_TOKEN` | DigitalOcean API token |
| `DO_REGISTRY_NAME` | Container registry name (e.g. `my-registry`) |
| `DO_DROPLET_IP` | Droplet public IP |
| `DO_SSH_USER` | SSH user (e.g. `root`) |
| `DO_SSH_KEY` | Private SSH key |

### Droplet setup

```bash
# On the Droplet
apt update && apt install -y docker.io docker-compose-plugin
mkdir -p /opt/github-release-notifier
cd /opt/github-release-notifier

# Copy docker-compose.yml and .env to the droplet
# Set production values in .env, especially:
#   API_KEY, GITHUB_TOKEN, SMTP_*, BASE_URL, DATABASE_URL

docker compose up -d
```

Every push to `main` triggers the CI pipeline:
1. **Lint** via golangci-lint
2. **Test** with race detector (Postgres + Redis service containers)
3. **Build** Docker image
4. **Push** to DigitalOcean Container Registry
5. **Deploy** to the Droplet via SSH

---

## Environment variables reference

See [`.env.example`](.env.example) for the full list with descriptions.

Required: `API_KEY`, `DATABASE_URL`, `SMTP_HOST`, `SMTP_FROM`, `BASE_URL`

Strongly recommended: `GITHUB_TOKEN` (raises GitHub rate limit from 60/hr to 5,000/hr)

---

## Project structure

```
.
├── cmd/server/main.go                  # Entry point
├── internal/
│   ├── api/
│   │   ├── handler.go                  # HTTP handlers
│   │   ├── handler_test.go
│   │   ├── middleware.go               # Auth, metrics, logging
│   │   └── router.go                   # Chi router wiring
│   ├── cache/redis.go                  # Redis cache abstraction
│   ├── config/config.go                # Environment config
│   ├── db/
│   │   ├── db.go                       # Connection + migration runner
│   │   └── migrations/
│   │       └── 000001_initial.up.sql
│   ├── github/
│   │   ├── client.go                   # GitHub API client
│   │   └── client_test.go
│   ├── mailer/mailer.go                # SMTP email sender
│   ├── metrics/metrics.go              # Prometheus instruments
│   ├── models/models.go                # Domain types
│   ├── repository/subscription.go      # Postgres data-access
│   ├── scanner/
│   │   ├── scanner.go                  # Background release scanner
│   │   └── scanner_test.go
│   └── service/
│       ├── subscription.go             # Business logic
│       └── subscription_test.go
├── static/index.html                   # htmx + Tailwind UI
├── Dockerfile                          # Multi-stage, distroless runtime
├── docker-compose.yml                  # Full local stack
├── .github/workflows/ci.yml            # Lint → Test → Build → Deploy
├── .golangci.yml                       # Linter configuration
├── .env.example                        # Config reference
└── README.md
```
