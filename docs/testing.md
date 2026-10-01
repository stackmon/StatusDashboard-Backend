# Testing

There are two suites: fast unit tests under `internal/` and an integration suite under `tests/` that
runs the real HTTP stack against a throwaway PostgreSQL container.

```shell
make test       # go test ./internal/... -count 1
make test-acc   # go test ./tests/... -count 1   (needs Docker)
make lint       # golangci-lint 2.11.4, the version checked by CI
```

CI (`.github/workflows/ci.yaml`) runs the linter and `make test`; the integration suite is meant to be
run locally or in a Docker-capable environment.

## Unit tests

| Package | Files | Covers |
| --- | ---: | --- |
| `internal/api` | 2 | middleware chain, embedded Swagger UI |
| `internal/api/auth` | 1 | OIDC token validation |
| `internal/api/rbac` | 1 | role resolution and configuration |
| `internal/api/v2` | 6 | auth, RBAC, validation, visibility and helpers |
| `internal/checker` | 1 | maintenance status calculation (info transitions are untested) |
| `internal/conf` | 1 | configuration parsing, defaults and validation |

## Integration tests

`tests/main_test.go` starts a `postgres:15-alpine` container with testcontainers-go, applies
`db/migrations`, seeds `tests/testdata/dump_test.sql` (six components — CCE, ECS and DCS in EU-DE and
EU-NL — plus one resolved incident) and mounts the production route table, so every test exercises
authentication, RBAC and the handlers together. `tests/idp_test.go` runs a local OIDC provider that
signs RS256 tokens, which lets the suite mint admin, operator, creator and reporter tokens without an
external Zitadel.

| File | Focus |
| --- | --- |
| `main_test.go` | container bootstrap, route initialisation, DB helpers |
| `rbac_helpers_test.go` | pre-built tokens, request helpers, event factories |
| `idp_test.go` | local identity provider |
| `rbac_permissions_test.go` | per-role maintenance transition matrix |
| `rbac_creation_test.go` | creation rules and initial statuses |
| `rbac_workflow_test.go` | creator → review → completion workflows |
| `rbac_version_test.go` | optimistic locking and version conflicts |
| `rbac_visibility_test.go` | hidden events and hidden fields |
| `rbac_token_test.go` | token validation failures |
| `rbac_admin_only_test.go` | endpoints restricted to admins |
| `rbac_reporter_test.go` | reporter scope restrictions |
| `rbac_extract_test.go` | component extraction |
| `v2_test.go` | `/v2/incidents` alias, components, patch, extract, availability |
| `v2_events_test.go` | `/v2/events` |
| `v2_system_incident_test.go` | system incident creation and component movement |

Both suites use `-count 1`, so caching never hides a flake. Expected behaviour is documented in
[auth.md](auth.md) and [events.md](events.md).
