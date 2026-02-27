# URL Shortener

A Redis-backed URL shortener written in Go.

## Features

- Create short URLs with optional custom aliases
- Redirect from short code to original URL
- Track visit count per short URL
- Optional expiration (`expiration_days`)
- Fetch per-URL stats
- Delete short URLs
- Redis health endpoint

## Requirements

- Go 1.25+
- Redis 7+
- Docker (optional, for local Redis via compose and integration tests)

## Quick Start

```bash
make docker-run
make run
```

The server starts on `http://localhost:8080` by default.

## API

### Health

```bash
curl -s http://localhost:8080/health
```

### Create short URL

```bash
curl -s -X POST http://localhost:8080/api/v1/shorten \
  -H 'Content-Type: application/json' \
  -d '{"url":"https://example.com/docs","custom_alias":"docs01","expiration_days":7}'
```

`custom_alias` and `expiration_days` are optional.

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

## Make Targets

- `make all`: build + test
- `make build`: compile binary
- `make run`: run API
- `make test`: run test suite
- `make itest`: run integration tests
- `make docker-run`: start Redis container
- `make docker-down`: stop Redis container
- `make watch`: live reload via `air`
- `make clean`: remove compiled binary

## Notes

- Integration tests automatically skip when Docker is unavailable.
- Environment variables are read from `.env`.
