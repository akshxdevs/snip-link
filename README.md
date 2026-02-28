# url-shortner
Redis-backed URL shortener in Go with custom aliases, visit tracking, optional TTL, and a Next.js frontend.

[![Build](https://img.shields.io/badge/build-go%20test%20.%2F...-brightgreen)](https://github.com/akshxdevs/url-shortner)
[![Go Version](https://img.shields.io/badge/go-1.25.7-00ADD8?logo=go)](https://go.dev/)
[![Go Report Card](https://goreportcard.com/badge/github.com/akshxdevs/url-shortner)](https://goreportcard.com/report/github.com/akshxdevs/url-shortner)
[![License](https://img.shields.io/badge/license-unlicensed-lightgrey)](https://github.com/akshxdevs/url-shortner)

## Overview
`url-shortner` provides:
- short URL creation with auto-generated or custom alias codes
- redirect from short code to original URL
- per-URL visit tracking incremented on every redirect
- optional URL expiry via `expiration_days`
- stats endpoint returning code, long URL, visits, and expiry
- full delete of any short URL
- deep Redis health reporting with connection pool diagnostics

## Key Features
- Cryptographically random 7-character short codes via `crypto/rand` with up to 10 collision-retry attempts.
- Custom alias validation (`^[a-zA-Z0-9_-]{4,32}$`) with atomic conflict detection using `HSetNX`.
- Redis hash data model per URL: stores `url`, `created_at`, and `visits` as a single key.
- Optional TTL set via Redis `EXPIRE`; `ExpiresAt` derived dynamically from key TTL on stats reads.
- CORS middleware allowing `GET`, `POST`, `DELETE`, `OPTIONS` for frontend integration.
- Graceful shutdown with 5-second drain timeout on `SIGINT`/`SIGTERM`.
- Integration tests using `testcontainers-go` — auto-skipped when Docker is unavailable.

## Tech Stack
- Go `1.25.7`
- `net/http` + `http.ServeMux`
- Redis `7+` (`redis/go-redis/v9`)
- NanoID (`matoous/go-nanoid/v2`)
- Testcontainers (`testcontainers-go`)
- Next.js `16` + React `19` + Tailwind CSS `v4` + TypeScript (frontend)

## Installation
### Prerequisites
- Go `1.25+`
- Redis `7+`
- Docker (optional, for local Redis via compose and integration tests)
- Bun (optional, for running the frontend)

### Run locally
```bash
git clone git@github.com:akshxdevs/url-shortner.git
cd url-shortner
go mod tidy
make docker-run
make run
```

### Build and test
```bash
make build
make test
make itest
```

## Configuration
Environment variables (autoloaded from `.env`):

```env
PORT=8080
BLUEPRINT_DB_ADDRESS=localhost
BLUEPRINT_DB_PORT=6379
BLUEPRINT_DB_PASSWORD=
BLUEPRINT_DB_DATABASE=0
```

Notes:
- `PORT` defaults to `8080` if unset.
- `BLUEPRINT_DB_DATABASE` must be a valid integer (Redis DB index); the server will fatal on startup if it is not.
- `BLUEPRINT_DB_PASSWORD` can be left empty for local Redis with no auth.

## API Endpoints
- `GET /` — service info with available routes
- `GET /health` — deep Redis health and connection pool stats
- `POST /api/v1/shorten` — create a short URL
- `GET /{code}` — redirect to the original URL (increments visit count)
- `GET /api/v1/urls/{code}` — fetch stats for a short URL
- `DELETE /api/v1/urls/{code}` — permanently delete a short URL

## Usage Examples
### Create short URL (auto code)
```bash
curl -s -X POST http://localhost:8080/api/v1/shorten \
  -H "Content-Type: application/json" \
  -d '{"url":"https://example.com/very/long/path"}'
```

### Create short URL (custom alias + expiry)
```bash
curl -s -X POST http://localhost:8080/api/v1/shorten \
  -H "Content-Type: application/json" \
  -d '{"url":"https://example.com/docs","custom_alias":"docs01","expiration_days":7}'
```

### Redirect
```bash
curl -i http://localhost:8080/docs01
```

### Get URL stats
```bash
curl -s http://localhost:8080/api/v1/urls/docs01
```

### Delete short URL
```bash
curl -i -X DELETE http://localhost:8080/api/v1/urls/docs01
```

## Core Functions (Server Layer)
`internal/server/routes.go`
- Route registration via `http.ServeMux` with method-prefixed patterns.
- `createShortURLHandler` — validates URL, resolves short code (custom or generated), sets TTL, stores in Redis.
- `redirectHandler` — fetches long URL, increments visits, issues `302` redirect.
- `urlStatsHandler` — returns full `URLStats` JSON including dynamic expiry.
- `deleteURLHandler` — hard deletes the Redis key, returns `204`.
- `resolveShortCode` — alias validation + existence check, or 10-attempt random generation loop.
- `validateTargetURL` — enforces `http`/`https` scheme and non-empty host.
- `corsMiddleware` — injects CORS headers and handles `OPTIONS` preflight.

`internal/server/server.go`
- `NewServer` wires port, Redis service, and route handler into `http.Server` with configured timeouts.

## Database Service Contract
`internal/redis.Service` covers:
- `CreateShortURL` — atomic `HSetNX` creation with metadata and optional TTL.
- `GetLongURL` — single-field `HGET` for redirect hot path.
- `IncrementVisits` — existence-guarded `HINCRBY`.
- `GetStats` — `HGETALL` + `TTL` assembled into `URLStats`.
- `DeleteShortURL` — `DEL` with not-found detection.
- `ShortCodeExists` — `EXISTS` check used by code generation and alias validation.
- `Health` — deep Redis diagnostics with pool stats and threshold warnings.

Used as the handler dependency (`Server.db`) to keep route tests DB-agnostic via interface mocking.

## Health Payload Fields
`GET /health` returns Redis server info and connection pool stats:
- `redis_status`, `redis_message`, `redis_ping_response`
- `redis_version`, `redis_mode`, `redis_uptime_in_seconds`
- `redis_used_memory`, `redis_used_memory_peak`, `redis_max_memory`
- `redis_connected_clients`, `redis_active_connections`
- `redis_hits_connections`, `redis_misses_connections`, `redis_timeouts_connections`
- `redis_total_connections`, `redis_idle_connections`, `redis_stale_connections`
- `redis_pool_size_percentage`

Warning messages are set when: clients exceed 80% of pool size, stale connections exceed 500, memory usage is ≥ 90% of max, uptime is under 1 hour, or pool utilization exceeds 90%.

## Project Structure
```text
.
├── app/                        Next.js frontend
│   ├── app/
│   │   ├── globals.css
│   │   ├── layout.tsx
│   │   └── page.tsx
│   ├── public/
│   ├── package.json
│   └── tsconfig.json
├── cmd/
│   └── api/main.go
├── internal/
│   ├── redis/
│   │   ├── redis.go
│   │   └── redis_test.go
│   └── server/
│       ├── routes.go
│       ├── routes_test.go
│       └── server.go
├── docker-compose.yml
├── Makefile
└── README.md
```

## Development Commands
```bash
make run          # run API server
make watch        # live reload via air
make test         # unit tests
make itest        # integration tests (requires Docker)
make build        # compile binary → ./main
make clean        # remove compiled binary
make docker-run   # start Redis container
make docker-down  # stop Redis container
```

## Frontend (app/)
```bash
cd app
bun install
bun dev     # starts Next.js dev server
```

## Current Limitations
- No authentication or rate limiting on the shorten endpoint.
- No persistence beyond Redis — data is lost if Redis is flushed without a snapshot.
- Single Redis instance only; no cluster or sentinel support.
- Short code generation uses `crypto/rand` directly; NanoID dependency pulled in but not yet wired as primary generator.
- No OpenAPI spec yet.

## License
Licensed under the [MIT License](LICENSE).
