package tests

import (
	"net/http"
	"testing"

	"github.com/stackmon/otc-status-dashboard/internal/event"
)

// ---------------------------------------------------------------------------
// Extract RBAC restrictions (acceptance tests)
// ---------------------------------------------------------------------------

// TestExtract_AdminCanExtractIncident verifies that admin can extract
// components from an incident-type event.
func TestExtract_AdminCanExtractIncident(t *testing.T) {
	r := initRBACTests(t)
	truncateIncidents(t)

	resp := createEventOK(t, r, incidentDataMultiComponent(), adminToken)
	eventID := resp.Result[0].IncidentID

	w := extractComponents(t, r, eventID, []int{2}, adminToken)
	assertHTTPStatus(t, w.Code, http.StatusOK)
}

// TestExtract_OperatorCanExtractIncident verifies that operator can extract
// components from an incident-type event.
func TestExtract_OperatorCanExtractIncident(t *testing.T) {
	r := initRBACTests(t)
	truncateIncidents(t)

	resp := createEventOK(t, r, incidentDataMultiComponent(), operatorToken)
	eventID := resp.Result[0].IncidentID

	w := extractComponents(t, r, eventID, []int{2}, operatorToken)
	assertHTTPStatus(t, w.Code, http.StatusOK)
}

// TestExtract_CreatorCannotExtractIncident verifies that creator role
// is denied extract even on an incident-type event.
func TestExtract_CreatorCannotExtractIncident(t *testing.T) {
	r := initRBACTests(t)
	truncateIncidents(t)

	// Create as admin so it exists, then try extract as creator.
	resp := createEventOK(t, r, incidentDataMultiComponent(), adminToken)
	eventID := resp.Result[0].IncidentID

	w := extractComponents(t, r, eventID, []int{2}, creatorTokenA)
	assertHTTPStatus(t, w.Code, http.StatusForbidden)
}

// TestExtract_NoRoleCannotExtract verifies that a user with no recognized
// RBAC group is denied extract.
func TestExtract_NoRoleCannotExtract(t *testing.T) {
	r := initRBACTests(t)
	truncateIncidents(t)

	resp := createEventOK(t, r, incidentDataMultiComponent(), adminToken)
	eventID := resp.Result[0].IncidentID

	w := extractComponents(t, r, eventID, []int{2}, noRoleToken)
	assertHTTPStatus(t, w.Code, http.StatusForbidden)
}

// TestExtract_UnauthenticatedCannotExtract verifies that requests without
// a token are rejected.
func TestExtract_UnauthenticatedCannotExtract(t *testing.T) {
	r := initRBACTests(t)
	truncateIncidents(t)

	resp := createEventOK(t, r, incidentDataMultiComponent(), adminToken)
	eventID := resp.Result[0].IncidentID

	w := extractComponents(t, r, eventID, []int{2}, "")
	assertHTTPStatus(t, w.Code, http.StatusUnauthorized)
}

// TestExtract_ForbiddenForMaintenance verifies that extract is blocked
// for maintenance-type events even for admin.
func TestExtract_ForbiddenForMaintenance(t *testing.T) {
	r := initRBACTests(t)
	truncateIncidents(t)

	// Create maintenance as admin (starts at planned).
	resp := createEventOK(t, r, maintenanceData(), adminToken)
	eventID := resp.Result[0].IncidentID

	w := extractComponents(t, r, eventID, []int{2}, adminToken)
	assertHTTPStatus(t, w.Code, http.StatusForbidden)
}

// TestExtract_ForbiddenForInfo verifies that extract is blocked
// for info-type events even for admin.
func TestExtract_ForbiddenForInfo(t *testing.T) {
	r := initRBACTests(t)
	truncateIncidents(t)

	resp := createEventOK(t, r, infoEventData(), adminToken)
	eventID := resp.Result[0].IncidentID

	w := extractComponents(t, r, eventID, []int{1}, adminToken)
	assertHTTPStatus(t, w.Code, http.StatusForbidden)
}

// TestExtract_OperatorCannotExtractMaintenance verifies that even operator
// cannot extract from maintenance.
func TestExtract_OperatorCannotExtractMaintenance(t *testing.T) {
	r := initRBACTests(t)
	truncateIncidents(t)

	resp := createEventOK(t, r, maintenanceData(), operatorToken)
	eventID := resp.Result[0].IncidentID

	w := extractComponents(t, r, eventID, []int{2}, operatorToken)
	assertHTTPStatus(t, w.Code, http.StatusForbidden)
}

// TestExtract_CreatorCannotExtractMaintenance verifies that creator
// is denied extract for maintenance (role check fires first).
func TestExtract_CreatorCannotExtractMaintenance(t *testing.T) {
	r := initRBACTests(t)
	truncateIncidents(t)

	resp := createEventOK(t, r, maintenanceData(), creatorTokenA)
	eventID := resp.Result[0].IncidentID

	w := extractComponents(t, r, eventID, []int{2}, creatorTokenA)
	assertHTTPStatus(t, w.Code, http.StatusForbidden)
}

// TestExtract_OperatorCannotExtractInfo verifies that operator
// cannot extract from info events.
func TestExtract_OperatorCannotExtractInfo(t *testing.T) {
	r := initRBACTests(t)
	truncateIncidents(t)

	resp := createEventOK(t, r, infoEventData(), operatorToken)
	eventID := resp.Result[0].IncidentID

	w := extractComponents(t, r, eventID, []int{1}, operatorToken)
	assertHTTPStatus(t, w.Code, http.StatusForbidden)
}

// TestExtract_IncidentTypeCombinations is a matrix test that verifies
// extract restrictions across all role × event-type combinations.
func TestExtract_IncidentTypeCombinations(t *testing.T) {
	r := initRBACTests(t)

	tests := []struct {
		name       string
		eventData  func() (int, *testing.T)
		eventType  string
		token      string
		wantStatus int
	}{
		{
			name:       "admin + incident → 200",
			eventType:  event.TypeIncident,
			token:      adminToken,
			wantStatus: http.StatusOK,
		},
		{
			name:       "operator + incident → 200",
			eventType:  event.TypeIncident,
			token:      operatorToken,
			wantStatus: http.StatusOK,
		},
		{
			name:       "creator + incident → 403",
			eventType:  event.TypeIncident,
			token:      creatorTokenA,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "admin + maintenance → 403",
			eventType:  event.TypeMaintenance,
			token:      adminToken,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "operator + maintenance → 403",
			eventType:  event.TypeMaintenance,
			token:      operatorToken,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "admin + info → 403",
			eventType:  event.TypeInformation,
			token:      adminToken,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "operator + info → 403",
			eventType:  event.TypeInformation,
			token:      operatorToken,
			wantStatus: http.StatusForbidden,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			truncateIncidents(t)

			var eventID int
			var extractComp []int

			switch tc.eventType {
			case event.TypeIncident:
				resp := createEventOK(t, r, incidentDataMultiComponent(), adminToken)
				eventID = resp.Result[0].IncidentID
				extractComp = []int{2}
			case event.TypeMaintenance:
				resp := createEventOK(t, r, maintenanceData(), adminToken)
				eventID = resp.Result[0].IncidentID
				extractComp = []int{2}
			case event.TypeInformation:
				resp := createEventOK(t, r, infoEventData(), adminToken)
				eventID = resp.Result[0].IncidentID
				extractComp = []int{1}
			}

			w := extractComponents(t, r, eventID, extractComp, tc.token)
			assertHTTPStatus(t, w.Code, tc.wantStatus)
		})
	}
}
