package v2

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackmon/otc-status-dashboard/internal/db"
)

func maintenanceIncident(createdBy, contactEmail *string) *db.Incident {
	text := "Title"
	start := time.Now().UTC()
	impact := 1
	return &db.Incident{
		Text:         &text,
		StartDate:    &start,
		Impact:       &impact,
		Type:         "maintenance",
		CreatedBy:    createdBy,
		ContactEmail: contactEmail,
	}
}

func TestCreatorFieldExposedToAuthenticated(t *testing.T) {
	creator := "user@example.com"
	contactEmail := "contact@example.com"

	body, err := json.Marshal(toAPIEvent(maintenanceIncident(&creator, &contactEmail), true))
	require.NoError(t, err)

	assert.Contains(t, string(body), `"creator":"user@example.com"`)
}

func TestCreatorFieldHiddenFromUnauthenticated(t *testing.T) {
	creator := "user@example.com"

	body, err := json.Marshal(toAPIEvent(maintenanceIncident(&creator, nil), false))
	require.NoError(t, err)

	assert.NotContains(t, string(body), "creator")
	assert.NotContains(t, string(body), "user@example.com")
}

func TestContactEmailHiddenFromUnauthenticated(t *testing.T) {
	contactEmail := "contact@example.com"

	body, err := json.Marshal(toAPIEvent(maintenanceIncident(nil, &contactEmail), false))
	require.NoError(t, err)

	assert.NotContains(t, string(body), "contact_email")
	assert.NotContains(t, string(body), "contact@example.com")
}

func TestVersionFieldExposedToAuthenticated(t *testing.T) {
	testTime, err := time.Parse(time.RFC3339, "2025-08-01T11:45:26.371Z")
	require.NoError(t, err)

	impact0 := 0 // maintenance
	version := 5

	maintenanceEvent := &db.Incident{
		ID:          1,
		Text:        &[]string{"Maintenance event"}[0],
		Description: &[]string{"Test maintenance description"}[0],
		StartDate:   &testTime,
		Impact:      &impact0,
		Type:        "maintenance",
		System:      false,
		Version:     &version,
		Status:      "planned",
		Components:  []db.Component{{ID: 1, Name: "Component 1"}},
		Statuses: []db.IncidentStatus{
			{ID: 1, Status: "planned", Text: "Planned", Timestamp: testTime},
		},
	}

	t.Run("Version exposed for authenticated maintenance event", func(t *testing.T) {
		apiEvent := toAPIEvent(maintenanceEvent, true) // authenticated
		require.NotNil(t, apiEvent.Version, "Version should be exposed for authenticated users")
		assert.Equal(t, version, *apiEvent.Version, "Version value should match")
	})

	t.Run("Version hidden for unauthenticated maintenance event", func(t *testing.T) {
		apiEvent := toAPIEvent(maintenanceEvent, false) // not authenticated
		assert.Nil(t, apiEvent.Version, "Version should be hidden for unauthenticated users")
	})

	t.Run("Version NOT exposed for authenticated incident event", func(t *testing.T) {
		impact2 := 2 // incident
		incidentEvent := &db.Incident{
			ID:          2,
			Text:        &[]string{"Incident event"}[0],
			Description: &[]string{"Test incident description"}[0],
			StartDate:   &testTime,
			Impact:      &impact2,
			Type:        "incident",
			System:      false,
			Version:     &version,
			Status:      "analysing",
			Components:  []db.Component{{ID: 1, Name: "Component 1"}},
			Statuses: []db.IncidentStatus{
				{ID: 1, Status: "analysing", Text: "Analysing", Timestamp: testTime},
			},
		}

		apiEvent := toAPIEvent(incidentEvent, true) // authenticated
		assert.Nil(t, apiEvent.Version, "Version should NOT be exposed for non-maintenance events")
	})

	t.Run("Version NOT exposed for authenticated info event", func(t *testing.T) {
		infoEvent := &db.Incident{
			ID:          3,
			Text:        &[]string{"Info event"}[0],
			Description: &[]string{"Test info description"}[0],
			StartDate:   &testTime,
			Impact:      &impact0,
			Type:        "info",
			System:      false,
			Version:     &version,
			Status:      "active",
			Components:  []db.Component{{ID: 1, Name: "Component 1"}},
			Statuses: []db.IncidentStatus{
				{ID: 1, Status: "active", Text: "Active", Timestamp: testTime},
			},
		}

		apiEvent := toAPIEvent(infoEvent, true) // authenticated
		assert.Nil(t, apiEvent.Version, "Version should NOT be exposed for non-maintenance events")
	})
}
