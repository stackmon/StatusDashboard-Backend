# Status Dashboard Backend

REST API behind the OTC Status Dashboard. It stores components and the events that affect them —
incidents, maintenance windows and informational messages — and exposes them to the frontend, to
machine reporters and as an RSS feed.

Go 1.26 · Gin · GORM/PostgreSQL · OIDC (Zitadel), shipped as a small Alpine image (`Dockerfile`).

## Quick start

```shell
cp .env.example .env         # fill in the Zitadel issuer, client id and role names
docker compose up -d database
make migrate-up
go run cmd/main.go           # or: make build && ./app
```

The API then listens on `SD_PORT` (8000 by default). Swagger UI is at `/swagger/index.html` and the
raw specification at `/openapi.json`.

## Configuration

Read from the environment, with a `.env` file as fallback (`internal/conf`). Everything is prefixed
with `SD_`. The application refuses to start when `SD_PORT`, the OIDC pair or `SD_RBAC_ROLES_ADMINS`
is missing or invalid.

| Variable | Default | Notes |
| --- | --- | --- |
| `SD_DB` | – | PostgreSQL DSN, e.g. `postgres://pg:pass@localhost:5432/status_dashboard?sslmode=disable` |
| `SD_CACHE` | – | cache backend, `internal` for the in-process implementation |
| `SD_LOG_LEVEL` | `devel` | `devel` or a zap level such as `info`, `warn`, `error` |
| `SD_PORT` | `8000` | must be between 1024 and 50000 |
| `SD_OPENAPI_SPEC_PATH` | `openapi.yaml` | path of the spec served at `/openapi.json` |
| `SD_OIDC_ISSUER` | – | **required**, must match the discovered issuer exactly |
| `SD_OIDC_CLIENT_ID` | – | **required**, the audience accepted in `aud` |
| `SD_OIDC_USERNAME_CLAIM` | – | display name only, never used as identity |
| `SD_RBAC_ROLES_ADMINS` | – | **required**, role name(s) mapped to `admin` |
| `SD_RBAC_ROLES_OPERATORS` | – | role name(s) mapped to `operator` |
| `SD_RBAC_ROLES_CREATORS` | – | role name(s) mapped to `creator` |
| `SD_RBAC_ROLES_REPORTERS` | – | role name(s) mapped to `reporter` (machine principals) |
| `SD_STATIC_ORIGINS` | – | OBS website endpoints serving the static site, primary first, host only |
| `SD_STATIC_CACHE_TTL` | `5m` | lifetime of a cached response that carries no `Cache-Control` |
| `SD_STATIC_CACHE_MAX_BYTES` | `67108864` | byte budget of the whole response cache |

Each `SD_RBAC_ROLES_*` variable accepts a comma-separated list of names. See [docs/auth.md](docs/auth.md).

## API

`openapi.yaml` is the authoritative contract; it is baked into the image and served with the API.

| Group | Endpoints |
| --- | --- |
| `/v2/events` | current event API: list with pagination, create, read, patch, patch an update, extract components |
| `/v2/incidents` | deprecated alias of `/v2/events` |
| `/v2/components` | list, create, read |
| `/v2/availability` | monthly availability per component |
| `/rss/` | RSS feed for the frontend |

Business rules the specification cannot express — validation, status lifecycles, automatic
transitions — are documented in [docs/events.md](docs/events.md).

## Static site

With `SD_STATIC_ORIGINS` set, every path the API does not own is proxied to the OBS website
endpoints over HTTPS, with the origin hostname in `Host` because OBS selects the bucket from that
header and ignores SNI. Origins are tried in order: a connect failure, a timeout or a `502`/`504`
falls through to the next one. `GET` and `HEAD` are proxied, any other method keeps the API's 404,
and an empty origin list leaves the router exactly as it was without this feature.

Responses are cached in memory per method, host, path and query:

- `GET` only — never `HEAD`, never an API route.
- TTL from `max-age`/`s-maxage`, `SD_STATIC_CACHE_TTL` when the origin sends neither; responses
  marked `no-store` or `no-cache` and responses that are not `200` are not stored.
- A single object is buffered up to 8 MiB, the whole cache is bounded by `SD_STATIC_CACHE_MAX_BYTES`
  and evicted least-recently-used first.
- Concurrent requests for the same missing entry share one origin fetch, and an expired entry is
  served when every origin is unreachable.

`X-Cache` reports the outcome: `HIT`, `MISS`, `STALE` or `BYPASS`.

## Development

```shell
make test        # unit tests
make test-acc    # integration tests (Docker required)
make lint        # golangci-lint 2.11.4
make migrate-up  # apply the next migration from db/migrations
make migrate-create name=add_xyz
```

Layout: `cmd/` entry point, `internal/api` HTTP layer (middleware, routes, `v2`, `rbac`,
`auth`), `internal/static` OBS proxy and response cache, `internal/db` persistence,
`internal/checker` background status transitions, `internal/event` domain types,
`db/migrations` schema, `tests/` integration suite, `openapi.yaml` contract.

## Documentation

| Document | Contents |
| --- | --- |
| [docs/events.md](docs/events.md) | event types, creation rules, status lifecycles, availability, RSS |
| [docs/auth.md](docs/auth.md) | OIDC authentication, RBAC roles, permissions, field visibility |
| [docs/testing.md](docs/testing.md) | unit and integration suites, how to run them |
| [docs/diagrams/](docs/diagrams) | decision graph for system incident creation |
| [openapi.yaml](openapi.yaml) | HTTP contract and schemas |
