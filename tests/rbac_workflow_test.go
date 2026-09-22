package tests

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackmon/otc-status-dashboard/internal/event"
)

// TestWorkflow_CreatorToCompletionViaOperator verifies the full lifecycle:
// creator → pending_review → operator approves → reviewed → planned →
// in_progress → completed.
func TestWorkflow_CreatorToCompletionViaOperator(t *testing.T) {
	r := initRBACTests(t)
	truncateIncidents(t)

	// Step 1: Creator creates → pending_review.
	resp := createEventOK(t, r, maintenanceData(), creatorTokenA)
	eventID := resp.Result[0].IncidentID

	inc := getEventOK(t, r, eventID, creatorTokenA)
	assert.Equal(t, event.MaintenancePendingReview, lastStatus(inc))

	// Step 2: Operator approves → reviewed.
	inc = transitionTo(t, r, eventID, event.MaintenanceReviewed, operatorToken)
	assert.Equal(t, event.MaintenanceReviewed, lastStatus(inc))

	// Step 3: Operator plans → planned.
	inc = transitionTo(t, r, eventID, event.MaintenancePlanned, operatorToken)
	assert.Equal(t, event.MaintenancePlanned, lastStatus(inc))

	// Step 4: Operator starts → in_progress.
	inc = transitionTo(t, r, eventID, event.MaintenanceInProgress, operatorToken)
	assert.Equal(t, event.MaintenanceInProgress, lastStatus(inc))

	// Step 5: Operator completes → completed.
	inc = transitionTo(t, r, eventID, event.MaintenanceCompleted, operatorToken)
	assert.Equal(t, event.MaintenanceCompleted, lastStatus(inc))
}

// TestWorkflow_CreatorToCompletionViaAdmin verifies the full lifecycle
// driven entirely by admin after creator submission.
func TestWorkflow_CreatorToCompletionViaAdmin(t *testing.T) {
	r := initRBACTests(t)
	truncateIncidents(t)

	resp := createEventOK(t, r, maintenanceData(), creatorTokenA)
	eventID := resp.Result[0].IncidentID

	inc := getEventOK(t, r, eventID, creatorTokenA)
	assert.Equal(t, event.MaintenancePendingReview, lastStatus(inc))

	// Admin drives the entire workflow.
	for _, status := range []event.Status{
		event.MaintenanceReviewed,
		event.MaintenancePlanned,
		event.MaintenanceInProgress,
		event.MaintenanceCompleted,
	} {
		inc = transitionTo(t, r, eventID, status, adminToken)
		assert.Equal(t, status, lastStatus(inc))
	}
}

// TestWorkflow_OperatorFullLifecycle verifies that operator can manage
// the entire lifecycle of an event they created (starts at planned).
func TestWorkflow_OperatorFullLifecycle(t *testing.T) {
	r := initRBACTests(t)
	truncateIncidents(t)

	resp := createEventOK(t, r, maintenanceData(), operatorToken)
	eventID := resp.Result[0].IncidentID

	inc := getEventOK(t, r, eventID, operatorToken)
	assert.Equal(t, event.MaintenancePlanned, lastStatus(inc))

	for _, status := range []event.Status{
		event.MaintenanceInProgress,
		event.MaintenanceCompleted,
	} {
		inc = transitionTo(t, r, eventID, status, operatorToken)
		assert.Equal(t, status, lastStatus(inc))
	}
}

// TestWorkflow_CancellationFromAnyStatus verifies that operator and admin
// can cancel a maintenance event from any active status.
func TestWorkflow_CancellationFromAnyStatus(t *testing.T) {
	r := initRBACTests(t)

	cancellableStatuses := []event.Status{
		event.MaintenancePendingReview,
		event.MaintenanceReviewed,
		event.MaintenancePlanned,
		event.MaintenanceInProgress,
	}

	for _, fromStatus := range cancellableStatuses {
		t.Run("operator_cancels_from_"+string(fromStatus), func(t *testing.T) {
			truncateIncidents(t)
			eventID := createAtStatus(t, r, fromStatus)
			inc := getEventOK(t, r, eventID, operatorToken)
			assertPatchStatus(t, r, eventID, event.MaintenanceCancelled, intPtr(eventVersion(inc)), operatorToken, http.StatusOK)
		})

		t.Run("admin_cancels_from_"+string(fromStatus), func(t *testing.T) {
			truncateIncidents(t)
			eventID := createAtStatus(t, r, fromStatus)
			inc := getEventOK(t, r, eventID, adminToken)
			assertPatchStatus(t, r, eventID, event.MaintenanceCancelled, intPtr(eventVersion(inc)), adminToken, http.StatusOK)
		})
	}
}

// TestWorkflow_CreatorBlockedAfterApproval verifies that once an event
// is approved (reviewed), the creator can no longer modify it.
func TestWorkflow_CreatorBlockedAfterApproval(t *testing.T) {
	r := initRBACTests(t)
	truncateIncidents(t)

	resp := createEventOK(t, r, maintenanceData(), creatorTokenA)
	eventID := resp.Result[0].IncidentID

	// Operator approves.
	transitionTo(t, r, eventID, event.MaintenanceReviewed, operatorToken)

	inc := getEventOK(t, r, eventID, creatorTokenA)
	version := intPtr(eventVersion(inc))

	// Creator attempts various transitions — all should fail.
	statuses := []event.Status{
		event.MaintenancePendingReview,
		event.MaintenancePlanned,
		event.MaintenanceCancelled,
	}
	for _, s := range statuses {
		t.Run("creator_blocked_"+string(s), func(t *testing.T) {
			assertPatchStatus(t, r, eventID, s, version, creatorTokenA, http.StatusConflict)
		})
	}
}

// TestWorkflow_OperatorApprovesAndPlans verifies the operator's ability
// to take a pending_review event through approval and planning.
func TestWorkflow_OperatorApprovesAndPlans(t *testing.T) {
	r := initRBACTests(t)
	truncateIncidents(t)

	resp := createEventOK(t, r, maintenanceData(), creatorTokenA)
	eventID := resp.Result[0].IncidentID

	// Approve.
	inc := transitionTo(t, r, eventID, event.MaintenanceReviewed, operatorToken)
	assert.Equal(t, event.MaintenanceReviewed, lastStatus(inc))

	// Plan.
	inc = transitionTo(t, r, eventID, event.MaintenancePlanned, operatorToken)
	assert.Equal(t, event.MaintenancePlanned, lastStatus(inc))
}

// TestWorkflow_UpdateHistoryPreserved verifies that each status transition
// adds an entry to the updates array.
func TestWorkflow_UpdateHistoryPreserved(t *testing.T) {
	r := initRBACTests(t)
	truncateIncidents(t)

	resp := createEventOK(t, r, maintenanceData(), creatorTokenA)
	eventID := resp.Result[0].IncidentID

	inc := getEventOK(t, r, eventID, creatorTokenA)
	initialUpdates := len(inc.Updates)
	require.GreaterOrEqual(t, initialUpdates, 1, "event should have at least one initial update")

	transitionTo(t, r, eventID, event.MaintenanceReviewed, adminToken)
	transitionTo(t, r, eventID, event.MaintenancePlanned, adminToken)

	inc = getEventOK(t, r, eventID, adminToken)
	assert.Len(t, inc.Updates, initialUpdates+2,
		"each transition should add one update entry")
}
