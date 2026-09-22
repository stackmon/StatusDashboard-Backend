# Integration Test Documentation

## Overview

All integration tests run against a real PostgreSQL database provisioned via
[testcontainers-go](https://github.com/testcontainers/testcontainers-go).
The database is seeded with fixture data (`tests/dump_test.sql`) and the full
application middleware chain (including RBAC) is exercised end-to-end.

**Run command:**

```bash
go test ./tests/ -v -timeout 300s -count=1
```

**Lint command:**

```bash
golangci-lint run ./tests/
```

---

## Test Infrastructure

| File | Lines | Purpose |
|------|------:|---------|
| `main_test.go` | 289 | `TestMain` (testcontainers bootstrap), `initTests`, route initialisation with production RBAC middleware, DB helpers (`truncateIncidents`, `restoreFixtureIncident`) |
| `rbac_helpers_test.go` | 278 | HMAC JWT signing (`testHMACSecret`), `tokenForRole`, pre-built tokens (`adminToken`, `operatorToken`, `creatorToken`), role constants, HTTP request helpers, event factory functions |
| `dump_test.sql` | — | Fixture data: 6 components (CCE, ECS, DCS × EU-DE/EU-NL), 1 resolved incident |

---

## Test Suite Summary

| Category | Files | Top-Level Tests | Subtests | Total |
|----------|------:|----------------:|---------:|------:|
| RBAC — Permissions | 1 | 5 | 27 | 32 |
| RBAC — Creation | 1 | 3 | 10 | 13 |
| RBAC — Visibility | 1 | 4 | 8 | 12 |
| RBAC — Workflow | 1 | 7 | 11 | 18 |
| RBAC — Version Conflict | 1 | 5 | 7 | 12 |
| RBAC — Token Validation | 1 | 3 | 2 | 5 |
| RBAC — Admin-Only Config | 1 | 4 | 10 | 14 |
| Auth (OAuth flow) | 1 | 1 | 0 | 1 |
| V1 API | 1 | 5 | 0 | 5 |
| V2 Events API | 1 | 10 | 17 | 27 |
| V2 Incidents API (deprecated) | 1 | 13 | 22 | 35 |
| V2 System Incidents | 1 | 11 | 2 | 13 |
| **Total** | **13** | **71** | **116** | **187** |

---

## RBAC Test Coverage

### 1. Permissions (`rbac_permissions_test.go`)

Tests verify that each role can only perform the actions allowed by the
[permissions matrix](../auth/permissions.md).

#### Operator Status Transitions — `TestPermissions_OperatorPatchMatrix`

| # | Subtest | From → To | Expected | Spec Ref |
|---|---------|-----------|----------|----------|
| 1 | `operator pending_review → reviewed` | pending_review → reviewed | 200 | FR-014 |
| 2 | `operator pending_review → cancelled` | pending_review → cancelled | 200 | FR-015 |
| 3 | `operator reviewed → planned` | reviewed → planned | 200 | FR-013 |
| 4 | `operator reviewed → cancelled` | reviewed → cancelled | 200 | FR-015 |
| 5 | `operator planned → in_progress` | planned → in_progress | 200 | FR-013 |
| 6 | `operator planned → cancelled` | planned → cancelled | 200 | FR-015 |
| 7 | `operator in_progress → completed` | in_progress → completed | 200 | FR-013 |
| 8 | `operator in_progress → cancelled` | in_progress → cancelled | 200 | FR-015 |

#### Admin Status Transitions — `TestPermissions_AdminPatchMatrix`

| # | Subtest | From → To | Expected | Spec Ref |
|---|---------|-----------|----------|----------|
| 1 | `admin pending_review → reviewed` | pending_review → reviewed | 200 | FR-015a |
| 2 | `admin pending_review → cancelled` | pending_review → cancelled | 200 | FR-015a |
| 3 | `admin reviewed → planned` | reviewed → planned | 200 | FR-015a |
| 4 | `admin planned → in_progress` | planned → in_progress | 200 | FR-015a |
| 5 | `admin planned → cancelled` | planned → cancelled | 200 | FR-015a |
| 6 | `admin in_progress → completed` | in_progress → completed | 200 | FR-015a |
| 7 | `admin in_progress → cancelled` | in_progress → cancelled | 200 | FR-015a |

#### Creator Restrictions — `TestPermissions_CreatorPatchRestrictions`

| # | Subtest | Scenario | Expected | Spec Ref |
|---|---------|----------|----------|----------|
| 1 | `can patch own pending_review to pending_review` | Edit own event in pending_review | 200 | FR-006 |
| 2 | `can cancel own pending_review` | Cancel own pending_review event | 200 | FR-007 |
| 3 | `cannot approve own event to reviewed` | Self-approval blocked | 409 | FR-009 |
| 4 | `cannot patch another creators event` | Cross-user modification blocked | 403 | FR-029 |
| 5 | `cannot patch reviewed event even if own` | Post-review modification blocked | 409 | FR-009 |
| 6 | `cannot patch planned event` | Post-plan modification blocked | 409 | FR-024 |

#### Unauthenticated and Unauthorized — `TestPermissions_NoRoleRejected`, `TestPermissions_UnauthenticatedRejected`

| # | Test | Scenario | Expected | Spec Ref |
|---|------|----------|----------|----------|
| 1 | `cannot create event` | Authenticated user without any RBAC group | 403 | FR-028 |
| 2 | `cannot patch event` | Authenticated user without any RBAC group | 403 | FR-028 |
| 3 | `no token on POST returns 401` | Missing Authorization header on POST | 401 | FR-027 |
| 4 | `no token on PATCH returns 401` | Missing Authorization header on PATCH | 401 | FR-027 |

---

### 2. Event Creation (`rbac_creation_test.go`)

#### Initial Status by Role — `TestCreation_RoleInitialStatus`

| # | Subtest | Role | Event Type | Expected Status | Spec Ref |
|---|---------|------|------------|-----------------|----------|
| 1 | `creator creates maintenance with pending_review status` | creator | maintenance | pending_review | FR-005 |
| 2 | `operator creates maintenance with planned status` | operator | maintenance | planned | FR-005a |
| 3 | `admin creates maintenance with planned status` | admin | maintenance | planned | FR-005b |

#### Incident Creation by All Roles — `TestCreation_IncidentByRoles`

| # | Subtest | Role | Event Type | Expected | Spec Ref |
|---|---------|------|------------|----------|----------|
| 1 | `creator creates incident` | creator | incident | 201 | FR-004 |
| 2 | `operator creates incident` | operator | incident | 201 | FR-013 |
| 3 | `admin creates incident` | admin | incident | 201 | FR-015a |

#### Maintenance Validation — `TestCreation_MaintenanceValidation`

| # | Subtest | Scenario | Expected | Spec Ref |
|---|---------|----------|----------|----------|
| 1 | `missing contact_email rejected` | No contact_email field | 400 | FR-008 |
| 2 | `invalid contact_email rejected` | Malformed email | 400 | FR-008a |
| 3 | `empty description rejected` | Empty description field | 400 | FR-008c |
| 4 | `non-zero impact for maintenance rejected` | impact ≠ 0 | 400 | FR-008d |

---

### 3. Visibility (`rbac_visibility_test.go`)

#### Unauthenticated Visibility — `TestVisibility_PendingReviewHiddenFromUnauth`

| # | Subtest | Scenario | Expected | Spec Ref |
|---|---------|----------|----------|----------|
| 1 | `GET list hides pending_review` | List events without token | pending_review events excluded | FR-022-1 |
| 2 | `GET by ID returns 404 for pending_review` | Get event by ID without token | 404 | FR-022-1 |

#### Authenticated Visibility — `TestVisibility_PendingReviewVisibleToAuth`

| # | Subtest | Scenario | Expected | Spec Ref |
|---|---------|----------|----------|----------|
| 1 | `creator can see pending_review by id` | Creator GET by ID | 200 | FR-012 |
| 2 | `operator can see pending_review by id` | Operator GET by ID | 200 | FR-012 |
| 3 | `admin can see pending_review by id` | Admin GET by ID | 200 | FR-012 |
| 4 | `auth user sees pending_review in list` | Authenticated GET list | pending_review events included | FR-012 |

#### Field Visibility — `TestVisibility_ContactEmailAndCreator`

| # | Subtest | Scenario | Expected | Spec Ref |
|---|---------|----------|----------|----------|
| 1 | `auth user sees contact_email and creator` | Authenticated GET | Fields present | FR-017, FR-019 |
| 2 | `unauth user does not see contact_email and creator` | Unauthenticated GET | Fields absent | FR-017a, FR-019a |

#### List Comparison — `TestVisibility_AuthVsUnauthEventList`

| # | Test | Scenario | Expected | Spec Ref |
|---|------|----------|----------|----------|
| 1 | `AuthVsUnauthEventList` | Compare authenticated vs unauthenticated event list lengths | Auth list ≥ unauth list | FR-022-1 |

---

### 4. Maintenance Workflow (`rbac_workflow_test.go`)

#### End-to-End Workflows

| # | Test | Workflow | Roles Involved | Spec Ref |
|---|------|----------|---------------|----------|
| 1 | `TestWorkflow_CreatorToCompletionViaOperator` | pending_review → reviewed → planned → in_progress → completed | creator + operator | FR-005, FR-014, FR-022 |
| 2 | `TestWorkflow_CreatorToCompletionViaAdmin` | pending_review → reviewed → planned → in_progress → completed | creator + admin | FR-005, FR-015a, FR-022 |
| 3 | `TestWorkflow_OperatorFullLifecycle` | planned → in_progress → completed (direct) | operator | FR-005a, FR-013 |
| 4 | `TestWorkflow_OperatorApprovesAndPlans` | pending_review → reviewed → planned | creator + operator | FR-014, FR-013 |
| 5 | `TestWorkflow_UpdateHistoryPreserved` | Full lifecycle with update history verification | creator + operator | FR-025 |

#### Cancellation Matrix — `TestWorkflow_CancellationFromAnyStatus`

| # | Subtest | Cancel From | By Role | Expected | Spec Ref |
|---|---------|-------------|---------|----------|----------|
| 1 | `operator cancels from pending_review` | pending_review | operator | 200 | FR-015 |
| 2 | `admin cancels from pending_review` | pending_review | admin | 200 | FR-015a |
| 3 | `operator cancels from reviewed` | reviewed | operator | 200 | FR-015 |
| 4 | `admin cancels from reviewed` | reviewed | admin | 200 | FR-015a |
| 5 | `operator cancels from planned` | planned | operator | 200 | FR-015 |
| 6 | `admin cancels from planned` | planned | admin | 200 | FR-015a |
| 7 | `operator cancels from in_progress` | in_progress | operator | 200 | FR-015 |
| 8 | `admin cancels from in_progress` | in_progress | admin | 200 | FR-015a |

#### Creator Blocked After Approval — `TestWorkflow_CreatorBlockedAfterApproval`

| # | Subtest | Status | Action | Expected | Spec Ref |
|---|---------|--------|--------|----------|----------|
| 1 | `creator blocked pending_review` | reviewed | Creator patch | 409 | FR-009 |
| 2 | `creator blocked planned` | planned | Creator patch | 409 | FR-024 |
| 3 | `creator blocked cancelled` | cancelled | Creator patch | 409 | FR-009 |

---

### 5. Version Conflict (`rbac_version_test.go`)

#### Nil Version on Maintenance PATCH — `TestVersion_NilVersionOnMaintenancePatch`

| # | Subtest | Role | Scenario | Expected | Spec Ref |
|---|---------|------|----------|----------|----------|
| 1 | `creator nil version rejected` | creator | PATCH without version field | 400 | FR-015-2 |
| 2 | `operator nil version rejected` | operator | PATCH without version field | 400 | FR-015-2 |
| 3 | `admin nil version rejected` | admin | PATCH without version field | 400 | FR-015-2 |

#### Wrong Version on Maintenance PATCH — `TestVersion_WrongVersionOnMaintenancePatch`

| # | Subtest | Scenario | Expected | Spec Ref |
|---|---------|----------|----------|----------|
| 1 | `stale version returns 409` | Version = current − 1 | 409 | FR-032 |
| 2 | `completely wrong version returns 409` | Version = 999 | 409 | FR-032 |

#### Incident Version (Not Enforced) — `TestVersion_NilVersionOnIncidentPatch`, `TestVersion_WrongVersionOnIncidentPatch`

| # | Test / Subtest | Scenario | Expected | Notes |
|---|----------------|----------|----------|-------|
| 1 | `nil version on incident patch is accepted` | PATCH incident without version | 200 | Version only enforced for maintenance |
| 2 | `explicit version on incident patch is accepted` | PATCH incident with wrong version | 200 | Version only enforced for maintenance |

#### Concurrent Modification — `TestVersion_ConcurrentMaintenancePatch`

| # | Test | Scenario | Expected | Spec Ref |
|---|------|----------|----------|----------|
| 1 | `ConcurrentMaintenancePatch` | Two operators PATCH same event; second gets stale version | First 200, second 409 | FR-015-1 |

---

### 6. Token Validation (`rbac_token_test.go`)

| # | Test / Subtest | Scenario | Expected | Spec Ref |
|---|----------------|----------|----------|----------|
| 1 | `POST returns 401` | Token signed with wrong HMAC key | 401 | FR-026 |
| 2 | `PATCH returns 401` | Token signed with wrong HMAC key | 401 | FR-026 |
| 3 | `InvalidGroupsClaim` | Token with a non-array groups claim (local HMAC branch) | 403 | FR-002 |
| 4 | `ValidClaimsSucceeds` | Token with valid roles and username (local HMAC branch) | 201 | FR-002a |

---

### 7. Admin-Only Configuration (`rbac_admin_only_test.go`)

Tests verify correct behavior when only `SD_RBAC_GROUPS_ADMINS` is configured
(creator and operator role names are empty strings).

#### Admin CRUD — `TestAdminOnly_AdminCRUD`

| # | Subtest | Scenario | Expected | Notes |
|---|---------|----------|----------|-------|
| 1 | `admin POST creates event` | Admin creates maintenance event | 200 | Only recognized role |
| 2 | `admin GET list returns events` | Admin lists events | Non-empty list | — |
| 3 | `admin GET single event` | Admin retrieves event by ID | 200 | — |
| 4 | `admin PATCH transitions event` | Admin changes event status | 200 | — |

#### Creator Rejected — `TestAdminOnly_CreatorRejected`

| # | Subtest | Scenario | Expected | Notes |
|---|---------|----------|----------|-------|
| 1 | `creator POST rejected with 403` | Creator group not configured | 403 | No matching RBAC group |
| 2 | `creator PATCH rejected with 403` | Creator attempts PATCH | 403 | — |

#### Operator Rejected — `TestAdminOnly_OperatorRejected`

| # | Subtest | Scenario | Expected | Notes |
|---|---------|----------|----------|-------|
| 1 | `operator POST rejected with 403` | Operator group not configured | 403 | No matching RBAC group |
| 2 | `operator PATCH rejected with 403` | Operator attempts PATCH | 403 | — |

#### Unauthenticated GET — `TestAdminOnly_UnauthenticatedGET`

| # | Subtest | Scenario | Expected | Notes |
|---|---------|----------|----------|-------|
| 1 | `unauth GET list succeeds` | Unauthenticated list request | Non-empty list | Soft-auth allows read |
| 2 | `unauth GET single event succeeds` | Unauthenticated get by ID | 200 | Planned events visible |

---

## API Endpoint Test Coverage

### V1 API (`v1_test.go`)

| # | Test | Endpoint | Method | Scenario |
|---|------|----------|--------|----------|
| 1 | `TestV1GetIncidentsHandler` | `/v1/incidents` | GET | Returns fixture incidents in V1 format |
| 2 | `TestV1GetComponentsStatusHandler` | `/v1/component_status` | GET | Returns components with incidents; filters auth-restricted events |
| 3 | `TestV1PostComponentsStatusHandlerNegative` | `/v1/component_status` | POST | Rejects invalid payloads (missing fields, wrong format) |
| 4 | `TestV1PostComponentsStatusHandler` | `/v1/component_status` | POST | Creates system incidents via component status reporting |
| 5 | `TestV1MaintenancePreventCreation` | `/v1/component_status` | POST | Skips incident creation when active maintenance exists |

### V2 Events API (`v2_events_test.go`)

| # | Test | Endpoint | Method | Scenario |
|---|------|----------|--------|----------|
| 1 | `TestV2PostEventsHandlerNegative` | `/v2/events` | POST | Rejects invalid payloads |
| 2 | `TestV2PostEventsHandler` | `/v2/events` | POST | Creates incidents and maintenance events |
| 3 | `TestV2PatchEventHandlerNegative` | `/v2/events/:id` | PATCH | Rejects invalid patches |
| 4 | `TestV2PatchEventHandler` | `/v2/events/:id` | PATCH | Status transitions, field updates |
| 5 | `TestV2PostEventExtractHandler` | `/v2/events/:id/extract` | POST | Extracts components from events |
| 6 | `TestV2GetEventsFilteredHandler` | `/v2/events` | GET | Filters: impact, component_id, system, active (6 subtests) |
| 7 | `TestV2GetEventsHandler` | `/v2/events` | GET | Pagination: default, limit+page variations (4 subtests) |
| 8 | `TestV2PostEventsMaintenanceHandler` | `/v2/events` | POST | Maintenance creation with components |
| 9 | `TestV2PostEventsInfoWithExistingEventsHandler` | `/v2/events` | POST | Info event creation with pre-existing events |
| 10 | `TestV2PatchEventUpdateHandler` | `/v2/events/:id/updates/:uid` | PATCH | Update text editing (7 subtests) |

### V2 Incidents API — Deprecated (`v2_test.go`)

| # | Test | Endpoint | Method | Scenario |
|---|------|----------|--------|----------|
| 1 | `TestV2GetIncidentsHandler` | `/v2/incidents` | GET | List incidents |
| 2 | `TestV2GetComponentsHandler` | `/v2/components` | GET | List components |
| 3 | `TestV2PostIncidentsHandlerNegative` | `/v2/incidents` | POST | Rejects invalid payloads |
| 4 | `TestV2PostIncidentsHandler` | `/v2/incidents` | POST | Creates incidents |
| 5 | `TestV2PatchIncidentHandlerNegative` | `/v2/incidents/:id` | PATCH | Rejects invalid patches |
| 6 | `TestV2PatchIncidentHandler` | `/v2/incidents/:id` | PATCH | Status transitions |
| 7 | `TestV2PostIncidentExtractHandler` | `/v2/incidents/:id/extract` | POST | Component extraction |
| 8 | `TestV2CreateComponentAndList` | `/v2/components` | POST+GET | Component creation and listing |
| 9 | `TestV2GetIncidentsFilteredHandler` | `/v2/incidents` | GET | Filters: date, impact, component, system, active, combinations (15 subtests) |
| 10 | `TestV2PostMaintenanceHandler` | `/v2/incidents` | POST | Maintenance creation |
| 11 | `TestV2PostInfoWithExistingEventsHandler` | `/v2/incidents` | POST | Info event with existing events |
| 12 | `TestV2GetComponentsAvailability` | `/v2/availability` | GET | Component availability calculation |
| 13 | `TestV2PatchIncidentUpdateHandler` | `/v2/incidents/:id/updates/:uid` | PATCH | Update text editing (7 subtests) |

### V2 System Incidents (`v2_system_incident_test.go`)

| # | Test | Endpoint | Method | Scenario |
|---|------|----------|--------|----------|
| 1 | `TestV2SystemIncidentCreationWrongType` | `/v2/incidents` | POST | Rejects maintenance/info types for system incidents (2 subtests) |
| 2 | `TestV2SystemIncidentCreationNoActiveEvents` | `/v2/incidents` | POST | Creates new system incident when none exist |
| 3 | `TestV2SystemIncidentCreationWithMaintenance` | `/v2/incidents` | POST | Handles component with active maintenance |
| 4 | `TestV2SystemIncidentCreationWithNonSystemIncident` | `/v2/incidents` | POST | Handles component with non-system incident |
| 5 | `TestV2SystemIncidentSameImpact` | `/v2/incidents` | POST | Adds component to existing same-impact incident |
| 6 | `TestV2SystemIncidentHigherImpact` | `/v2/incidents` | POST | Creates new incident with higher impact |
| 7 | `TestV2SystemIncidentLowerImpactSingleComponent` | `/v2/incidents` | POST | Raises impact when single component |
| 8 | `TestV2SystemIncidentLowerImpactMultiComponent` | `/v2/incidents` | POST | Moves component to higher-impact incident |
| 9 | `TestV2SystemIncidentReuseExisting` | `/v2/incidents` | POST | Reuses existing incident with matching impact |
| 10 | `TestV2SystemIncidentMultipleComponents` | `/v2/incidents` | POST | Processes multiple components in one request |
| 11 | `TestV2SystemIncidentMixedScenarios` | `/v2/incidents` | POST | Complex multi-component mixed impact scenarios |

---

## Spec Requirement Traceability

The table below maps each functional requirement from the
[RBAC specification](../../specs/001-maintenance-rbac/spec.md) to the
test(s) that verify it.

| Requirement | Description | Covered By |
|-------------|-------------|------------|
| FR-002 | Extract roles from the JWT token | `TestToken_InvalidGroupsClaim` |
| FR-002a | Map role names via SD_RBAC_GROUPS_* env vars | `TestToken_ValidClaimsSucceeds` |
| FR-004 | Creator can create maintenance events | `TestCreation_RoleInitialStatus` |
| FR-005 | Creator → pending_review initial status | `TestCreation_RoleInitialStatus/creator_creates_maintenance_with_pending_review_status` |
| FR-005a | Operator → planned initial status | `TestCreation_RoleInitialStatus/operator_creates_maintenance_with_planned_status` |
| FR-005b | Admin → planned initial status | `TestCreation_RoleInitialStatus/admin_creates_maintenance_with_planned_status` |
| FR-006 | Creator can modify only pending_review | `TestPermissions_CreatorPatchRestrictions/can_patch_own_pending_review_to_pending_review` |
| FR-007 | Creator can cancel only from pending_review | `TestPermissions_CreatorPatchRestrictions/can_cancel_own_pending_review` |
| FR-008 | Require valid email for maintenance | `TestCreation_MaintenanceValidation/missing_contact_email_rejected` |
| FR-008a | RFC 5322 email validation | `TestCreation_MaintenanceValidation/invalid_contact_email_rejected` |
| FR-008c | Required fields validation | `TestCreation_MaintenanceValidation/empty_description_rejected` |
| FR-008d | Reject invalid submissions with 400 | `TestCreation_MaintenanceValidation/non-zero_impact_for_maintenance_rejected` |
| FR-009 | Reject creator modifications when status ≠ pending_review | `TestPermissions_CreatorPatchRestrictions/cannot_approve_own_event_to_reviewed`, `cannot_patch_reviewed_event_even_if_own` |
| FR-013 | Operator can PATCH events unrestricted | `TestPermissions_OperatorPatchMatrix` (all 8 subtests) |
| FR-014 | Operator approval: pending_review → reviewed | `TestPermissions_OperatorPatchMatrix/operator_pending_review_→_reviewed` |
| FR-015 | Operator can cancel from any status | `TestWorkflow_CancellationFromAnyStatus` (operator subtests) |
| FR-015-1 | First concurrent approval wins, second gets 409 | `TestVersion_ConcurrentMaintenancePatch` |
| FR-015-2 | Version conflict detection | `TestVersion_NilVersionOnMaintenancePatch`, `TestVersion_WrongVersionOnMaintenancePatch` |
| FR-015a | Admin unrestricted access | `TestPermissions_AdminPatchMatrix` (all 7 subtests) |
| FR-016 | Store creator user_id | `TestVisibility_ContactEmailAndCreator/auth_user_sees_contact_email_and_creator` |
| FR-017 | Expose creator to authenticated users | `TestVisibility_ContactEmailAndCreator/auth_user_sees_contact_email_and_creator` |
| FR-017a | Hide creator from unauthenticated users | `TestVisibility_ContactEmailAndCreator/unauth_user_does_not_see_contact_email_and_creator` |
| FR-019 | Store contact email | `TestVisibility_ContactEmailAndCreator/auth_user_sees_contact_email_and_creator` |
| FR-019a | Hide contact_email from unauthenticated users | `TestVisibility_ContactEmailAndCreator/unauth_user_does_not_see_contact_email_and_creator` |
| FR-022 | Status workflow: pending_review → reviewed → planned → ... | `TestWorkflow_CreatorToCompletionViaOperator`, `TestWorkflow_CreatorToCompletionViaAdmin` |
| FR-022-1 | Hide pending_review/reviewed from unauthenticated | `TestVisibility_PendingReviewHiddenFromUnauth` |
| FR-024 | Creator cannot skip statuses | `TestPermissions_CreatorPatchRestrictions/cannot_patch_planned_event` |
| FR-025 | Audit trail in incident_status table | `TestWorkflow_UpdateHistoryPreserved` |
| FR-026 | Validate JWT tokens | `TestToken_InvalidSignature` |
| FR-027 | 401 for missing JWT | `TestPermissions_UnauthenticatedRejected` |
| FR-028 | 403 for insufficient permissions | `TestPermissions_NoRoleRejected` |
| FR-029 | Validate creator user_id for cross-user access | `TestPermissions_CreatorPatchRestrictions/cannot_patch_another_creators_event` |
| FR-032 | 409 on version mismatch | `TestVersion_WrongVersionOnMaintenancePatch` |

---

## Areas Not Covered by Integration Tests

The following spec requirements are not directly tested at the integration
level and should be verified through other means (unit tests, manual testing,
or future test additions):

| Requirement | Description | Reason |
|-------------|-------------|--------|
| FR-011 | Operator sees badge count of pending_review events | Frontend/UI concern; API provides `status` filter |
| FR-018 | Display creator as "Creator or Author" in UI | Frontend/UI concern |
| FR-020 | Display contact email in UI | Frontend/UI concern |
| FR-021 | No actual email notifications sent | Not testable at API level |
| FR-023 | Checker: reviewed → planned transition | Requires checker goroutine; not exercised in API tests |
| FR-023a–d | Checker validation and timing behaviour | Same as above |
| FR-008b | end_date after start_date validation | Covered implicitly in `TestV2PostEventsHandler` and `TestV2PatchEventHandler` negative cases |

---

## Test File Reference

| File | Category | Tests |
|------|----------|-------|
| `auth_test.go` | OAuth | `TestAuth` |
| `rbac_admin_only_test.go` | RBAC | `TestAdminOnly_AdminCRUD`, `TestAdminOnly_CreatorRejected`, `TestAdminOnly_OperatorRejected`, `TestAdminOnly_UnauthenticatedGET` |
| `rbac_creation_test.go` | RBAC | `TestCreation_RoleInitialStatus`, `TestCreation_IncidentByRoles`, `TestCreation_MaintenanceValidation` |
| `rbac_permissions_test.go` | RBAC | `TestPermissions_OperatorPatchMatrix`, `TestPermissions_AdminPatchMatrix`, `TestPermissions_CreatorPatchRestrictions`, `TestPermissions_NoRoleRejected`, `TestPermissions_UnauthenticatedRejected` |
| `rbac_token_test.go` | RBAC | `TestToken_InvalidSignature`, `TestToken_InvalidGroupsClaim`, `TestToken_ValidClaimsSucceeds` |
| `rbac_version_test.go` | RBAC | `TestVersion_NilVersionOnMaintenancePatch`, `TestVersion_WrongVersionOnMaintenancePatch`, `TestVersion_NilVersionOnIncidentPatch`, `TestVersion_WrongVersionOnIncidentPatch`, `TestVersion_ConcurrentMaintenancePatch` |
| `rbac_visibility_test.go` | RBAC | `TestVisibility_PendingReviewHiddenFromUnauth`, `TestVisibility_PendingReviewVisibleToAuth`, `TestVisibility_ContactEmailAndCreator`, `TestVisibility_AuthVsUnauthEventList` |
| `rbac_workflow_test.go` | RBAC | `TestWorkflow_CreatorToCompletionViaOperator`, `TestWorkflow_CreatorToCompletionViaAdmin`, `TestWorkflow_OperatorFullLifecycle`, `TestWorkflow_CancellationFromAnyStatus`, `TestWorkflow_CreatorBlockedAfterApproval`, `TestWorkflow_OperatorApprovesAndPlans`, `TestWorkflow_UpdateHistoryPreserved` |
| `v1_test.go` | V1 API | `TestV1GetIncidentsHandler`, `TestV1GetComponentsStatusHandler`, `TestV1PostComponentsStatusHandlerNegative`, `TestV1PostComponentsStatusHandler`, `TestV1MaintenancePreventCreation` |
| `v2_events_test.go` | V2 Events | `TestV2PostEventsHandlerNegative`, `TestV2PostEventsHandler`, `TestV2PatchEventHandlerNegative`, `TestV2PatchEventHandler`, `TestV2PostEventExtractHandler`, `TestV2GetEventsFilteredHandler`, `TestV2GetEventsHandler`, `TestV2PostEventsMaintenanceHandler`, `TestV2PostEventsInfoWithExistingEventsHandler`, `TestV2PatchEventUpdateHandler` |
| `v2_system_incident_test.go` | V2 System | `TestV2SystemIncidentCreationWrongType`, `TestV2SystemIncidentCreationNoActiveEvents`, `TestV2SystemIncidentCreationWithMaintenance`, `TestV2SystemIncidentCreationWithNonSystemIncident`, `TestV2SystemIncidentSameImpact`, `TestV2SystemIncidentHigherImpact`, `TestV2SystemIncidentLowerImpactSingleComponent`, `TestV2SystemIncidentLowerImpactMultiComponent`, `TestV2SystemIncidentReuseExisting`, `TestV2SystemIncidentMultipleComponents`, `TestV2SystemIncidentMixedScenarios` |
| `v2_test.go` | V2 Incidents (deprecated) | `TestV2GetIncidentsHandler`, `TestV2GetComponentsHandler`, `TestV2PostIncidentsHandlerNegative`, `TestV2PostIncidentsHandler`, `TestV2PatchIncidentHandlerNegative`, `TestV2PatchIncidentHandler`, `TestV2PostIncidentExtractHandler`, `TestV2CreateComponentAndList`, `TestV2GetIncidentsFilteredHandler`, `TestV2PostMaintenanceHandler`, `TestV2PostInfoWithExistingEventsHandler`, `TestV2GetComponentsAvailability`, `TestV2PatchIncidentUpdateHandler` |

---

## Unit Test Coverage

Unit tests run without external dependencies (no database, no identity provider).

**Run command:**

```bash
go test ./internal/... -count=1
```

### Coverage Summary

| Package | Coverage | Key Test Files |
|---------|----------|---------------|
| `internal/conf` | 72.9% | `conf_test.go` — Validate, MinSecretKeyLength, PortValidation, FillDefaults, maskSecret, sanitizeDBString, mergeConfigs, Log |
| `internal/api` | 49.6% | `middleware_test.go` — OIDC and HMAC verification, AuthenticationMW, SetJWTClaims, RBAC authorization |
| `internal/api/rbac` | 100% | `rbac_test.go` — HasAuthorizedRole, role resolution, role names |
| `internal/api/auth` | 92.4% | `auth_test.go` — HMAC and OIDC verification dispatch, signing method selection; `oidc_test.go` — discovery, JWKS caching, roles claim extraction, token validation |
