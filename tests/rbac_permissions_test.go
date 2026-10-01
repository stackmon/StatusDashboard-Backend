package tests

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/stackmon/otc-status-dashboard/internal/event"
)

// advanceFromPlanned transitions an event from planned to the target status
// step-by-step using the admin token.
func advanceFromPlanned(t *testing.T, r *gin.Engine, eventID int, target event.Status) {
	t.Helper()

	path := pathFromPlanned(target)
	for _, s := range path {
		transitionTo(t, r, eventID, s, adminToken)
	}
}

// pathFromPlanned returns the intermediate transitions from planned → target.
func pathFromPlanned(target event.Status) []event.Status {
	switch target {
	case event.MaintenancePlanned:
		return nil
	case event.MaintenanceInProgress:
		return []event.Status{event.MaintenanceInProgress}
	case event.MaintenanceCompleted:
		return []event.Status{event.MaintenanceInProgress, event.MaintenanceCompleted}
	case event.MaintenanceCancelled:
		return []event.Status{event.MaintenanceCancelled}
	case event.MaintenanceModified:
		return []event.Status{event.MaintenanceModified}
	default:
		return nil
	}
}

// createAtStatus creates a maintenance event and advances it to the given
// status. For pending_review it creates as creator; for others as admin
// (which starts at planned) and advances from there.
func createAtStatus(t *testing.T, r *gin.Engine, target event.Status) int {
	t.Helper()

	if target == event.MaintenancePendingReview {
		resp := createEventOK(t, r, maintenanceData(), creatorTokenA)
		return resp.Result[0].IncidentID
	}

	// Reviewed comes before planned in the workflow — create as creator
	// then approve via admin.
	if target == event.MaintenanceReviewed {
		resp := createEventOK(t, r, maintenanceData(), creatorTokenA)
		eventID := resp.Result[0].IncidentID
		transitionTo(t, r, eventID, event.MaintenanceReviewed, adminToken)
		return eventID
	}

	// All other statuses: create as admin (→ planned) then advance.
	resp := createEventOK(t, r, maintenanceData(), adminToken)
	eventID := resp.Result[0].IncidentID
	advanceFromPlanned(t, r, eventID, target)
	return eventID
}

// ---------------------------------------------------------------------------
// Operator permissions (unrestricted — same as admin for maintenance events)
// ---------------------------------------------------------------------------

// TestPermissions_OperatorPatchMatrix verifies that operator can PATCH
// maintenance events in any status (unrestricted, same as admin).
func TestPermissions_OperatorPatchMatrix(t *testing.T) {
	r := initRBACTests(t)

	tests := []struct {
		name       string
		fromStatus event.Status
		toStatus   event.Status
	}{
		{"pending_review → reviewed", event.MaintenancePendingReview, event.MaintenanceReviewed},
		{"pending_review → cancelled", event.MaintenancePendingReview, event.MaintenanceCancelled},
		{"reviewed → planned", event.MaintenanceReviewed, event.MaintenancePlanned},
		{"reviewed → cancelled", event.MaintenanceReviewed, event.MaintenanceCancelled},
		{"planned → in_progress", event.MaintenancePlanned, event.MaintenanceInProgress},
		{"planned → cancelled", event.MaintenancePlanned, event.MaintenanceCancelled},
		{"in_progress → completed", event.MaintenanceInProgress, event.MaintenanceCompleted},
		{"in_progress → cancelled", event.MaintenanceInProgress, event.MaintenanceCancelled},
	}

	for _, tc := range tests {
		t.Run("operator_"+tc.name, func(t *testing.T) {
			truncateIncidents(t)

			eventID := createAtStatus(t, r, tc.fromStatus)
			inc := getEventOK(t, r, eventID, operatorToken)
			assertPatchStatus(t, r, eventID, tc.toStatus, intPtr(eventVersion(inc)), operatorToken, http.StatusOK)
		})
	}
}

// ---------------------------------------------------------------------------
// Admin permissions (unrestricted)
// ---------------------------------------------------------------------------

// TestPermissions_AdminPatchMatrix verifies that admin can PATCH
// maintenance events in any status.
func TestPermissions_AdminPatchMatrix(t *testing.T) {
	r := initRBACTests(t)

	tests := []struct {
		name       string
		fromStatus event.Status
		toStatus   event.Status
	}{
		{"pending_review → reviewed", event.MaintenancePendingReview, event.MaintenanceReviewed},
		{"pending_review → cancelled", event.MaintenancePendingReview, event.MaintenanceCancelled},
		{"reviewed → planned", event.MaintenanceReviewed, event.MaintenancePlanned},
		{"planned → in_progress", event.MaintenancePlanned, event.MaintenanceInProgress},
		{"planned → cancelled", event.MaintenancePlanned, event.MaintenanceCancelled},
		{"in_progress → completed", event.MaintenanceInProgress, event.MaintenanceCompleted},
		{"in_progress → cancelled", event.MaintenanceInProgress, event.MaintenanceCancelled},
	}

	for _, tc := range tests {
		t.Run("admin_"+tc.name, func(t *testing.T) {
			truncateIncidents(t)

			eventID := createAtStatus(t, r, tc.fromStatus)
			inc := getEventOK(t, r, eventID, adminToken)
			assertPatchStatus(t, r, eventID, tc.toStatus, intPtr(eventVersion(inc)), adminToken, http.StatusOK)
		})
	}
}

// ---------------------------------------------------------------------------
// Creator permissions (restricted: own events, pending_review only)
// ---------------------------------------------------------------------------

// TestPermissions_CreatorPatchRestrictions verifies creator-specific
// restrictions: own events only, pending_review only, limited target statuses.
func TestPermissions_CreatorPatchRestrictions(t *testing.T) {
	r := initRBACTests(t)

	t.Run("can patch own pending_review to pending_review", func(t *testing.T) {
		truncateIncidents(t)
		resp := createEventOK(t, r, maintenanceData(), creatorTokenA)
		inc := getEventOK(t, r, resp.Result[0].IncidentID, creatorTokenA)
		assertPatchStatus(t, r, inc.ID, event.MaintenancePendingReview, intPtr(eventVersion(inc)), creatorTokenA, http.StatusOK)
	})

	t.Run("can cancel own pending_review", func(t *testing.T) {
		truncateIncidents(t)
		resp := createEventOK(t, r, maintenanceData(), creatorTokenA)
		inc := getEventOK(t, r, resp.Result[0].IncidentID, creatorTokenA)
		assertPatchStatus(t, r, inc.ID, event.MaintenanceCancelled, intPtr(eventVersion(inc)), creatorTokenA, http.StatusOK)
	})

	t.Run("cannot approve own event to reviewed", func(t *testing.T) {
		truncateIncidents(t)
		resp := createEventOK(t, r, maintenanceData(), creatorTokenA)
		inc := getEventOK(t, r, resp.Result[0].IncidentID, creatorTokenA)
		assertPatchStatus(t, r, inc.ID, event.MaintenanceReviewed, intPtr(eventVersion(inc)), creatorTokenA, http.StatusConflict)
	})

	t.Run("cannot patch another creators event", func(t *testing.T) {
		truncateIncidents(t)
		resp := createEventOK(t, r, maintenanceData(), creatorTokenA)
		inc := getEventOK(t, r, resp.Result[0].IncidentID, creatorTokenA)
		assertPatchStatus(t, r, inc.ID, event.MaintenancePendingReview, intPtr(eventVersion(inc)), creatorTokenB, http.StatusForbidden)
	})

	t.Run("cannot patch reviewed event even if own", func(t *testing.T) {
		truncateIncidents(t)
		resp := createEventOK(t, r, maintenanceData(), creatorTokenA)
		eventID := resp.Result[0].IncidentID
		transitionTo(t, r, eventID, event.MaintenanceReviewed, adminToken)
		inc := getEventOK(t, r, eventID, creatorTokenA)
		assertPatchStatus(t, r, eventID, event.MaintenanceCancelled, intPtr(eventVersion(inc)), creatorTokenA, http.StatusConflict)
	})

	t.Run("cannot patch planned event", func(t *testing.T) {
		truncateIncidents(t)
		resp := createEventOK(t, r, maintenanceData(), creatorTokenA)
		eventID := resp.Result[0].IncidentID
		transitionTo(t, r, eventID, event.MaintenanceReviewed, adminToken)
		transitionTo(t, r, eventID, event.MaintenancePlanned, adminToken)
		inc := getEventOK(t, r, eventID, creatorTokenA)
		assertPatchStatus(t, r, eventID, event.MaintenanceCancelled, intPtr(eventVersion(inc)), creatorTokenA, http.StatusConflict)
	})
}

// ---------------------------------------------------------------------------
// No-role and unauthenticated
// ---------------------------------------------------------------------------

// TestPermissions_NoRoleRejected verifies that users with no recognized
// RBAC group are denied all write operations.
func TestPermissions_NoRoleRejected(t *testing.T) {
	r := initRBACTests(t)

	t.Run("cannot create event", func(t *testing.T) {
		truncateIncidents(t)
		w, _ := createEvent(t, r, maintenanceData(), noRoleToken)
		assertHTTPStatus(t, w.Code, http.StatusForbidden)
	})

	t.Run("cannot patch event", func(t *testing.T) {
		truncateIncidents(t)
		resp := createEventOK(t, r, maintenanceData(), creatorTokenA)
		inc := getEventOK(t, r, resp.Result[0].IncidentID, creatorTokenA)
		assertPatchStatus(t, r, inc.ID, event.MaintenancePendingReview, intPtr(eventVersion(inc)), noRoleToken, http.StatusForbidden)
	})
}

// TestPermissions_UnauthenticatedRejected verifies that requests without
// a valid token are rejected for all write operations.
func TestPermissions_UnauthenticatedRejected(t *testing.T) {
	r := initRBACTests(t)

	t.Run("no token on POST returns 401", func(t *testing.T) {
		truncateIncidents(t)
		w, _ := createEvent(t, r, maintenanceData(), "")
		assertHTTPStatus(t, w.Code, http.StatusUnauthorized)
	})

	t.Run("no token on PATCH returns 401", func(t *testing.T) {
		truncateIncidents(t)
		resp := createEventOK(t, r, maintenanceData(), creatorTokenA)
		inc := getEventOK(t, r, resp.Result[0].IncidentID, creatorTokenA)
		assertPatchStatus(t, r, inc.ID, event.MaintenancePendingReview, intPtr(eventVersion(inc)), "", http.StatusUnauthorized)
	})
}

func assertHTTPStatus(t *testing.T, got, want int) {
	t.Helper()
	if got != want {
		t.Errorf("HTTP status: got %d, want %d", got, want)
	}
}
