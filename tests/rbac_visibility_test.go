package tests

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackmon/otc-status-dashboard/internal/event"
)

// TestVisibility_PendingReviewHiddenFromUnauth verifies that maintenance
// events in pending_review status are not visible to unauthenticated users.
func TestVisibility_PendingReviewHiddenFromUnauth(t *testing.T) {
	r := initRBACTests(t)
	truncateIncidents(t)

	// Create a pending_review event (creator).
	resp := createEventOK(t, r, maintenanceData(), creatorTokenA)
	eventID := resp.Result[0].IncidentID

	t.Run("GET list hides pending_review", func(t *testing.T) {
		events := listEvents(t, r, "")
		for _, ev := range events {
			assert.NotEqual(t, event.MaintenancePendingReview, ev.Status,
				"pending_review events must not be visible to unauthenticated users")
		}
	})

	t.Run("GET by ID returns 404 for pending_review", func(t *testing.T) {
		w, _ := getEvent(t, r, eventID, "")
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}

// TestVisibility_PendingReviewVisibleToAuth verifies that authenticated
// users can see pending_review events.
func TestVisibility_PendingReviewVisibleToAuth(t *testing.T) {
	r := initRBACTests(t)
	truncateIncidents(t)

	resp := createEventOK(t, r, maintenanceData(), creatorTokenA)
	eventID := resp.Result[0].IncidentID

	roles := []struct {
		name  string
		token string
	}{
		{"creator", creatorTokenA},
		{"operator", operatorToken},
		{"admin", adminToken},
	}

	for _, role := range roles {
		t.Run(role.name+"_can_see_pending_review_by_id", func(t *testing.T) {
			w, inc := getEvent(t, r, eventID, role.token)
			assert.Equal(t, http.StatusOK, w.Code)
			require.NotNil(t, inc)
		})
	}

	t.Run("auth user sees pending_review in list", func(t *testing.T) {
		events := listEvents(t, r, adminToken)
		found := false
		for _, ev := range events {
			if ev.ID == eventID {
				found = true
				break
			}
		}
		assert.True(t, found, "authenticated user should see pending_review event in list")
	})
}

// TestVisibility_ContactEmailAndCreator verifies that contact_email and
// creator fields are visible to authenticated users but hidden from
// unauthenticated users.
func TestVisibility_ContactEmailAndCreator(t *testing.T) {
	r := initRBACTests(t)
	truncateIncidents(t)

	// Create a maintenance event (visible to unauth since it starts as planned).
	resp := createEventOK(t, r, maintenanceData(), adminToken)
	eventID := resp.Result[0].IncidentID

	t.Run("auth user sees contact_email and creator", func(t *testing.T) {
		inc := getEventOK(t, r, eventID, adminToken)
		assert.NotEmpty(t, inc.ContactEmail, "contact_email should be visible to authenticated user")
		assert.NotEmpty(t, inc.CreatedBy, "creator should be visible to authenticated user")
	})

	t.Run("unauth user does not see contact_email and creator", func(t *testing.T) {
		_, inc := getEvent(t, r, eventID, "")
		require.NotNil(t, inc)
		assert.Empty(t, inc.ContactEmail, "contact_email must be hidden from unauthenticated users")
		assert.Empty(t, inc.CreatedBy, "creator must be hidden from unauthenticated users")
	})
}

// TestVisibility_AuthVsUnauthEventList verifies that authenticated users
// see more events than unauthenticated users (pending_review included).
func TestVisibility_AuthVsUnauthEventList(t *testing.T) {
	r := initRBACTests(t)
	truncateIncidents(t)

	// Create one planned (visible) and one pending_review (hidden from unauth).
	createEventOK(t, r, maintenanceData(), adminToken)    // planned
	createEventOK(t, r, maintenanceData(), creatorTokenA) // pending_review

	authEvents := listEvents(t, r, adminToken)
	unauthEvents := listEvents(t, r, "")

	assert.GreaterOrEqual(t, len(authEvents), 2, "auth user should see both events")
	assert.Less(t, len(unauthEvents), len(authEvents),
		"unauth user should see fewer events (pending_review hidden)")

	// Verify no pending_review in unauth list.
	for _, ev := range unauthEvents {
		assert.NotEqual(t, event.MaintenancePendingReview, ev.Status)
	}
}

// TestVisibility_CancelledMaintenanceWithoutPublicStatus verifies that
// a maintenance cancelled before reaching "planned" is hidden from unauthenticated users.
func TestVisibility_CancelledMaintenanceWithoutPublicStatus(t *testing.T) {
	r := initRBACTests(t)
	truncateIncidents(t)

	// Creator creates maintenance → pending_review.
	resp := createEventOK(t, r, maintenanceData(), creatorTokenA)
	eventID := resp.Result[0].IncidentID

	// Admin cancels it directly from pending_review.
	stored := getEventOK(t, r, eventID, adminToken)
	patchResp := patchEvent(t, r, eventID, patchData(event.MaintenanceCancelled, intPtr(eventVersion(stored))), adminToken)
	require.Equal(t, http.StatusOK, patchResp.Code)

	t.Run("unauth list hides cancelled-without-public-status", func(t *testing.T) {
		events := listEvents(t, r, "")
		for _, ev := range events {
			assert.NotEqual(t, eventID, ev.ID,
				"cancelled maintenance without public status must be hidden")
		}
	})

	t.Run("unauth GET by ID returns 404", func(t *testing.T) {
		rec, _ := getEvent(t, r, eventID, "")
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("auth user still sees cancelled event", func(t *testing.T) {
		got := getEventOK(t, r, eventID, adminToken)
		assert.Equal(t, event.MaintenanceCancelled, got.Status)
	})
}

// TestVisibility_CancelledMaintenanceAfterPlanned verifies that a maintenance
// cancelled after reaching "planned" remains visible to unauthenticated users.
func TestVisibility_CancelledMaintenanceAfterPlanned(t *testing.T) {
	r := initRBACTests(t)
	truncateIncidents(t)

	// Admin creates maintenance → planned.
	resp := createEventOK(t, r, maintenanceData(), adminToken)
	eventID := resp.Result[0].IncidentID

	// Transition to cancelled.
	stored := getEventOK(t, r, eventID, adminToken)
	patchResp := patchEvent(t, r, eventID, patchData(event.MaintenanceCancelled, intPtr(eventVersion(stored))), adminToken)
	require.Equal(t, http.StatusOK, patchResp.Code)

	t.Run("unauth can see cancelled-after-planned event", func(t *testing.T) {
		rec, got := getEvent(t, r, eventID, "")
		assert.Equal(t, http.StatusOK, rec.Code)
		require.NotNil(t, got)
		assert.Equal(t, event.MaintenanceCancelled, got.Status)
	})
}

// TestVisibility_InternalStatusesFilteredFromUpdates verifies that
// pending_review and reviewed statuses are filtered from the Updates array
// for unauthenticated users.
func TestVisibility_InternalStatusesFilteredFromUpdates(t *testing.T) {
	r := initRBACTests(t)
	truncateIncidents(t)

	// Creator creates → pending_review, then operator approves → reviewed → planned.
	resp := createEventOK(t, r, maintenanceData(), creatorTokenA)
	eventID := resp.Result[0].IncidentID

	inc := transitionTo(t, r, eventID, event.MaintenanceReviewed, operatorToken)
	_ = transitionTo(t, r, eventID, event.MaintenancePlanned, operatorToken)
	_ = inc

	t.Run("auth user sees all statuses in updates", func(t *testing.T) {
		authInc := getEventOK(t, r, eventID, adminToken)
		hasInternal := false
		for _, u := range authInc.Updates {
			if u.Status == event.MaintenancePendingReview || u.Status == event.MaintenanceReviewed {
				hasInternal = true
				break
			}
		}
		assert.True(t, hasInternal, "authenticated user should see internal statuses")
	})

	t.Run("unauth user does not see internal statuses in updates", func(t *testing.T) {
		_, unauthInc := getEvent(t, r, eventID, "")
		require.NotNil(t, unauthInc)
		for _, u := range unauthInc.Updates {
			assert.NotEqual(t, event.MaintenancePendingReview, u.Status,
				"pending_review must not appear in updates for unauthenticated users")
			assert.NotEqual(t, event.MaintenanceReviewed, u.Status,
				"reviewed must not appear in updates for unauthenticated users")
		}
	})
}

// TestVisibility_ReviewedHiddenFromUnauth verifies that maintenance events
// in reviewed status are not visible to unauthenticated users.
func TestVisibility_ReviewedHiddenFromUnauth(t *testing.T) {
	r := initRBACTests(t)
	truncateIncidents(t)

	// Creator creates → pending_review, operator moves to reviewed.
	resp := createEventOK(t, r, maintenanceData(), creatorTokenA)
	eventID := resp.Result[0].IncidentID
	_ = transitionTo(t, r, eventID, event.MaintenanceReviewed, operatorToken)

	t.Run("unauth list hides reviewed", func(t *testing.T) {
		events := listEvents(t, r, "")
		for _, ev := range events {
			assert.NotEqual(t, eventID, ev.ID,
				"reviewed maintenance must not be visible to unauthenticated users")
		}
	})

	t.Run("unauth GET by ID returns 404 for reviewed", func(t *testing.T) {
		w, _ := getEvent(t, r, eventID, "")
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("auth user sees reviewed event", func(t *testing.T) {
		inc := getEventOK(t, r, eventID, adminToken)
		assert.Equal(t, event.MaintenanceReviewed, inc.Status)
	})
}

// TestVisibility_CancelledAfterReviewedHidden verifies that a maintenance
// cancelled from reviewed status (never reached planned) is hidden from unauth.
func TestVisibility_CancelledAfterReviewedHidden(t *testing.T) {
	r := initRBACTests(t)
	truncateIncidents(t)

	// Creator creates → pending_review, operator reviews, admin cancels.
	resp := createEventOK(t, r, maintenanceData(), creatorTokenA)
	eventID := resp.Result[0].IncidentID
	_ = transitionTo(t, r, eventID, event.MaintenanceReviewed, operatorToken)

	stored := getEventOK(t, r, eventID, adminToken)
	patchResp := patchEvent(t, r, eventID, patchData(event.MaintenanceCancelled, intPtr(eventVersion(stored))), adminToken)
	require.Equal(t, http.StatusOK, patchResp.Code)

	t.Run("unauth list hides cancelled-after-reviewed", func(t *testing.T) {
		events := listEvents(t, r, "")
		for _, ev := range events {
			assert.NotEqual(t, eventID, ev.ID,
				"cancelled maintenance that never reached planned must be hidden")
		}
	})

	t.Run("unauth GET by ID returns 404", func(t *testing.T) {
		rec, _ := getEvent(t, r, eventID, "")
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("auth user sees cancelled event", func(t *testing.T) {
		got := getEventOK(t, r, eventID, adminToken)
		assert.Equal(t, event.MaintenanceCancelled, got.Status)
	})
}

// TestVisibility_IncidentNotFiltered verifies that incident events are never
// affected by the maintenance visibility rules.
func TestVisibility_IncidentNotFiltered(t *testing.T) {
	r := initRBACTests(t)
	truncateIncidents(t)

	resp := createEventOK(t, r, incidentData(), adminToken)
	eventID := resp.Result[0].IncidentID

	t.Run("unauth can see incident in list", func(t *testing.T) {
		events := listEvents(t, r, "")
		found := false
		for _, ev := range events {
			if ev.ID == eventID {
				found = true
				break
			}
		}
		assert.True(t, found, "incident must always be visible to unauthenticated users")
	})

	t.Run("unauth can GET incident by ID", func(t *testing.T) {
		w, inc := getEvent(t, r, eventID, "")
		assert.Equal(t, http.StatusOK, w.Code)
		require.NotNil(t, inc)
		assert.Equal(t, event.TypeIncident, inc.Type)
	})

	t.Run("incident updates are not filtered", func(t *testing.T) {
		_, inc := getEvent(t, r, eventID, "")
		require.NotNil(t, inc)
		assert.NotEmpty(t, inc.Updates, "incident should have updates visible to unauth")
	})
}

// TestVisibility_InfoEventNotFiltered verifies that info events are never
// affected by the maintenance visibility rules.
func TestVisibility_InfoEventNotFiltered(t *testing.T) {
	r := initRBACTests(t)
	truncateIncidents(t)

	infoEvt := infoEventData()
	resp := createEventOK(t, r, infoEvt, adminToken)
	eventID := resp.Result[0].IncidentID

	t.Run("unauth can see info event in list", func(t *testing.T) {
		events := listEvents(t, r, "")
		found := false
		for _, ev := range events {
			if ev.ID == eventID {
				found = true
				break
			}
		}
		assert.True(t, found, "info event must always be visible to unauthenticated users")
	})

	t.Run("unauth can GET info event by ID", func(t *testing.T) {
		w, inc := getEvent(t, r, eventID, "")
		assert.Equal(t, http.StatusOK, w.Code)
		require.NotNil(t, inc)
		assert.Equal(t, event.TypeInformation, inc.Type)
	})

	t.Run("info cancelled event remains visible to unauth", func(t *testing.T) {
		inc := getEventOK(t, r, eventID, adminToken)
		w := patchEvent(t, r, eventID, patchData(event.InfoCancelled, intPtr(eventVersion(inc))), adminToken)
		require.Equal(t, http.StatusOK, w.Code)

		w2, cancelled := getEvent(t, r, eventID, "")
		assert.Equal(t, http.StatusOK, w2.Code)
		require.NotNil(t, cancelled)
		assert.Equal(t, event.InfoCancelled, cancelled.Status)
	})
}
