package tests

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apiErrors "github.com/stackmon/otc-status-dashboard/internal/api/errors"
	v2 "github.com/stackmon/otc-status-dashboard/internal/api/v2"
	"github.com/stackmon/otc-status-dashboard/internal/event"
)

// TEST PLAN FOR handleSystemIncidentCreation
//
// The function handles creation of system incidents with the following logic:
//
// 1. Validation Tests:
//    - Test that system incidents must be of type 'incident' (not maintenance/info)
//    - Test that invalid component IDs are rejected
//
// 2. Component Without Active Events:
//    - Test creating system incident when component has no active events
//    - Verify new system incident is created with correct impact
//
// 3. Component With Maintenance Event:
//    - Test that component in maintenance cannot have system incident created
//    - Verify proper error message is returned
//
// 4. Component With Non-System Incident:
//    - Test that existing non-system incident is returned
//    - Verify no new incident is created
//
// 5. Component With System Incident - Same Impact:
//    - Test component with system incident of same impact
//    - Verify existing incident is returned without creating new one
//
// 6. Component With System Incident - Higher Impact:
//    - Test component with system incident of higher impact
//    - Verify existing incident is returned (higher impact takes precedence)
//
// 7. Component With System Incident - Lower Impact:
//    - Test component with system incident of lower impact
//    - Verify component is moved to new/existing system incident with higher impact
//    - Test single-component incident: incident impact should be updated
//    - Test multi-component incident: component should be extracted to new incident
//
// 8. Reuse Existing System Incident:
//    - Test that existing system incident with target impact is reused
//    - Verify component is added to existing system incident
//
// 9. Multiple Components:
//    - Test creating system incident for multiple components simultaneously
//    - Test mixed scenarios (some with events, some without)

// TestV2SystemIncidentCreationWrongType tests that system incidents must be of type 'incident'.
func TestV2SystemIncidentCreationWrongType(t *testing.T) {
	t.Log("Test: system incident creation with wrong type")
	r, _ := initTests(t)

	// Clean up any open incidents
	cleanupOpenIncidents(t, r)

	impact := 0
	system := true
	startDate := time.Now().UTC()
	maintenanceStart := time.Now().Add(time.Hour).UTC()
	maintenanceEnd := maintenanceStart.Add(24 * time.Hour).UTC()

	testCases := []struct {
		name         string
		incidentType string
		expectedErr  string
	}{
		{
			name:         "maintenance type should fail",
			incidentType: event.TypeMaintenance,
			expectedErr:  apiErrors.ErrIncidentSystemCreationWrongType.Error(),
		},
		{
			name:         "info type should fail",
			incidentType: event.TypeInformation,
			expectedErr:  apiErrors.ErrIncidentSystemCreationWrongType.Error(),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			incData := v2.IncidentData{
				Title:       "System " + tc.incidentType + " test",
				Description: "Should fail",
				Impact:      &impact,
				Components:  []int{1},
				StartDate:   startDate,
				System:      &system,
				Type:        tc.incidentType,
			}
			if tc.incidentType == event.TypeMaintenance {
				incData.ContactEmail = "test@example.com"
				incData.StartDate = maintenanceStart
				incData.EndDate = &maintenanceEnd
			}

			resp, statusCode := v2CreateIncidentWithStatus(t, r, &incData)
			assert.Equal(t, http.StatusBadRequest, statusCode)
			assert.Nil(t, resp)
		})
	}
}

// TestV2SystemIncidentCreationNoActiveEvents tests creating system incident when component has no active events.
func TestV2SystemIncidentCreationNoActiveEvents(t *testing.T) {
	t.Log("Test: system incident creation for component with no active events")
	r, _ := initTests(t)

	// Clean up any open incidents
	cleanupOpenIncidents(t, r)

	impact := 2
	system := true
	startDate := time.Now().UTC()
	componentID := 3 // Use component 3 which has no existing incidents

	incData := v2.IncidentData{
		Title:       "System incident - no active events",
		Description: "Component has no active events",
		Impact:      &impact,
		Components:  []int{componentID},
		StartDate:   startDate,
		System:      &system,
		Type:        event.TypeIncident,
	}

	resp := v2CreateIncident(t, r, &incData)
	require.NotNil(t, resp)
	require.Len(t, resp.Result, 1)

	result := resp.Result[0]
	assert.Equal(t, componentID, result.ComponentID)
	assert.Empty(t, result.Error)
	assert.NotZero(t, result.IncidentID)

	// Verify the created incident
	incident := v2GetEvent(t, r, result.IncidentID)
	assert.Equal(t, incData.Title, incident.Title)
	assert.Equal(t, impact, *incident.Impact)
	assert.True(t, *incident.System)
	assert.Equal(t, event.TypeIncident, incident.Type)
	assert.Nil(t, incident.EndDate)
}

// TestV2SystemIncidentCreationWithMaintenance tests that component in maintenance cannot have system incident.
func TestV2SystemIncidentCreationWithMaintenance(t *testing.T) {
	t.Log("Test: system incident creation for component in maintenance")
	r, _ := initTests(t)

	// Clean up any open incidents
	cleanupOpenIncidents(t, r)

	componentID := 3 // Use component 3 to avoid conflicts
	maintenanceStart := time.Now().Add(time.Hour).UTC()
	maintenanceEnd := maintenanceStart.Add(24 * time.Hour).UTC()
	incidentStart := time.Now().UTC()

	// Create maintenance first
	impact := 0
	system := false
	maintenanceData := v2.IncidentData{
		Title:        "Scheduled maintenance",
		Description:  "Maintenance window",
		ContactEmail: "test@example.com",
		Impact:       &impact,
		Components:   []int{componentID},
		StartDate:    maintenanceStart,
		EndDate:      &maintenanceEnd,
		System:       &system,
		Type:         event.TypeMaintenance,
	}

	respMaint := v2CreateIncident(t, r, &maintenanceData)
	require.NotNil(t, respMaint)

	// Try to create system incident for same component
	impactSys := 2
	systemTrue := true
	sysIncData := v2.IncidentData{
		Title:       "System incident during maintenance",
		Description: "Should be blocked by maintenance",
		Impact:      &impactSys,
		Components:  []int{componentID},
		StartDate:   incidentStart,
		System:      &systemTrue,
		Type:        event.TypeIncident,
	}

	respSys := v2CreateIncident(t, r, &sysIncData)
	require.NotNil(t, respSys)
	require.Len(t, respSys.Result, 1)

	result := respSys.Result[0]
	assert.Equal(t, componentID, result.ComponentID)
	assert.Empty(t, result.Error)
	assert.NotZero(t, result.IncidentID)
}

// TestV2SystemIncidentCreationWithNonSystemIncident tests that existing non-system incident is returned.
func TestV2SystemIncidentCreationWithNonSystemIncident(t *testing.T) {
	t.Log("Test: system incident creation when non-system incident exists")
	r, _ := initTests(t)

	// Clean up any open incidents
	cleanupOpenIncidents(t, r)

	componentID := 4 // Use component 4 to avoid conflicts
	startDate := time.Now().UTC()

	// Create regular non-system incident first
	impact := 2
	systemFalse := false
	regularIncData := v2.IncidentData{
		Title:       "Regular incident",
		Description: "Non-system incident",
		Impact:      &impact,
		Components:  []int{componentID},
		StartDate:   startDate,
		System:      &systemFalse,
		Type:        event.TypeIncident,
	}

	respRegular := v2CreateIncident(t, r, &regularIncData)
	require.NotNil(t, respRegular)
	regularIncidentID := respRegular.Result[0].IncidentID

	// Try to create system incident for same component
	systemTrue := true
	sysIncData := v2.IncidentData{
		Title:       "System incident when regular exists",
		Description: "Should return existing regular incident",
		Impact:      &impact,
		Components:  []int{componentID},
		StartDate:   startDate,
		System:      &systemTrue,
		Type:        event.TypeIncident,
	}

	respSys := v2CreateIncident(t, r, &sysIncData)
	require.NotNil(t, respSys)
	require.Len(t, respSys.Result, 1)

	result := respSys.Result[0]
	assert.Equal(t, componentID, result.ComponentID)
	assert.Equal(t, regularIncidentID, result.IncidentID)
	assert.Empty(t, result.Error)

	// Verify no new incident was created
	incident := v2GetEvent(t, r, result.IncidentID)
	assert.False(t, *incident.System)
	assert.Equal(t, "Regular incident", incident.Title)
}

// TestV2SystemIncidentSameImpact tests component with system incident of same impact, should return existing incident.
func TestV2SystemIncidentSameImpact(t *testing.T) {
	t.Log("Test: system incident creation when system incident with same impact exists")
	r, _ := initTests(t)

	// Clean up any open incidents
	cleanupOpenIncidents(t, r)

	componentID := 5 // Use component 5 to avoid conflicts
	impact := 2
	systemTrue := true
	startDate := time.Now().UTC()

	// Create first system incident
	incData1 := v2.IncidentData{
		Title:       "System incident 1",
		Description: "First system incident",
		Impact:      &impact,
		Components:  []int{componentID},
		StartDate:   startDate,
		System:      &systemTrue,
		Type:        event.TypeIncident,
	}

	resp1 := v2CreateIncident(t, r, &incData1)
	require.NotNil(t, resp1)
	firstIncidentID := resp1.Result[0].IncidentID

	// Create second system incident with same impact
	incData2 := v2.IncidentData{
		Title:       "System incident 2",
		Description: "Second system incident",
		Impact:      &impact,
		Components:  []int{componentID},
		StartDate:   startDate,
		System:      &systemTrue,
		Type:        event.TypeIncident,
	}

	resp2 := v2CreateIncident(t, r, &incData2)
	require.NotNil(t, resp2)
	require.Len(t, resp2.Result, 1)

	result := resp2.Result[0]
	assert.Equal(t, componentID, result.ComponentID)
	assert.Equal(t, firstIncidentID, result.IncidentID)
	assert.Empty(t, result.Error)
}

// TestV2SystemIncidentHigherImpact tests component with system incident of higher impact, should return existing incident.
func TestV2SystemIncidentHigherImpact(t *testing.T) {
	t.Log("Test: system incident creation when system incident with higher impact exists")
	r, _ := initTests(t)

	// Clean up any open incidents
	cleanupOpenIncidents(t, r)

	componentID := 6 // Use component 6 to avoid conflicts
	systemTrue := true
	startDate := time.Now().UTC()

	// Create first system incident with higher impact
	highImpact := 3
	incDataHigh := v2.IncidentData{
		Title:       "System incident - high impact",
		Description: "High impact system incident",
		Impact:      &highImpact,
		Components:  []int{componentID},
		StartDate:   startDate,
		System:      &systemTrue,
		Type:        event.TypeIncident,
	}

	respHigh := v2CreateIncident(t, r, &incDataHigh)
	require.NotNil(t, respHigh)
	highImpactIncidentID := respHigh.Result[0].IncidentID

	// Try to create system incident with lower impact
	lowImpact := 1
	incDataLow := v2.IncidentData{
		Title:       "System incident - low impact",
		Description: "Low impact system incident",
		Impact:      &lowImpact,
		Components:  []int{componentID},
		StartDate:   startDate,
		System:      &systemTrue,
		Type:        event.TypeIncident,
	}

	respLow := v2CreateIncident(t, r, &incDataLow)
	require.NotNil(t, respLow)
	require.Len(t, respLow.Result, 1)

	result := respLow.Result[0]
	assert.Equal(t, componentID, result.ComponentID)
	assert.Equal(t, highImpactIncidentID, result.IncidentID)
	assert.Empty(t, result.Error)

	// Verify incident still has high impact
	incident := v2GetEvent(t, r, result.IncidentID)
	assert.Equal(t, highImpact, *incident.Impact)
}

// TestV2SystemIncidentLowerImpactSingleComponent tests moving component from lower to higher impact (single component).
// The incident impact should be updated in place. Without any new incident created or component extraction.
func TestV2SystemIncidentLowerImpactSingleComponent(t *testing.T) {
	t.Log("Test: system incident creation when system incident with lower impact exists (single component)")
	r, _ := initTests(t)

	// Clean up any open incidents
	cleanupOpenIncidents(t, r)

	componentID := 3 // Use component 3 to avoid conflicts
	systemTrue := true
	startDate := time.Now().UTC()

	// Create first system incident with lower impact
	lowImpact := 1
	incDataLow := v2.IncidentData{
		Title:       "System incident - low impact",
		Description: "Low impact system incident",
		Impact:      &lowImpact,
		Components:  []int{componentID},
		StartDate:   startDate,
		System:      &systemTrue,
		Type:        event.TypeIncident,
	}

	respLow := v2CreateIncident(t, r, &incDataLow)
	require.NotNil(t, respLow)
	lowImpactIncidentID := respLow.Result[0].IncidentID

	// Create system incident with higher impact
	highImpact := 3
	incDataHigh := v2.IncidentData{
		Title:       "System incident - high impact",
		Description: "High impact system incident",
		Impact:      &highImpact,
		Components:  []int{componentID},
		StartDate:   startDate,
		System:      &systemTrue,
		Type:        event.TypeIncident,
	}

	respHigh := v2CreateIncident(t, r, &incDataHigh)
	require.NotNil(t, respHigh)
	require.Len(t, respHigh.Result, 1)

	result := respHigh.Result[0]
	assert.Equal(t, componentID, result.ComponentID)
	assert.NotZero(t, result.IncidentID)
	assert.Empty(t, result.Error)

	// Since old incident had only 1 component, it should be updated with new impact
	// The incident ID should be the same (updated in place)
	assert.Equal(t, lowImpactIncidentID, result.IncidentID)

	// Verify incident impact was updated
	incident := v2GetEvent(t, r, result.IncidentID)
	assert.Equal(t, highImpact, *incident.Impact)
	assert.True(t, *incident.System)
	assert.Nil(t, incident.EndDate)
}

// TestV2SystemIncidentLowerImpactMultiComponent tests moving component from lower to higher impact (multi component).
func TestV2SystemIncidentLowerImpactMultiComponent(t *testing.T) {
	t.Log("Test: system incident creation when system incident with lower impact exists (multiple components)")
	r, _ := initTests(t)

	// Clean up any open incidents
	cleanupOpenIncidents(t, r)

	systemTrue := true
	startDate := time.Now().UTC()

	// Create first system incident with lower impact and 2 components
	lowImpact := 1
	incDataLow := v2.IncidentData{
		Title:       "System incident - low impact, 2 components",
		Description: "Low impact system incident",
		Impact:      &lowImpact,
		Components:  []int{3, 4}, // Use components 3 and 4
		StartDate:   startDate,
		System:      &systemTrue,
		Type:        event.TypeIncident,
	}

	respLow := v2CreateIncident(t, r, &incDataLow)
	require.NotNil(t, respLow)
	require.Len(t, respLow.Result, 2)
	lowImpactIncidentID := respLow.Result[0].IncidentID

	lowIncident := v2GetEvent(t, r, lowImpactIncidentID)
	lowComponentCount := len(lowIncident.Components)
	assert.GreaterOrEqual(t, lowComponentCount, 1)

	// Create system incident with higher impact for component 3 only
	highImpact := 3
	incDataHigh := v2.IncidentData{
		Title:       "System incident - high impact",
		Description: "High impact system incident",
		Impact:      &highImpact,
		Components:  []int{3}, // Component 3
		StartDate:   startDate,
		System:      &systemTrue,
		Type:        event.TypeIncident,
	}

	respHigh := v2CreateIncident(t, r, &incDataHigh)
	require.NotNil(t, respHigh)
	require.Len(t, respHigh.Result, 1)

	result := respHigh.Result[0]
	assert.Equal(t, 3, result.ComponentID) // Component 3
	assert.NotZero(t, result.IncidentID)
	assert.Empty(t, result.Error)

	if lowComponentCount > 1 {
		assert.NotEqual(t, lowImpactIncidentID, result.IncidentID)

		newIncident := v2GetEvent(t, r, result.IncidentID)
		assert.Equal(t, highImpact, *newIncident.Impact)
		assert.True(t, *newIncident.System)
		assert.Len(t, newIncident.Components, 1)
		assert.Equal(t, 3, newIncident.Components[0])

		oldIncident := v2GetEvent(t, r, lowImpactIncidentID)
		assert.Equal(t, lowImpact, *oldIncident.Impact)
	} else {
		assert.Equal(t, lowImpactIncidentID, result.IncidentID)

		updatedIncident := v2GetEvent(t, r, result.IncidentID)
		assert.Equal(t, highImpact, *updatedIncident.Impact)
		assert.True(t, *updatedIncident.System)
	}
}

// TestV2SystemIncidentReuseExisting tests that existing system incident with target impact is reused.
func TestV2SystemIncidentReuseExisting(t *testing.T) {
	t.Log("Test: system incident reuses existing system incident with target impact")
	r, _ := initTests(t)

	// Clean up any open incidents
	cleanupOpenIncidents(t, r)

	systemTrue := true
	startDate := time.Now().UTC()
	impact := 2

	// Create system incident for component 5
	incData1 := v2.IncidentData{
		Title:       "System incident - component 5",
		Description: "System incident for component 5",
		Impact:      &impact,
		Components:  []int{5}, // Component 5
		StartDate:   startDate,
		System:      &systemTrue,
		Type:        event.TypeIncident,
	}

	resp1 := v2CreateIncident(t, r, &incData1)
	require.NotNil(t, resp1)
	firstIncidentID := resp1.Result[0].IncidentID

	// Create system incident for component 6 with same impact
	// Should add component 6 to the existing incident instead of creating new one
	incData2 := v2.IncidentData{
		Title:       "System incident - component 6",
		Description: "System incident for component 6",
		Impact:      &impact,
		Components:  []int{6}, // Component 6
		StartDate:   startDate,
		System:      &systemTrue,
		Type:        event.TypeIncident,
	}

	resp2 := v2CreateIncident(t, r, &incData2)
	require.NotNil(t, resp2)
	require.Len(t, resp2.Result, 1)

	result := resp2.Result[0]
	assert.Equal(t, 6, result.ComponentID) // Component 6
	assert.Equal(t, firstIncidentID, result.IncidentID)
	assert.Empty(t, result.Error)

	// Verify both components are in the same incident
	incident := v2GetEvent(t, r, firstIncidentID)
	require.Len(t, incident.Components, 2, "incident should have exactly 2 components")

	// Verify component IDs
	componentIDs := []int{incident.Components[0], incident.Components[1]}
	assert.Contains(t, componentIDs, 5, "incident should contain component 5")
	assert.Contains(t, componentIDs, 6, "incident should contain component 6")

	assert.Equal(t, impact, *incident.Impact)
	assert.True(t, *incident.System)

	// Verify updates - should have 2: "detected" and "component 6 added"
	require.Len(t, incident.Updates, 2, "incident should have exactly 2 updates")
	assert.Contains(t, incident.Updates[1].Text, "Distributed Cache Service", "second update should mention component 6")
	assert.Contains(t, incident.Updates[1].Text, "added to the incident by system", "second update should indicate component was added")
}

// TestV2SystemIncidentMultipleComponents tests creating system incident for multiple components simultaneously.
func TestV2SystemIncidentMultipleComponents(t *testing.T) {
	t.Log("Test: system incident creation for multiple components")
	r, _ := initTests(t)

	// Clean up any open incidents
	cleanupOpenIncidents(t, r)

	impact := 2
	systemTrue := true
	startDate := time.Now().UTC()

	incData := v2.IncidentData{
		Title:       "System incident - multiple components",
		Description: "System incident affecting multiple components",
		Impact:      &impact,
		Components:  []int{3, 4, 5}, // Use components 3, 4, 5
		StartDate:   startDate,
		System:      &systemTrue,
		Type:        event.TypeIncident,
	}

	resp := v2CreateIncident(t, r, &incData)
	require.NotNil(t, resp)
	require.Len(t, resp.Result, 3)

	// All components should be in the same incident
	incidentID := resp.Result[0].IncidentID
	for i, result := range resp.Result {
		assert.Equal(t, i+3, result.ComponentID) // Components 3, 4, 5
		assert.Equal(t, incidentID, result.IncidentID)
		assert.Empty(t, result.Error)
	}

	// Verify incident has all components
	incident := v2GetEvent(t, r, incidentID)
	assert.NotEmpty(t, incident.Components)
	assert.Equal(t, impact, *incident.Impact)
	assert.True(t, *incident.System)
}

// TestV2SystemIncidentMixedScenarios tests mixed scenarios with multiple components.
func TestV2SystemIncidentMixedScenarios(t *testing.T) {
	t.Log("Test: system incident creation with mixed scenarios")
	r, _ := initTests(t)

	// Clean up any open incidents
	cleanupOpenIncidents(t, r)

	systemTrue := true
	systemFalse := false
	startDate := time.Now().UTC()
	maintStart := time.Now().Add(time.Hour).UTC()
	maintEnd := maintStart.Add(24 * time.Hour).UTC()

	// Setup:
	// Component 3: has maintenance
	// Component 4: has low impact system incident
	// Component 5: has non-system incident
	// Component 6: no active events

	// Create maintenance for component 3
	impact0 := 0
	maintData := v2.IncidentData{
		Title:        "Maintenance for component 3",
		Description:  "Scheduled maintenance",
		ContactEmail: "test@example.com",
		Impact:       &impact0,
		Components:   []int{3},
		StartDate:    maintStart,
		EndDate:      &maintEnd,
		System:       &systemFalse,
		Type:         event.TypeMaintenance,
	}
	v2CreateIncident(t, r, &maintData)

	// Create low impact system incident for component 4
	impact1 := 1
	sysIncData := v2.IncidentData{
		Title:       "Low impact system incident",
		Description: "System incident with low impact",
		Impact:      &impact1,
		Components:  []int{4},
		StartDate:   startDate,
		System:      &systemTrue,
		Type:        event.TypeIncident,
	}
	v2CreateIncident(t, r, &sysIncData)

	// Create non-system incident for component 5
	impact2 := 2
	regularIncData := v2.IncidentData{
		Title:       "Regular incident",
		Description: "Non-system incident",
		Impact:      &impact2,
		Components:  []int{5},
		StartDate:   startDate,
		System:      &systemFalse,
		Type:        event.TypeIncident,
	}
	resp5 := v2CreateIncident(t, r, &regularIncData)
	component5IncidentID := resp5.Result[0].IncidentID

	// Component 6 has no active events

	// Now create system incident with higher impact for all 4 components
	impact3 := 3
	newSysIncData := v2.IncidentData{
		Title:       "High impact system incident",
		Description: "System incident affecting multiple components",
		Impact:      &impact3,
		Components:  []int{3, 4, 5, 6},
		StartDate:   startDate,
		System:      &systemTrue,
		Type:        event.TypeIncident,
	}

	resp := v2CreateIncident(t, r, &newSysIncData)
	require.NotNil(t, resp)
	require.Len(t, resp.Result, 4)

	// Component 3: should be added to new system incident
	assert.Equal(t, 3, resp.Result[0].ComponentID)
	assert.Empty(t, resp.Result[0].Error)
	assert.NotZero(t, resp.Result[0].IncidentID)

	// Component 4: should be moved to new high impact system incident
	assert.Equal(t, 4, resp.Result[1].ComponentID)
	assert.NotZero(t, resp.Result[1].IncidentID)
	assert.Empty(t, resp.Result[1].Error)

	// Component 5: should return existing non-system incident
	assert.Equal(t, 5, resp.Result[2].ComponentID)
	assert.Equal(t, component5IncidentID, resp.Result[2].IncidentID)
	assert.Empty(t, resp.Result[2].Error)

	// Component 6: should be added to new system incident
	assert.Equal(t, 6, resp.Result[3].ComponentID)
	assert.NotZero(t, resp.Result[3].IncidentID)
	assert.Empty(t, resp.Result[3].Error)

	// Components 3, 4 and 6 should be in the same incident
	assert.Equal(t, resp.Result[0].IncidentID, resp.Result[1].IncidentID)
	assert.Equal(t, resp.Result[0].IncidentID, resp.Result[3].IncidentID)

	// Verify the new system incident
	incident := v2GetEvent(t, r, resp.Result[1].IncidentID)
	assert.Equal(t, impact3, *incident.Impact)
	assert.True(t, *incident.System)
	assert.NotEmpty(t, incident.Components)
}

// Helper function to clean up open incidents before each test.
func cleanupOpenIncidents(t *testing.T, r *gin.Engine) {
	t.Helper()
	incidents := v2GetEvents(t, r)
	for _, inc := range incidents {
		if inc.EndDate == nil {
			// Close open incidents
			endDate := time.Now().Add(-time.Hour).UTC()
			inc.EndDate = &endDate
			v2PatchIncident(t, r, inc)
		} else if inc.Type == event.TypeMaintenance {
			// Cancel maintenances if not already cancelled
			v2PatchIncident(t, r, inc, event.MaintenanceCancelled)
		}
	}
}

// Helper function that returns both response and status code.
func v2CreateIncidentWithStatus(t *testing.T, r *gin.Engine, inc *v2.IncidentData) (*v2.PostIncidentResp, int) {
	t.Helper()

	data, err := json.Marshal(inc)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, v2IncidentsEndpoint, bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		return nil, w.Code
	}

	respCreated := &v2.PostIncidentResp{}
	err = json.Unmarshal(w.Body.Bytes(), respCreated)
	require.NoError(t, err)

	return respCreated, w.Code
}
