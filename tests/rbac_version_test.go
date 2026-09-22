package tests

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	v2 "github.com/stackmon/otc-status-dashboard/internal/api/v2"
	"github.com/stackmon/otc-status-dashboard/internal/event"
)

// TestVersion_NilVersionOnMaintenancePatch verifies that a PATCH on a
// maintenance event without a version field returns 400.
func TestVersion_NilVersionOnMaintenancePatch(t *testing.T) {
	r := initRBACTests(t)

	roles := []struct {
		name  string
		token string
	}{
		{"creator", creatorTokenA},
		{"operator", operatorToken},
		{"admin", adminToken},
	}

	for _, role := range roles {
		t.Run(role.name+"_nil_version_rejected", func(t *testing.T) {
			truncateIncidents(t)

			var eventID int
			if role.name == "creator" {
				resp := createEventOK(t, r, maintenanceData(), creatorTokenA)
				eventID = resp.Result[0].IncidentID
			} else {
				resp := createEventOK(t, r, maintenanceData(), role.token)
				eventID = resp.Result[0].IncidentID
			}

			// PATCH with nil version → 400
			assertPatchStatus(t, r, eventID, event.MaintenancePendingReview, nil, role.token, http.StatusBadRequest)
		})
	}
}

// TestVersion_WrongVersionOnMaintenancePatch verifies that a PATCH on a
// maintenance event with a stale version returns 409 (version conflict).
func TestVersion_WrongVersionOnMaintenancePatch(t *testing.T) {
	r := initRBACTests(t)

	t.Run("stale version returns 409", func(t *testing.T) {
		truncateIncidents(t)
		resp := createEventOK(t, r, maintenanceData(), creatorTokenA)
		eventID := resp.Result[0].IncidentID

		inc := getEventOK(t, r, eventID, creatorTokenA)
		version := eventVersion(inc)

		// First PATCH succeeds — version increments.
		assertPatchStatus(t, r, eventID, event.MaintenancePendingReview, intPtr(version), creatorTokenA, http.StatusOK)

		// Second PATCH with same (now stale) version → 409.
		assertPatchStatus(t, r, eventID, event.MaintenancePendingReview, intPtr(version), creatorTokenA, http.StatusConflict)
	})

	t.Run("completely wrong version returns 409", func(t *testing.T) {
		truncateIncidents(t)
		resp := createEventOK(t, r, maintenanceData(), adminToken)
		eventID := resp.Result[0].IncidentID

		// Use a version number far from the real one.
		assertPatchStatus(t, r, eventID, event.MaintenancePlanned, intPtr(999), adminToken, http.StatusConflict)
	})
}

// TestVersion_NilVersionOnIncidentPatch verifies that a PATCH on a
// non-maintenance (incident) event does NOT require a version field.
func TestVersion_NilVersionOnIncidentPatch(t *testing.T) {
	r := initRBACTests(t)

	t.Run("nil version on incident patch is accepted", func(t *testing.T) {
		truncateIncidents(t)
		resp := createEventOK(t, r, incidentData(), creatorTokenA)
		eventID := resp.Result[0].IncidentID

		// PATCH incident with nil version — should succeed.
		w := patchEvent(t, r, eventID, &v2.PatchIncidentData{
			Message:    "incident update",
			Status:     event.IncidentAnalysing,
			UpdateDate: incNow(),
			Version:    nil,
		}, creatorTokenA)
		assert.Equal(t, http.StatusOK, w.Code, "incident PATCH with nil version should succeed: %s", w.Body.String())
	})
}

// TestVersion_WrongVersionOnIncidentPatch verifies that a PATCH on an
// incident-type event with a wrong version still works (version is optional
// for non-maintenance events).
func TestVersion_WrongVersionOnIncidentPatch(t *testing.T) {
	r := initRBACTests(t)

	t.Run("explicit version on incident patch is accepted", func(t *testing.T) {
		truncateIncidents(t)
		resp := createEventOK(t, r, incidentData(), creatorTokenA)
		eventID := resp.Result[0].IncidentID

		w := patchEvent(t, r, eventID, &v2.PatchIncidentData{
			Message:    "incident update",
			Status:     event.IncidentAnalysing,
			UpdateDate: incNow(),
			Version:    intPtr(1),
		}, creatorTokenA)
		assert.Equal(t, http.StatusOK, w.Code, "incident PATCH with explicit version: %s", w.Body.String())
	})
}

// TestVersion_ConcurrentMaintenancePatch simulates two users trying to patch
// the same maintenance event simultaneously — only the first should succeed.
func TestVersion_ConcurrentMaintenancePatch(t *testing.T) {
	r := initRBACTests(t)
	truncateIncidents(t)

	resp := createEventOK(t, r, maintenanceData(), creatorTokenA)
	eventID := resp.Result[0].IncidentID

	// Both users read the same version.
	inc := getEventOK(t, r, eventID, adminToken)
	version := eventVersion(inc)

	// First user (admin) patches — succeeds.
	w1 := patchEvent(t, r, eventID, patchData(event.MaintenanceReviewed, intPtr(version)), adminToken)
	assert.Equal(t, http.StatusOK, w1.Code)

	// Second user (operator) patches with same version — 409 conflict.
	w2 := patchEvent(t, r, eventID, patchData(event.MaintenanceReviewed, intPtr(version)), operatorToken)
	assert.Equal(t, http.StatusConflict, w2.Code)
}
