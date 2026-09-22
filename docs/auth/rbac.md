# Role-Based Access Control (RBAC)

## Overview

The Status Dashboard implements RBAC for maintenance event management. Roles are resolved from the
role names carried by the JWT roles claim of the access token and mapped to application permissions.

## Roles

Four application roles are supported, with highest privilege taking precedence when a user has multiple roles.
Role names in this document refer to abstract application roles. Each role is mapped from a role name
of the identity provider project, configured via the corresponding environment variable
(e.g. `SD_RBAC_ROLES_ADMINS` → `admin` role).

| Role | Priority | Description |
|------|----------|-------------|
| `admin` | Highest | Full access to all operations; will gain additional system-level privileges in future releases |
| `operator` | Medium | Full CRUD access to all maintenance events (event admin) |
| `creator` | Low | Create and manage own maintenance events |
| `reporter` | Lowest | Machine principal: may only create system incidents, read-only everywhere else |

## Configuration

RBAC is always active — there is no disable toggle. `SD_RBAC_ROLES_ADMINS` is mandatory;
`SD_RBAC_ROLES_OPERATORS`, `SD_RBAC_ROLES_CREATORS` and `SD_RBAC_ROLES_REPORTERS` are optional (when
omitted, no user can match the corresponding role).

Each variable accepts either a single role name or a **comma-separated list** of role names.
All listed role names are mapped to the same role, matched case-sensitively.

| Environment Variable | Required | Description |
|---------------------|----------|-------------|
| `SD_RBAC_ROLES_ADMINS` | **Yes** | Role name(s) that map to the `admin` role |
| `SD_RBAC_ROLES_OPERATORS` | No | Role name(s) that map to the `operator` role |
| `SD_RBAC_ROLES_CREATORS` | No | Role name(s) that map to the `creator` role |
| `SD_RBAC_ROLES_REPORTERS` | No | Role name(s) that map to the `reporter` role |

The pre-Zitadel `SD_RBAC_GROUPS_*` variables are still read for one release; when both the old and
the new name are set, the new one wins and a deprecation warning is logged.

**Example** — mapping multiple Zitadel project roles to the `admin` role:
```
SD_RBAC_ROLES_ADMINS=sd_admins,status-dashboard
```
A token whose roles claim contains either `sd_admins` or `status-dashboard` is granted the
`admin` role.

## Permissions by Role

### `admin`

- Unrestricted access to all maintenance operations
- Bypass all status-based restrictions
- Can transition events to any status
- Events created with status `planned` (bypass review)

### `operator`

- Full CRUD access to all maintenance events (event admin)
- Create events with status `planned` (bypass review workflow)
- View all maintenance events regardless of status
- PATCH events from any status to any valid maintenance status
- Cancel events from any status

### `creator`

- Create maintenance events (status: `pending_review`)
- Modify **own** events only when status is `pending_review`
- Cancel **own** events only when status is `pending_review`
- Cannot modify events after approval (`reviewed`, `planned`, etc.)

### `reporter`

Machine principals (monitoring, automation) that report incidents on behalf of the system:

- Create incidents with `"system": true` via `POST /v2/events` only
- Cannot create maintenance events, information events or human-authored incidents
- Cannot PATCH, extract or cancel anything — every other write route returns `403 Forbidden`
- Read-only on all GET routes, with the public view: `creator`, `contact_email` and `version` are
  hidden, and `pending_review` / `reviewed` events are not listed

A reporter role name may be combined with other roles in the token; the highest privilege then wins,
so a principal holding both a creator and a reporter role behaves as a creator.

## Maintenance Status Workflow

```
Creator creates event
        │
        ▼
  ┌─────────────┐
  │pending_review│ ◄── creator can modify/cancel (own events only)
  └──────┬──────┘     operator/admin can approve, modify, or cancel
         │ operator/admin approves (or any PATCH by operator/admin)
         ▼
  ┌─────────────┐
  │  reviewed   │
  └──────┬──────┘
         │ Checker auto-transitions (no validation)
         ▼
  ┌─────────────┐
  │   planned   │ ◄── operator/admin bypass to here on creation
  └──────┬──────┘
         │ Checker (StartDate reached)
         ▼
  ┌─────────────┐
  │ in_progress │
  └──────┬──────┘
         │ Checker (EndDate reached)
         ▼
  ┌─────────────┐
  │  completed  │  (terminal)
  └─────────────┘

cancelled  ◄── admin: from any status
           ◄── operator: from any status
           ◄── creator: from pending_review (own event) only
```

> **Retroactive maintenance**: events may be created with dates in the past. The checker will
> automatically transition them to `completed` and backfill intermediate status history with
> correct timestamps. See [permissions.md](permissions.md) for the full workflow.

For the complete status transition matrix and per-role rules, see [permissions.md](permissions.md).

## Field Visibility

Some fields are only visible to authenticated users:

| Field | Visibility |
|-------|------------|
| `creator` | Authenticated only |
| `contact_email` | Authenticated only |
| `version` | Authenticated only (maintenance events only) |

Events with status `pending_review` or `reviewed` are hidden from unauthenticated users.

Machine principals holding only the `reporter` role are treated as unauthenticated for reads: they
see the same fields and the same set of events as an anonymous caller.

## Error Responses

| HTTP Code | Condition |
|-----------|-----------|
| `401 Unauthorized` | Missing or invalid JWT token |
| `403 Forbidden` | Insufficient role permissions |
| `403 Forbidden` | Attempting to modify event you don't own (`creator` role) |
| `403 Forbidden` | `reporter` role outside `POST /v2/events` with a system incident |
| `409 Conflict` | Status transition not allowed for current role/state |
| `409 Conflict` | Version mismatch (concurrent modification) |
| `409 Conflict` | Event no longer in expected status |

## JWT Token Structure

Zitadel access token claims used by the API:

```json
{
  "sub": "289257162845356033",
  "aud": ["390700708019568682", "289257162845356225"],
  "urn:zitadel:iam:org:project:roles": {
    "sd_creators": {
      "390700708019568682": "eco-preprod.tsi-dev.otc-service.com"
    }
  }
}
```

- `sub` → stored as the event `creator` (identity is the Zitadel user id, not an email address)
- the claim named by `SD_OIDC_ROLES_CLAIM` (default `urn:zitadel:iam:org:project:roles`) → its role
  names are matched against the configured `SD_RBAC_ROLES_*` variables to resolve the application
  role. Only names the resource server knows are considered, so unrelated custom role keys in the
  same map are ignored. For example, with `SD_RBAC_ROLES_ADMINS=sd_admins,status-dashboard`, a token
  holding either role key is granted the `admin` role.

## Authentication Providers

| Provider | JWT Algorithm | Key Source | Use Case |
|----------|-------------|------------|----------|
| **OIDC (Zitadel)** | RS256 | Issuer discovery + JWKS | Production SSO |
| **Local (HMAC)** | HS256 / HS384 / HS512 | `SD_SECRET_KEY` env var | Dev, tests, service-to-service (transitional) |

The verifier dispatches on the token `alg` header: RS256 tokens are verified against the configured
issuer (signature, `iss`, `aud`, `exp`), HMAC tokens against `SD_SECRET_KEY`. At least one provider
must be configured; `conf.Validate()` fails otherwise. See
[authentication.md](authentication.md) for the full pipeline.

### Security Hardening

- **Minimum secret key length**: `SD_SECRET_KEY` must be ≥ 32 characters (HMAC-SHA256 requirement).
- **No bypasses**: `SD_AUTHENTICATION_DISABLED` and `SD_RBAC_DISABLED` toggles have been removed.

### Audit Logging

All authentication events are logged in structured SIEM-ready format:

```json
{
  "event": "auth_audit",
  "action": "token_validation",
  "result": "success",
  "idp_type": "zitadel",
  "username": "user@example.com"
}
```

Fields: `event`, `action` (`token_validation` / `authorization`), `result` (`success` / `failure` / `denied`),
`idp_type` (`zitadel` / `local_hmac` / `unknown`), `username` (only when the configured username
claim is present), `reason` (omitted when empty).
