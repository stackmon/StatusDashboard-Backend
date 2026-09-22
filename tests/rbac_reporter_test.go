package tests

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v2 "github.com/stackmon/otc-status-dashboard/internal/api/v2"
	"github.com/stackmon/otc-status-dashboard/internal/event"
)

// systemIncidentData is the machine-reported incident shape a reporter role
// is allowed to create.
func systemIncidentData() v2.IncidentData {
	impact := 1
	system := true
	startDate := incNow()

	return v2.IncidentData{
		Title:      "Reporter system incident",
		Impact:     &impact,
		Components: []int{1},
		StartDate:  startDate,
		System:     &system,
		Type:       event.TypeIncident,
	}
}

// postJSON sends a raw JSON body to the router and returns the recorder.
func postJSON(t *testing.T, r *gin.Engine, path string, body []byte, token string) *httptest.ResponseRecorder {
	t.Helper()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	r.ServeHTTP(w, req)
	return w
}

// TestReporter_CanCreateSystemIncident covers the single write path a
// reporter role is allowed to use: a machine-reported incident.
func TestReporter_CanCreateSystemIncident(t *testing.T) {
	r := initRBACTests(t)
	truncateIncidents(t)

	resp := createEventOK(t, r, systemIncidentData(), reporterToken)
	require.NotEmpty(t, resp.Result)

	inc := getEventOK(t, r, resp.Result[0].IncidentID, reporterToken)
	assert.True(t, inc.System != nil && *inc.System, "the reported event stays a system incident")
}

func TestReporter_CannotCreateHumanEvents(t *testing.T) {
	r := initRBACTests(t)

	tests := []struct {
		name string
		data v2.IncidentData
	}{
		{"non-system incident", incidentData()},
		{"info event", infoEventData()},
		{"maintenance", maintenanceData()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			truncateIncidents(t)
			w, _ := createEvent(t, r, tt.data, reporterToken)
			assert.Equal(t, http.StatusForbidden, w.Code, "body: %s", w.Body.String())
		})
	}
}

func TestReporter_CannotMutateEvents(t *testing.T) {
	r := initRBACTests(t)
	truncateIncidents(t)

	resp := createEventOK(t, r, systemIncidentData(), creatorTokenA)
	eventID := resp.Result[0].IncidentID

	t.Run("PATCH is forbidden", func(t *testing.T) {
		inc := getEventOK(t, r, eventID, creatorTokenA)
		w := patchEvent(t, r, eventID, patchData(event.IncidentAnalysing, intPtr(eventVersion(inc))), reporterToken)
		assert.Equal(t, http.StatusForbidden, w.Code, "body: %s", w.Body.String())
	})

	t.Run("extract is forbidden", func(t *testing.T) {
		w := extractComponents(t, r, eventID, []int{2}, reporterToken)
		assert.Equal(t, http.StatusForbidden, w.Code, "body: %s", w.Body.String())
	})
}

// TestReporter_CannotWriteComponents covers the legacy component routes, which
// carry no RBAC middleware and rely on the reporter scope check alone.
func TestReporter_CannotWriteComponents(t *testing.T) {
	r, _ := initTests(t)

	t.Run("POST /v2/components is forbidden", func(t *testing.T) {
		w := postJSON(t, r, "/v2/components", []byte(`{}`), reporterToken)
		assert.Equal(t, http.StatusForbidden, w.Code, "body: %s", w.Body.String())
	})

	t.Run("POST /v1/component_status is forbidden", func(t *testing.T) {
		w := postJSON(t, r, "/v1/component_status", []byte(`{}`), reporterToken)
		assert.Equal(t, http.StatusForbidden, w.Code, "body: %s", w.Body.String())
	})
}

// TestReporter_PublicView checks that reporters never receive the internal
// fields reserved for human roles.
func TestReporter_PublicView(t *testing.T) {
	r := initRBACTests(t)
	truncateIncidents(t)

	resp := createEventOK(t, r, maintenanceData(), adminToken)
	eventID := resp.Result[0].IncidentID

	t.Run("GET by ID hides contact_email and creator", func(t *testing.T) {
		inc := getEventOK(t, r, eventID, reporterToken)
		assert.Empty(t, inc.ContactEmail, "contact_email is internal")
		assert.Empty(t, inc.CreatedBy, "creator is internal")
	})

	t.Run("GET list hides contact_email and creator", func(t *testing.T) {
		events := listEvents(t, r, reporterToken)
		require.NotEmpty(t, events)
		for _, ev := range events {
			assert.Empty(t, ev.ContactEmail)
			assert.Empty(t, ev.CreatedBy)
		}
	})
}
