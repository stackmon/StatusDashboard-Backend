package tests

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackmon/otc-status-dashboard/internal/event"
)

// TestCreation_RoleInitialStatus verifies that each role gets the correct
// initial status when creating a maintenance event.
func TestCreation_RoleInitialStatus(t *testing.T) {
	r := initRBACTests(t)

	t.Run("creator creates maintenance with pending_review status", func(t *testing.T) {
		truncateIncidents(t)
		resp := createEventOK(t, r, maintenanceData(), creatorTokenA)
		inc := getEventOK(t, r, resp.Result[0].IncidentID, creatorTokenA)
		assert.Equal(t, event.MaintenancePendingReview, lastStatus(inc))
	})

	t.Run("operator creates maintenance with planned status", func(t *testing.T) {
		truncateIncidents(t)
		resp := createEventOK(t, r, maintenanceData(), operatorToken)
		inc := getEventOK(t, r, resp.Result[0].IncidentID, operatorToken)
		assert.Equal(t, event.MaintenancePlanned, lastStatus(inc))
	})

	t.Run("admin creates maintenance with planned status", func(t *testing.T) {
		truncateIncidents(t)
		resp := createEventOK(t, r, maintenanceData(), adminToken)
		inc := getEventOK(t, r, resp.Result[0].IncidentID, adminToken)
		assert.Equal(t, event.MaintenancePlanned, lastStatus(inc))
	})
}

// TestCreation_IncidentByRoles verifies that all authorized roles can
// create incident-type events.
func TestCreation_IncidentByRoles(t *testing.T) {
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
		t.Run(role.name+"_creates_incident", func(t *testing.T) {
			truncateIncidents(t)
			resp := createEventOK(t, r, incidentData(), role.token)
			require.NotEmpty(t, resp.Result)
		})
	}
}

// TestCreation_MaintenanceValidation verifies maintenance-specific
// validation rules during creation.
func TestCreation_MaintenanceValidation(t *testing.T) {
	r := initRBACTests(t)

	t.Run("missing contact_email rejected", func(t *testing.T) {
		truncateIncidents(t)
		data := maintenanceData()
		data.ContactEmail = ""
		w, _ := createEvent(t, r, data, creatorTokenA)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("invalid contact_email rejected", func(t *testing.T) {
		truncateIncidents(t)
		data := maintenanceData()
		data.ContactEmail = "not-an-email"
		w, _ := createEvent(t, r, data, creatorTokenA)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("empty description rejected", func(t *testing.T) {
		truncateIncidents(t)
		data := maintenanceData()
		data.Description = ""
		w, _ := createEvent(t, r, data, creatorTokenA)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("non-zero impact for maintenance rejected", func(t *testing.T) {
		truncateIncidents(t)
		data := maintenanceData()
		wrongImpact := 1
		data.Impact = &wrongImpact
		w, _ := createEvent(t, r, data, creatorTokenA)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}
