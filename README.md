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
| `SD_OIDC_ROLES_CLAIM` | `urn:zitadel:iam:org:project:roles` | claim carrying the role names |
| `SD_OIDC_USERNAME_CLAIM` | – | display name only, never used as identity |
| `SD_RBAC_ROLES_ADMINS` | – | **required**, role name(s) mapped to `admin` |
| `SD_RBAC_ROLES_OPERATORS` | – | role name(s) mapped to `operator` |
| `SD_RBAC_ROLES_CREATORS` | – | role name(s) mapped to `creator` |
| `SD_RBAC_ROLES_REPORTERS` | – | role name(s) mapped to `reporter` (machine principals) |

Each `SD_RBAC_ROLES_*` variable accepts a comma-separated list of names. The deprecated
`SD_RBAC_GROUPS_*` variables are still read for one release. See [docs/auth.md](docs/auth.md).

## API

`openapi.yaml` is the authoritative contract; it is baked into the image and served with the API.

| Group | Endpoints |
| --- | --- |
| `/v2/events` | current event API: list with pagination, create, read, patch, patch an update, extract components |
| `/v2/incidents` | deprecated alias of `/v2/events` |
| `/v2/components` | list, create, read |
| `/v2/availability` | monthly availability per component |
| `/rss/` | RSS feed for the frontend |
| `/v1/component_status`, `/v1/incidents` | legacy API kept for the old frontend |

Business rules the specification cannot express — validation, status lifecycles, automatic
transitions — are documented in [docs/events.md](docs/events.md).

## Development

```shell
make test        # unit tests
make test-acc    # integration tests (Docker required)
make lint        # golangci-lint 2.11.4
make migrate-up  # apply the next migration from db/migrations
make migrate-create name=add_xyz
```

Layout: `cmd/` entry point, `internal/api` HTTP layer (middleware, routes, `v1`, `v2`, `rbac`,
`auth`), `internal/db` persistence, `internal/checker` background status transitions,
`internal/event` domain types, `db/migrations` schema, `tests/` integration suite,
`openapi.yaml` contract.

## Documentation

| Document | Contents |
| --- | --- |
| [docs/events.md](docs/events.md) | event types, creation rules, status lifecycles, availability, RSS, legacy API |
| [docs/auth.md](docs/auth.md) | OIDC authentication, RBAC roles, permissions, field visibility |
| [docs/testing.md](docs/testing.md) | unit and integration suites, how to run them |
| [docs/diagrams/](docs/diagrams) | decision graphs for the v1 flow and for system incident creation |
| [openapi.yaml](openapi.yaml) | HTTP contract and schemas |
