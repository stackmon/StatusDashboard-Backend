package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apiErrors "github.com/stackmon/otc-status-dashboard/internal/api/errors"
	v2 "github.com/stackmon/otc-status-dashboard/internal/api/v2"
	"github.com/stackmon/otc-status-dashboard/internal/event"
)

const (
	v2IncidentsEndpoint    = "/v2/incidents"
	v2AvailabilityEndpoint = "/v2/availability"
	v2EventsEndpoint       = "/v2/events"
)

// V2IncidentsListResponse defines the expected structure for the GET /v2/incidents endpoint.
type V2IncidentsListResponse struct {
	Data    []*v2.Incident `json:"data"`
	Message string         `json:"message,omitempty"`
}

func TestV2GetIncidentsHandler(t *testing.T) {
	t.Logf("start to test GET %s", v2IncidentsEndpoint)
	r, _ := initTests(t)

	// Clean up and create a known incident for this test
	truncateIncidents(t)

	components := []int{1}
	impact := 1
	title := "Test incident for get handler"
	startDate := time.Now().Add(-24 * time.Hour).UTC()
	system := false
	incidentCreateData := v2.IncidentData{
		Title:      title,
		Impact:     &impact,
		Components: components,
		StartDate:  startDate,
		System:     &system,
		Type:       event.TypeIncident,
	}

	// Create incident via API
	createBody, err := json.Marshal(incidentCreateData)
	require.NoError(t, err)
	createReq, _ := http.NewRequest(http.MethodPost, v2IncidentsEndpoint, bytes.NewReader(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createReq.Header.Set("Authorization", "Bearer "+adminToken)
	createW := httptest.NewRecorder()
	r.ServeHTTP(createW, createReq)
	require.Equal(t, http.StatusOK, createW.Code, "failed to create test incident: %s", createW.Body.String())

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, v2IncidentsEndpoint, nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)

	r.ServeHTTP(w, req)

	incidents := map[string][]*v2.Incident{}

	assert.Equal(t, 200, w.Code)

	err = json.Unmarshal(w.Body.Bytes(), &incidents)
	require.NoError(t, err)
	require.NotEmpty(t, incidents["data"], "expected at least one incident")

	found := false
	for _, inc := range incidents["data"] {
		if inc.Title == title {
			assert.Equal(t, impact, *inc.Impact)
			assert.Equal(t, event.TypeIncident, inc.Type)
			found = true
			break
		}
	}
	require.True(t, found, "expected incident with title %q not found", title)
}

func TestV2GetComponentsHandler(t *testing.T) {
	t.Log("start to test GET /v2/components")
	r, _ := initTests(t)

	var response = `[{"id":1,"name":"Cloud Container Engine","attributes":[{"name":"region","value":"EU-DE"},{"name":"category","value":"Container"},{"name":"type","value":"cce"}]},{"id":2,"name":"Cloud Container Engine","attributes":[{"name":"region","value":"EU-NL"},{"name":"category","value":"Container"},{"name":"type","value":"cce"}]},{"id":3,"name":"Elastic Cloud Server","attributes":[{"name":"region","value":"EU-DE"},{"name":"category","value":"Compute"},{"name":"type","value":"ecs"}]},{"id":4,"name":"Elastic Cloud Server","attributes":[{"name":"region","value":"EU-NL"},{"name":"category","value":"Compute"},{"name":"type","value":"ecs"}]},{"id":5,"name":"Distributed Cache Service","attributes":[{"name":"region","value":"EU-DE"},{"name":"category","value":"Database"},{"name":"type","value":"dcs"}]},{"id":6,"name":"Distributed Cache Service","attributes":[{"name":"region","value":"EU-NL"},{"name":"category","value":"Database"},{"name":"type","value":"dcs"}]}]`

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/v2/components", nil)

	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Equal(t, response, w.Body.String())
}

func TestV2PostIncidentsHandlerNegative(t *testing.T) {
	t.Log("start to test incident creation and check json data for /v2/incidents")
	r, _ := initTests(t)

	type testCase struct {
		ExpectedCode int
		Expected     string
		JSON         string
	}

	jsEndPresent := `{
  "title":"OpenStack Upgrade in regions EU-DE/EU-NL",
  "impact":1,
  "components":[
    1
  ],
  "start_date":"2024-11-25T09:32:14.075Z",
  "end_date":"2024-11-25T09:32:14.075Z",
  "system":false,
  "type":"incident",
  "updates":[
    {
      "id":163,
      "status":"resolved",
      "text":"issue resolved",
      "timestamp":"2024-11-25T09:32:14.075Z"
    }
  ]
}`
	jsUpdatesPresent := `{
  "title":"OpenStack Upgrade in regions EU-DE/EU-NL",
  "impact":1,
  "components":[
    1
  ],
  "start_date":"2024-11-25T09:32:14.075Z",
  "system":false,
  "type":"incident",
  "updates":[
    {
      "id":163,
      "status":"resolved",
      "text":"issue resolved",
      "timestamp":"2024-11-25T09:32:14.075Z"
    }
  ]
}`
	jsWrongComponents := `{
  "title":"OpenStack Upgrade in regions EU-DE/EU-NL",
  "impact":1,
  "components":[
    218,
    254
  ],
  "start_date":"2024-11-25T09:32:14.075Z",
  "system":false,
  "type":"incident"
}`
	jsWrongMaintenanceImpact := `{
  "title":"Maintenance with wrong impact",
  "impact":1,
  "components":[1],
  "start_date":"2099-11-25T09:32:14.075Z",
  "end_date":"2099-11-25T10:32:14.075Z",
  "description":"maintenance description",
  "contact_email":"test@example.com",
  "system":false,
  "type":"maintenance"
}`

	jsWrongIncidentImpact := `{
  "title":"event with maintenance impact",
  "impact":0,
  "components":[1],
  "start_date":"2024-11-25T09:32:14.075Z",
  "system":false,
  "type":"incident"
}`

	testCases := map[string]*testCase{
		"negative testcase, event is not a maintenance and end_date is present": {
			JSON:         jsEndPresent,
			Expected:     `{"errMsg":"event end_date should be empty"}`,
			ExpectedCode: 400,
		},
		"negative testcase, updates are present": {
			JSON:         jsUpdatesPresent,
			Expected:     `{"errMsg":"event updates should be empty"}`,
			ExpectedCode: 400,
		},
		"negative testcase, wrong components ids": {
			JSON:         jsWrongComponents,
			Expected:     `{"errMsg":"component does not exist, component_id: 218"}`,
			ExpectedCode: 400,
		},
		"negative testcase, maintenance with non-zero impact": {
			JSON:         jsWrongMaintenanceImpact,
			Expected:     `{"errMsg":"impact must be 0 for type 'maintenance' or 'info' and gt 0 for 'incident'"}`,
			ExpectedCode: 400,
		},
		"negative testcase, event with zero impact": {
			JSON:         jsWrongIncidentImpact,
			Expected:     `{"errMsg":"impact must be 0 for type 'maintenance' or 'info' and gt 0 for 'incident'"}`,
			ExpectedCode: 400,
		},
	}

	for title, c := range testCases {
		t.Logf("start test case: %s\n", title)

		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodPost, v2IncidentsEndpoint, strings.NewReader(c.JSON))
		req.Header.Set("Authorization", "Bearer "+adminToken)
		r.ServeHTTP(w, req)

		assert.Equal(t, c.ExpectedCode, w.Code)
		assert.Equal(t, c.Expected, w.Body.String())
	}
}

func TestV2PostIncidentsDescriptionLengthLimits(t *testing.T) {
	t.Log("start to test description length limits for /v2/incidents")
	truncateIncidents(t)
	r, _ := initTests(t)

	impact := 1
	system := false
	base := v2.IncidentData{
		Title:      "Incident with boundary description length",
		Impact:     &impact,
		Components: []int{1},
		StartDate:  time.Now().Add(-time.Hour).UTC(),
		System:     &system,
		Type:       event.TypeIncident,
	}

	t.Run("description with 1500 characters is accepted", func(t *testing.T) {
		data := base
		data.Description = strings.Repeat("a", 1500)

		payload, err := json.Marshal(data)
		require.NoError(t, err)

		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodPost, v2IncidentsEndpoint, bytes.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+adminToken)
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("description with 1501 characters is rejected", func(t *testing.T) {
		data := base
		data.Description = strings.Repeat("a", 1501)

		payload, err := json.Marshal(data)
		require.NoError(t, err)

		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodPost, v2IncidentsEndpoint, bytes.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+adminToken)
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.JSONEq(t, fmt.Sprintf(`{"errMsg":"%s"}`, apiErrors.ErrIncidentDescriptionTooLong), w.Body.String())
	})
}

func TestV2PostIncidentsHandler(t *testing.T) {
	t.Log("start to test incident creation for /v2/incidents")
	r, _ := initTests(t)

	t.Log("check if all incidents have end date, if not, set it to start date + 1ms")
	incidents := v2GetIncidents(t, r)
	for _, inc := range incidents {
		if inc.EndDate == nil {
			endDate := inc.StartDate.Add(time.Millisecond * 1).UTC()
			inc.EndDate = &endDate
			v2PatchIncident(t, r, inc)
		}
		if inc.Type == event.TypeMaintenance {
			t.Log("the component is maintenance, cancel it")
			v2PatchIncident(t, r, inc, event.MaintenanceCancelled)
		}
	}

	components := []int{1, 2}
	impact := 1
	title := "Test incident creation for api V2 for components: 1, 2. Test 1."
	description := "any description for incident"
	startDate := time.Now().AddDate(0, 0, -1).UTC()
	system := false
	incType := event.TypeIncident

	incidentCreateData := v2.IncidentData{
		Title:        title,
		Description:  description,
		ContactEmail: "test@example.com",
		Impact:       &impact,
		Components:   components,
		StartDate:    startDate,
		System:       &system,
		Type:         incType,
	}

	result := v2CreateIncident(t, r, &incidentCreateData)
	require.NotNil(t, result, "v2CreateIncident returned nil")

	assert.Len(t, result.Result, len(incidentCreateData.Components))
	assert.Empty(t, result.Result[0].Error)
	assert.Empty(t, result.Result[1].Error)
	assert.Equal(t, len(incidents)+1, result.Result[0].IncidentID)
	assert.Equal(t, len(incidents)+1, result.Result[1].IncidentID)

	t.Log("check created incident data, incident id: ", result.Result[0].IncidentID)
	incident := v2GetIncident(t, r, result.Result[0].IncidentID)
	assert.Equal(t, incidentCreateData.StartDate.Truncate(time.Microsecond), incident.StartDate)
	assert.Equal(t, title, incident.Title)
	assert.Equal(t, impact, *incident.Impact)
	assert.Equal(t, system, *incident.System)
	assert.Nil(t, incident.EndDate)
	require.NotNil(t, incident.Type)
	assert.Equal(t, event.TypeIncident, incident.Type)
	require.NotNil(t, incident.Updates)
	assert.Equal(t, "The incident is detected.", incident.Updates[0].Text)

	t.Log("create a new incident with the same components and the same impact, should close previous and move components to the new")
	t.Log("current time:", time.Now().UTC())
	incidentCreateData.Title = "Test incident creation for api V2 for components: 1, 2. Test should close previous and move components to the new."
	result = v2CreateIncident(t, r, &incidentCreateData)
	require.NotNil(t, result, "v2CreateIncident returned nil")
	assert.Equal(t, len(incidents)+2, result.Result[0].IncidentID)
	assert.Equal(t, len(incidents)+2, result.Result[1].IncidentID)

	oldIncident := v2GetIncident(t, r, result.Result[0].IncidentID-1)
	assert.NotNil(t, oldIncident.EndDate)
	assert.Len(t, oldIncident.Components, 1)
	assert.NotNil(t, oldIncident.Updates)
	assert.Len(t, oldIncident.Updates, 3)
	t.Logf("STATUS updates: %v", oldIncident.Updates)
	assert.Equal(t, event.IncidentDetected, oldIncident.Updates[0].Status)
	assert.Equal(t, event.OutDatedSystem, oldIncident.Updates[1].Status)
	assert.Equal(t, event.IncidentResolved, oldIncident.Updates[2].Status)
	assert.Equal(t, "The incident is detected.", oldIncident.Updates[0].Text)
	assert.Equal(t, fmt.Sprintf("Cloud Container Engine (Container, EU-DE, cce) moved to <a href='/incidents/%d'>Test incident creation for api V2 for components: 1, 2. Test should close previous and move components to the new.</a>", result.Result[0].IncidentID), oldIncident.Updates[1].Text)
	assert.Equal(t, fmt.Sprintf("Cloud Container Engine (Container, EU-NL, cce) moved to <a href='/incidents/%d'>Test incident creation for api V2 for components: 1, 2. Test should close previous and move components to the new.</a>, Incident closed by system", result.Result[0].IncidentID), oldIncident.Updates[2].Text)

	incidentN3 := v2GetIncident(t, r, result.Result[0].IncidentID)
	assert.Nil(t, incidentN3.EndDate)
	assert.Len(t, incidentN3.Components, 2)
	assert.NotNil(t, incidentN3.Updates)
	assert.Len(t, incidentN3.Updates, 3)
	assert.Equal(t, event.IncidentDetected, incidentN3.Updates[0].Status)
	assert.Equal(t, event.OutDatedSystem, incidentN3.Updates[1].Status)
	assert.Equal(t, event.OutDatedSystem, incidentN3.Updates[2].Status)
	assert.Equal(t, "The incident is detected.", incidentN3.Updates[0].Text)
	assert.Equal(t, fmt.Sprintf("Cloud Container Engine (Container, EU-DE, cce) moved from <a href='/incidents/%d'>Test incident creation for api V2 for components: 1, 2. Test 1.</a>", result.Result[0].IncidentID-1), incidentN3.Updates[1].Text)
	assert.Equal(t, fmt.Sprintf("Cloud Container Engine (Container, EU-NL, cce) moved from <a href='/incidents/%d'>Test incident creation for api V2 for components: 1, 2. Test 1.</a>", result.Result[0].IncidentID-1), incidentN3.Updates[2].Text)

	t.Log("create a new maintenance with the same components and higher impact, should create a new without components")

	impact = 0
	title = "Test maintenance creation for api V2 for the components: 1-Cloud Container Engine (Container, EU-DE, cce), 2-Cloud Container Engine (Container, EU-NL, cce)"
	incidentCreateData.Title = title
	incidentCreateData.Description = "any description for maintenance incident"
	incidentCreateData.ContactEmail = "test@example.com"
	startDate = time.Now().Add(time.Hour * 1).UTC()
	incidentCreateData.StartDate = startDate
	endDate := time.Now().AddDate(0, 0, 1).UTC()
	incidentCreateData.EndDate = &endDate
	incidentCreateData.Type = event.TypeMaintenance

	result = v2CreateIncident(t, r, &incidentCreateData)
	require.NotNil(t, result, "v2CreateIncident returned nil")
	assert.Equal(t, len(incidents)+3, result.Result[0].IncidentID)
	assert.Equal(t, len(incidents)+3, result.Result[1].IncidentID)

	maintenanceIncident := v2GetIncident(t, r, result.Result[0].IncidentID)
	assert.Equal(t, incidentCreateData.StartDate.Truncate(time.Microsecond), maintenanceIncident.StartDate)
	require.NotNil(t, incidentCreateData.EndDate)
	require.NotNil(t, maintenanceIncident.EndDate)
	assert.Equal(t, incidentCreateData.EndDate.Truncate(time.Microsecond), maintenanceIncident.EndDate.Truncate(time.Microsecond))
	assert.Equal(t, title, maintenanceIncident.Title)
	assert.Equal(t, impact, *maintenanceIncident.Impact)
	assert.Equal(t, system, *maintenanceIncident.System)
	assert.Equal(t, incidentCreateData.Description, maintenanceIncident.Description)
	assert.Equal(t, event.MaintenancePlanned, maintenanceIncident.Updates[0].Status)
	require.NotNil(t, maintenanceIncident.Type)
	assert.Equal(t, event.TypeMaintenance, maintenanceIncident.Type)
	assert.Equal(t, event.MaintenancePlanned, maintenanceIncident.Updates[0].Status)

	incidentN3 = v2GetIncident(t, r, result.Result[0].IncidentID-1)
	assert.Nil(t, incidentN3.EndDate)
	assert.Len(t, incidentN3.Components, 2)
	assert.NotNil(t, incidentN3.Updates)
	assert.Len(t, incidentN3.Updates, 3)
	assert.Equal(t, event.IncidentDetected, incidentN3.Updates[0].Status)
	assert.Equal(t, event.OutDatedSystem, incidentN3.Updates[1].Status)
	assert.Equal(t, event.OutDatedSystem, incidentN3.Updates[1].Status)
	assert.Equal(t, "The incident is detected.", incidentN3.Updates[0].Text)
	assert.Equal(t, fmt.Sprintf("Cloud Container Engine (Container, EU-DE, cce) moved from <a href='/incidents/%d'>Test incident creation for api V2 for components: 1, 2. Test 1.</a>", incidentN3.ID-1), incidentN3.Updates[1].Text)
	assert.Equal(t, fmt.Sprintf("Cloud Container Engine (Container, EU-NL, cce) moved from <a href='/incidents/%d'>Test incident creation for api V2 for components: 1, 2. Test 1.</a>", incidentN3.ID-1), incidentN3.Updates[2].Text)
	require.NotNil(t, incidentN3.Type)
	assert.Equal(t, event.TypeIncident, incidentN3.Type)

	t.Log("check response, if incident component is not present in the opened incidents, should create a new incident")
	components = []int{3}
	impact = 1
	startDate = time.Now().AddDate(0, 0, -1).UTC()
	incidentCreateData = v2.IncidentData{
		Title:       "Test for another different component id: 3.",
		Description: "Any description for incident with different component",
		Impact:      &impact,
		Components:  components,
		StartDate:   startDate,
		System:      &system,
		Type:        event.TypeIncident,
	}
	result = v2CreateIncident(t, r, &incidentCreateData)
	require.NotNil(t, result, "v2CreateIncident returned nil")
	assert.NotZero(t, result.Result[0].IncidentID)
	assert.Equal(t, len(incidents)+4, result.Result[0].IncidentID)
	assert.Equal(t, 3, result.Result[0].ComponentID)
}

func TestV2PatchIncidentHandlerNegative(t *testing.T) {
	t.Log("start to test negative cases for incident patching and check json data for /v2/incidents/42")
	r, _ := initTests(t)

	components := []int{1}
	impact := 1
	title := "Incident for negative tests for incident patching"
	startDate := time.Now().AddDate(0, 0, -1).UTC()
	system := false

	incidentCreateData := v2.IncidentData{
		Title:       title,
		Description: "any description for incident",
		Impact:      &impact,
		Components:  components,
		StartDate:   startDate,
		System:      &system,
		Type:        event.TypeIncident,
	}

	resp := v2CreateIncident(t, r, &incidentCreateData)
	require.NotNil(t, resp, "v2CreateIncident returned nil")
	incID10 := resp.Result[0].IncidentID

	type testCase struct {
		ExpectedCode int
		Expected     string
		JSON         string
	}

	jsWrongOpenedStatus := `{
		"title": "OpenStack Upgrade in regions EU-DE/EU-NL",
	 	"impact": 1,
	 	"message": "Any message why the incident was updated.",
	 	"status": "in_progres",
	 	"update_date": "2024-12-11T14:46:03.877Z",
	 	"start_date": "2024-12-11T14:46:03.877Z",
	 	"end_date": "2024-12-11T14:46:03.877Z",
		"type": "incident",
		"version": 1
	}`
	jsWrongOpenedStartDate := `{
	 "impact": 1,
	 "message": "Any message why the incident was updated.",
	 "status": "analysing",
	 "update_date": "2024-12-11T14:46:03.877Z",
	 "start_date": "2024-12-11T14:46:03.877Z",
	 "type": "incident",
	 "version": 1
	}`
	jsWrongOpenedStatusForChangingImpact := `{
	"impact": 0,
	"message": "Any message why the event was updated.",
	"status": "analysing",
	"update_date": "2024-12-11T14:46:03.877Z",
	"type": "maintenance",
	"version": 1
	}`
	jsWrongOpenedMaintenanceImpact := `{
	 "impact": 0,
	 "message": "Any message why the event was updated.",
	 "status": "impact changed",
	 "update_date": "2024-12-11T14:46:03.877Z",
	 "type": "maintenance",
	 "version": 1
	}`
	testCases := map[string]*testCase{
		"negative testcase, wrong status for opened incident": {
			JSON:         jsWrongOpenedStatus,
			Expected:     `{"errMsg":"wrong status for incident"}`,
			ExpectedCode: 400,
		},
		"negative testcase, wrong start date for opened incident": {
			JSON:         jsWrongOpenedStartDate,
			Expected:     `{"errMsg":"can not change start date for open incident"}`,
			ExpectedCode: 400,
		},
		"negative testcase, wrong status for changing impact": {
			JSON:         jsWrongOpenedStatusForChangingImpact,
			Expected:     `{"errMsg":"wrong status for changing impact"}`,
			ExpectedCode: 400,
		},
		"negative testcase, can't change impact from incident to maintenance": {
			JSON:         jsWrongOpenedMaintenanceImpact,
			Expected:     `{"errMsg":"can not change impact to 0"}`,
			ExpectedCode: 400,
		},
	}

	for testName, c := range testCases {
		t.Logf("start test case: %s\n", testName)

		url := fmt.Sprintf("/v2/incidents/%d", incID10)

		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodPatch, url, strings.NewReader(c.JSON))
		req.Header.Set("Authorization", "Bearer "+adminToken)
		r.ServeHTTP(w, req)

		assert.Equal(t, c.ExpectedCode, w.Code)
		assert.Equal(t, c.Expected, w.Body.String())
	}
}

func TestV2PatchIncidentHandler(t *testing.T) {
	t.Log("start to test incident patching")
	r, _ := initTests(t)

	components := []int{1}
	impact := 1
	title := "Test incident for patching test"
	description := "Test case Patch. Any description for incident"
	startDate := time.Now().AddDate(0, 0, -2).UTC()
	system := false

	currentVersion := 1

	internalPatch := func(id int, p *v2.PatchIncidentData) *v2.Incident {
		p.Version = &currentVersion
		d, err := json.Marshal(p)
		require.NoError(t, err)

		url := fmt.Sprintf("/v2/incidents/%d", id)
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodPatch, url, bytes.NewReader(d))
		req.Header.Set("Authorization", "Bearer "+adminToken)

		r.ServeHTTP(w, req)
		assert.Equal(t, 200, w.Code)

		inc := &v2.Incident{}
		err = json.Unmarshal(w.Body.Bytes(), inc)
		require.NoError(t, err)
		if inc.Version != nil {
			currentVersion = *inc.Version
		}
		return inc
	}

	incidentCreateData := v2.IncidentData{
		Title:        title,
		Description:  description,
		ContactEmail: "test@example.com",
		Impact:       &impact,
		Components:   components,
		StartDate:    startDate,
		System:       &system,
		Type:         event.TypeIncident,
	}

	resp := v2CreateIncident(t, r, &incidentCreateData)
	require.NotNil(t, resp, "v2CreateIncident returned nil")
	incID := resp.Result[0].IncidentID
	if fetched := v2GetIncident(t, r, incID); fetched != nil && fetched.Version != nil {
		currentVersion = *fetched.Version
	}

	newTitle := "patched incident title"
	newDescription := "patched incident description"
	t.Logf("patching incident title, from %s to %s", title, newTitle)

	pData := v2.PatchIncidentData{
		Title:       &newTitle,
		Description: &newDescription,
		Message:     "update title",
		Status:      "analysing",
		UpdateDate:  time.Now().UTC(),
	}

	inc := internalPatch(incID, &pData)
	assert.Equal(t, newTitle, inc.Title)
	assert.Equal(t, newDescription, inc.Description)

	newImpact := 2
	t.Logf("patching incident impact, from %d to %d", impact, newImpact)

	pData.Impact = &newImpact
	pData.Status = event.IncidentImpactChanged

	inc = internalPatch(incID, &pData)
	assert.Equal(t, newImpact, *inc.Impact)

	t.Logf("close incident")
	pData.Status = event.IncidentResolved
	updateDate := time.Now().UTC()
	pData.UpdateDate = updateDate

	inc = internalPatch(incID, &pData)
	require.NotNil(t, inc.EndDate)
	assert.Equal(t, updateDate.Truncate(time.Microsecond), inc.EndDate.Truncate(time.Microsecond))

	t.Logf("patching closed incident, change start date and end date")
	startDate = time.Now().AddDate(0, 0, -1).UTC()
	endDate := time.Now().UTC()

	pData.Status = event.IncidentChanged
	pData.StartDate = &startDate
	pData.EndDate = &endDate

	inc = internalPatch(incID, &pData)
	assert.Equal(t, startDate.Truncate(time.Microsecond), inc.StartDate)
	assert.Equal(t, event.IncidentChanged, inc.Status)
	require.NotNil(t, inc.EndDate)
	assert.Equal(t, endDate.Truncate(time.Microsecond), inc.EndDate.Truncate(time.Microsecond))

	t.Logf("reopen closed incident")

	pData.Status = event.IncidentReopened
	pData.StartDate = nil
	pData.EndDate = nil
	inc = internalPatch(incID, &pData)
	assert.Nil(t, inc.EndDate)

	t.Logf("final close the test incident")

	pData.Status = event.IncidentResolved
	inc = internalPatch(incID, &pData)
	assert.Equal(t, event.IncidentResolved, inc.Status)
	assert.NotNil(t, inc.EndDate)
}

func TestV2PostIncidentExtractHandler(t *testing.T) {
	t.Log("start to test component extraction from incident for the endpoint /v2/incidents/42/extract")
	r, _ := initTests(t)

	t.Log("check if all incidents have end date, if not, set it to start date + 1ms")
	incidents := v2GetIncidents(t, r)
	for _, inc := range incidents {
		if inc.EndDate == nil {
			endDate := inc.StartDate.Add(time.Millisecond * 1).UTC()
			inc.EndDate = &endDate
			v2PatchIncident(t, r, inc)
		}
		if inc.Type == event.TypeMaintenance {
			t.Log("the component is maintenance, cancel it")
			v2PatchIncident(t, r, inc, event.MaintenanceCancelled)
		}
	}

	components := []int{1, 2}
	impact := 1
	title := "Test component extraction for component dcs"
	description := "Test incident for extraction"
	startDate := time.Now().AddDate(0, 0, -1).UTC()
	system := false

	incidentCreateData := v2.IncidentData{
		Title:        title,
		Description:  description,
		ContactEmail: "test@example.com",
		Impact:       &impact,
		Components:   components,
		StartDate:    startDate,
		System:       &system,
		Type:         event.TypeIncident,
	}

	t.Log("create a initial incident", incidentCreateData)
	result := v2CreateIncident(t, r, &incidentCreateData)
	require.NotNil(t, result, "v2CreateIncident returned nil")

	t.Logf("prepare to extract components: %d from incident %d", 2, result.Result[0].IncidentID)
	type IncidentData struct {
		Components []int `json:"components"`
	}
	movedComponents := IncidentData{Components: []int{2}}
	data, err := json.Marshal(movedComponents)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, v2IncidentsEndpoint+fmt.Sprintf("/%d/extract", result.Result[0].IncidentID), bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	t.Log("check the new incident created by extraction")
	newInc := &v2.Incident{}
	err = json.Unmarshal(w.Body.Bytes(), newInc)
	require.NoError(t, err)
	assert.Len(t, newInc.Components, 1)
	assert.Equal(t, incidentCreateData.Impact, newInc.Impact)
	assert.Equal(t, description, newInc.Description)
	assert.Equal(t, fmt.Sprintf("Cloud Container Engine (Container, EU-NL, cce) moved from <a href='/incidents/%d'>Test component extraction for component dcs</a>", result.Result[0].IncidentID), newInc.Updates[0].Text)

	t.Log("check the old incident with a record about extraction")
	createdInc := v2GetIncident(t, r, result.Result[0].IncidentID)
	assert.Equal(t, "The incident is detected.", createdInc.Updates[0].Text)
	assert.Equal(t, fmt.Sprintf("Cloud Container Engine (Container, EU-NL, cce) moved to <a href='/incidents/%d'>Test component extraction for component dcs</a>", newInc.ID), createdInc.Updates[1].Text)

	t.Log("start negative case, try to extract all components from the incident, should return error")
	// start negative case
	movedComponents = IncidentData{Components: []int{1}}
	data, err = json.Marshal(movedComponents)
	require.NoError(t, err)

	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodPost, v2IncidentsEndpoint+fmt.Sprintf("/%d/extract", result.Result[0].IncidentID), bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.JSONEq(t, `{"errMsg":"can not move all components to the new incident, keep at least one"}`, w.Body.String())

	t.Log("start negative case, try to extract duplicate components, should return error")
	movedComponents = IncidentData{Components: []int{1, 1}}
	data, err = json.Marshal(movedComponents)
	require.NoError(t, err)

	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodPost, v2IncidentsEndpoint+fmt.Sprintf("/%d/extract", result.Result[0].IncidentID), bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "unique")
}

func v2CreateIncident(t *testing.T, r *gin.Engine, inc *v2.IncidentData) *v2.PostIncidentResp {
	t.Helper()

	data, err := json.Marshal(inc)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, v2IncidentsEndpoint, bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Logf("v2CreateIncident error: %s", w.Body.String())
		return nil
	}

	assert.Equal(t, http.StatusOK, w.Code)

	respCreated := &v2.PostIncidentResp{}
	err = json.Unmarshal(w.Body.Bytes(), respCreated)
	require.NoError(t, err)

	return respCreated
}

func v2GetIncident(t *testing.T, r *gin.Engine, id int) *v2.Incident {
	t.Helper()
	url := fmt.Sprintf("/v2/incidents/%d", id)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)

	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	var incident v2.Incident
	err := json.Unmarshal(w.Body.Bytes(), &incident)
	require.NoError(t, err)

	return &incident
}

func v2GetIncidents(t *testing.T, r *gin.Engine) []*v2.Incident {
	t.Helper()
	url := "/v2/incidents"
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	data := map[string][]*v2.Incident{}
	err := json.Unmarshal(w.Body.Bytes(), &data)
	require.NoError(t, err)

	return data["data"]
}

func v2PatchIncident(t *testing.T, r *gin.Engine, inc *v2.Incident, status ...event.Status) {
	t.Helper()

	st := event.IncidentResolved

	if len(status) == 1 {
		st = status[0]
	}

	version := 1
	if inc.Version != nil {
		version = *inc.Version
	}

	patch := v2.PatchIncidentData{
		Message:    "closed",
		Status:     st,
		UpdateDate: *inc.EndDate,
		Version:    &version,
	}

	d, err := json.Marshal(patch)
	require.NoError(t, err)

	url := fmt.Sprintf("/v2/incidents/%d", inc.ID)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPatch, url, bytes.NewReader(d))
	req.Header.Set("Authorization", "Bearer "+adminToken)

	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)

	updated := v2.Incident{}
	err = json.Unmarshal(w.Body.Bytes(), &updated)
	require.NoError(t, err)
	*inc = updated
}

func TestV2CreateComponentAndList(t *testing.T) {
	t.Log("start to test component creation and listing")
	r, _ := initTests(t)

	// Test case 1: Successful component creation
	t.Log("Test case 1: Create new component successfully")
	newComponent := v2.PostComponentData{
		Name: "Domain Name System",
		Attributes: []v2.ComponentAttribute{
			{Name: "type", Value: "dns"},
			{Name: "region", Value: "EU-DE"},
			{Name: "category", Value: "Network"},
		},
	}

	w := httptest.NewRecorder()
	data, _ := json.Marshal(newComponent)
	req, _ := http.NewRequest(http.MethodPost, "/v2/components", bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusCreated, w.Code)

	var createdComponent v2.Component
	err := json.Unmarshal(w.Body.Bytes(), &createdComponent)
	require.NoError(t, err)
	assert.Equal(t, newComponent.Name, createdComponent.Name)
	assert.Len(t, newComponent.Attributes, len(createdComponent.Attributes))

	// Test case 2: Try to create the same component again
	t.Log("Test case 2: Try to create duplicate component")
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodPost, "/v2/components", bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "component already exists")

	// Test case 3: Try to create component with invalid attributes (duplicate region)
	t.Log("Test case 3: Create component with invalid attributes")
	invalidComponent := v2.PostComponentData{
		Name: "Invalid Component",
		Attributes: []v2.ComponentAttribute{
			{Name: "region", Value: "EU-DE"},
			{Name: "region", Value: "EU-NL"}, // Duplicate attribute name
			{Name: "type", Value: "test"},
		},
	}

	data, _ = json.Marshal(invalidComponent)
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodPost, "/v2/components", bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "component attribute has invalid format")
}

func TestV2GetIncidentsFilteredHandler(t *testing.T) {
	t.Log("start to test GET /v2/incidents with filters")
	truncateIncidents(t)
	r, _ := initTests(t)

	type filterTestCase struct {
		name          string
		queryParams   map[string]string
		expectedIDs   []int
		expectedCount int
	}

	now := time.Now().UTC()

	impactMinor := 1
	impactMajor := 2
	impactOutage := 3
	impactMaintenance := 0
	systemTrue := true
	systemFalse := false

	resolvedStart := now.Add(-30 * 24 * time.Hour).Truncate(time.Second)
	resolvedEnd := now.Add(-29 * 24 * time.Hour).Truncate(time.Second)
	resolvedIncidentData := v2.IncidentData{
		Title:      "Resolved incident",
		Impact:     &impactMinor,
		Components: []int{1},
		StartDate:  resolvedStart,
		System:     &systemTrue,
		Type:       event.TypeIncident,
	}
	resolvedResp := v2CreateIncident(t, r, &resolvedIncidentData)
	require.NotNil(t, resolvedResp, "Failed to create resolved incident")
	resolvedID := resolvedResp.Result[0].IncidentID
	resolvedIncident := v2GetIncident(t, r, resolvedID)
	resolvedIncident.EndDate = &resolvedEnd
	v2PatchIncident(t, r, resolvedIncident)

	majorStart := now.Add(-20 * 24 * time.Hour).Truncate(time.Second)
	majorIncidentData := v2.IncidentData{
		Title:      "Major incident",
		Impact:     &impactMajor,
		Components: []int{2},
		StartDate:  majorStart,
		System:     &systemFalse,
		Type:       event.TypeIncident,
	}
	majorResp := v2CreateIncident(t, r, &majorIncidentData)
	require.NotNil(t, majorResp, "Failed to create major incident")
	majorID := majorResp.Result[0].IncidentID

	outageStart := now.Add(-10 * 24 * time.Hour).Truncate(time.Second)
	outageIncidentData := v2.IncidentData{
		Title:      "Outage incident",
		Impact:     &impactOutage,
		Components: []int{3},
		StartDate:  outageStart,
		System:     &systemTrue,
		Type:       event.TypeIncident,
	}
	outageResp := v2CreateIncident(t, r, &outageIncidentData)
	require.NotNil(t, outageResp, "Failed to create outage incident")
	outageID := outageResp.Result[0].IncidentID

	activeStart := now.Add(-2 * 24 * time.Hour).Truncate(time.Second)
	activeIncidentData := v2.IncidentData{
		Title:      "Active minor incident",
		Impact:     &impactMinor,
		Components: []int{4},
		StartDate:  activeStart,
		System:     &systemFalse,
		Type:       event.TypeIncident,
	}
	activeResp := v2CreateIncident(t, r, &activeIncidentData)
	require.NotNil(t, activeResp, "Failed to create active minor incident")
	activeID := activeResp.Result[0].IncidentID

	maintenanceStart := now.Add(24 * time.Hour).Truncate(time.Second)
	maintenanceEnd := maintenanceStart.Add(2 * time.Hour).Truncate(time.Second)
	maintenanceData := v2.IncidentData{
		Title:        "Planned maintenance",
		Description:  "Maintenance window",
		ContactEmail: "test@example.com",
		Impact:       &impactMaintenance,
		Components:   []int{1},
		StartDate:    maintenanceStart,
		EndDate:      &maintenanceEnd,
		System:       &systemFalse,
		Type:         event.TypeMaintenance,
	}
	maintenanceResp := v2CreateIncident(t, r, &maintenanceData)
	require.NotNil(t, maintenanceResp, "Failed to create maintenance")
	maintenanceID := maintenanceResp.Result[0].IncidentID

	startDateFilter := resolvedStart.Format(time.RFC3339)
	endDateFilter := resolvedEnd.Format(time.RFC3339)

	testCases := []filterTestCase{
		{
			name:          "No filters",
			queryParams:   nil,
			expectedIDs:   []int{resolvedID, majorID, outageID, activeID, maintenanceID},
			expectedCount: 5,
		},
		{
			name:          "Filter by start_date",
			queryParams:   map[string]string{"start_date": startDateFilter},
			expectedIDs:   []int{resolvedID, majorID, outageID, activeID, maintenanceID},
			expectedCount: 5,
		},
		{
			name:          "Filter by end_date",
			queryParams:   map[string]string{"end_date": endDateFilter},
			expectedIDs:   []int{resolvedID},
			expectedCount: 1,
		},
		{
			name:          "Filter by impact minor (1)",
			queryParams:   map[string]string{"impact": "1"},
			expectedIDs:   []int{resolvedID, activeID},
			expectedCount: 2,
		},
		{
			name:          "Filter by impact major (2)",
			queryParams:   map[string]string{"impact": "2"},
			expectedIDs:   []int{majorID},
			expectedCount: 1,
		},
		{
			name:          "Filter by impact maintenance (0)",
			queryParams:   map[string]string{"impact": "0"},
			expectedIDs:   []int{maintenanceID},
			expectedCount: 1,
		},
		{
			name:          "Filter by component_id 1",
			queryParams:   map[string]string{"components": "1"},
			expectedIDs:   []int{resolvedID, maintenanceID},
			expectedCount: 2,
		},
		{
			name:          "Filter by non-existent component_id 8",
			queryParams:   map[string]string{"components": "8"},
			expectedIDs:   []int{},
			expectedCount: 0,
		},
		{
			name:          "Filter by system true",
			queryParams:   map[string]string{"system": "true"},
			expectedIDs:   []int{resolvedID, outageID},
			expectedCount: 2,
		},
		{
			name:          "Filter by system false",
			queryParams:   map[string]string{"system": "false"},
			expectedIDs:   []int{majorID, activeID, maintenanceID},
			expectedCount: 3,
		},
		{
			name:          "Filter by active true",
			queryParams:   map[string]string{"active": "true"},
			expectedIDs:   []int{majorID, outageID, activeID},
			expectedCount: 3,
		},
		{
			name:          "Combination: active true and impact 1",
			queryParams:   map[string]string{"active": "true", "impact": "1"},
			expectedIDs:   []int{activeID},
			expectedCount: 1,
		},
		{
			name:          "Combination: component_id 3 and system true",
			queryParams:   map[string]string{"components": "3", "system": "true"},
			expectedIDs:   []int{outageID},
			expectedCount: 1,
		},
		{
			name:          "Date range: resolved window",
			queryParams:   map[string]string{"start_date": startDateFilter, "end_date": endDateFilter},
			expectedIDs:   []int{resolvedID},
			expectedCount: 1,
		},
		{
			name:          "Filter by impact 3 (outage)",
			queryParams:   map[string]string{"impact": "3"},
			expectedIDs:   []int{outageID},
			expectedCount: 1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req, _ := http.NewRequest(http.MethodGet, v2IncidentsEndpoint, nil)
			req.Header.Set("Authorization", "Bearer "+adminToken)

			q := req.URL.Query()
			for k, v := range tc.queryParams {
				q.Add(k, v)
			}
			req.URL.RawQuery = q.Encode()

			r.ServeHTTP(w, req)

			assert.Equal(t, http.StatusOK, w.Code, "Unexpected status code for: "+tc.name)

			var responseData V2IncidentsListResponse
			err := json.Unmarshal(w.Body.Bytes(), &responseData)
			require.NoError(t, err, "Failed to unmarshal response for: "+tc.name)

			actualIncidents := responseData.Data
			assert.Len(t, actualIncidents, tc.expectedCount, "Unexpected number of incidents for: "+tc.name)

			// When incidents are found or not, the message field should ideally be empty.
			assert.Empty(t, responseData.Message, "Expected no message for: "+tc.name)

			actualIDs := make([]int, len(actualIncidents))
			for i, inc := range actualIncidents {
				actualIDs[i] = inc.ID
			}
			assert.ElementsMatch(t, tc.expectedIDs, actualIDs, "Unexpected incident IDs for: "+tc.name)
		})
	}
}

func TestV2PostMaintenanceHandler(t *testing.T) {
	t.Log("start to test maintenance creation for /v2/incidents")
	r, _ := initTests(t)

	t.Log("create a maintenance")

	components := []int{1, 2}
	impact := 0
	title := "Test maintenance incident for dcs"
	description := "Test maintenance description"
	startDate := time.Now().Add(time.Hour * 1).UTC()
	endDate := time.Now().Add(time.Hour * 2).UTC()
	system := false

	incidentCreateData := v2.IncidentData{
		Title:        title,
		Description:  description,
		ContactEmail: "test@example.com",
		Impact:       &impact,
		Components:   components,
		StartDate:    startDate,
		EndDate:      &endDate,
		System:       &system,
		Type:         event.TypeMaintenance,
	}

	result := v2CreateIncident(t, r, &incidentCreateData)
	require.NotNil(t, result, "v2CreateIncident returned nil")
	assert.Len(t, incidentCreateData.Components, len(result.Result))

	incident := v2GetIncident(t, r, result.Result[0].IncidentID)
	assert.Equal(t, incidentCreateData.StartDate.Truncate(time.Microsecond), incident.StartDate)
	assert.Equal(t, incidentCreateData.EndDate.Truncate(time.Microsecond), *incident.EndDate)
	assert.Equal(t, title, incident.Title)
	assert.Equal(t, impact, *incident.Impact)
	assert.Equal(t, system, *incident.System)
	assert.Equal(t, description, incident.Description)
	assert.NotNil(t, incident.Updates)
	assert.Equal(t, event.MaintenancePlanned, incident.Updates[0].Status)
}

func TestV2PostInfoWithExistingEventsHandler(t *testing.T) {
	t.Log("start to test 'info' incident creation when an 'incident' and a 'maintenance' for the same component already exist")
	r, _ := initTests(t)

	// 1. Preparation: Close any existing open incidents for a clean state.
	incidentsBeforeTest := v2GetIncidents(t, r)
	for _, inc := range incidentsBeforeTest {
		if inc.EndDate == nil {
			t.Logf("Closing pre-existing open incident ID: %d for test setup", inc.ID)
			endDate := inc.StartDate.Add(time.Hour * 1).UTC()
			inc.EndDate = &endDate
			v2PatchIncident(t, r, inc)
		}
	}

	// 2. Create an active "incident" type event.
	t.Log("Create an active 'incident' type event")
	incidentComponentID := 1
	initialIncidentImpact := 1
	initialIncidentTitle := "Initial incident event"
	initialIncidentDescription := "Description for the initial incident"
	initialIncidentStartDate := time.Now().AddDate(0, 0, -1).UTC()
	initialIncidentSystem := false

	initialIncidentData := v2.IncidentData{
		Title:       initialIncidentTitle,
		Description: initialIncidentDescription,
		Impact:      &initialIncidentImpact,
		Components:  []int{incidentComponentID},
		StartDate:   initialIncidentStartDate,
		System:      &initialIncidentSystem,
		Type:        event.TypeIncident,
	}

	initialIncidentResp := v2CreateIncident(t, r, &initialIncidentData)
	require.NotNil(t, initialIncidentResp, "Failed to create initial incident")
	require.Len(t, initialIncidentResp.Result, 1, "Initial incident response should have one result")
	initialIncidentID := initialIncidentResp.Result[0].IncidentID
	t.Logf("Created active 'incident' with ID: %d for component %d", initialIncidentID, incidentComponentID)

	// 3. Create a planned "maintenance" type event for the SAME component.
	t.Log("Step 3: Create a planned 'maintenance' type event for the same component")
	maintenanceImpact := 0 // Maintenance impact is typically 0
	maintenanceTitle := "Planned Maintenance for Component"
	maintenanceDescription := "Description for the maintenance event"
	maintenanceStartDate := time.Now().AddDate(0, 0, 7).UTC()
	maintenanceEndDate := time.Now().AddDate(0, 0, 7).Add(time.Hour * 2).UTC()
	maintenanceSystem := false

	maintenanceIncidentData := v2.IncidentData{
		Title:        maintenanceTitle,
		Description:  maintenanceDescription,
		ContactEmail: "test@example.com",
		Impact:       &maintenanceImpact,
		Components:   []int{incidentComponentID},
		StartDate:    maintenanceStartDate,
		EndDate:      &maintenanceEndDate,
		System:       &maintenanceSystem,
		Type:         "maintenance",
	}

	maintenanceIncidentResp := v2CreateIncident(t, r, &maintenanceIncidentData)
	require.NotNil(t, maintenanceIncidentResp, "Failed to create 'maintenance' incident")
	require.Len(t, maintenanceIncidentResp.Result, 1, "'Maintenance' incident response should have one result")
	maintenanceIncidentID := maintenanceIncidentResp.Result[0].IncidentID
	t.Logf("Created planned 'maintenance' with ID: %d for component %d, starting at %s", maintenanceIncidentID, incidentComponentID, maintenanceStartDate)

	// 4. Create a new "info" type event for the SAME component.
	t.Log("Step 4: Create a new 'info' type event for the same component")
	infoImpact := 0
	infoTitle := "Informational Update During IsActive Incident and Before Maintenance"
	infoDescription := "Description for the info event"
	infoStartDate := time.Now().Add(time.Minute * -30).UTC()
	infoEndDate := time.Now().Add(time.Minute * 30).UTC()
	infoSystem := false

	infoIncidentData := v2.IncidentData{
		Title:       infoTitle,
		Description: infoDescription,
		Impact:      &infoImpact,
		Components:  []int{incidentComponentID},
		StartDate:   infoStartDate,
		EndDate:     &infoEndDate,
		System:      &infoSystem,
		Type:        "info",
	}

	infoIncidentResp := v2CreateIncident(t, r, &infoIncidentData)
	require.NotNil(t, infoIncidentResp, "Failed to create 'info' incident")
	require.Len(t, infoIncidentResp.Result, 1, "'Info' incident response should have one result")
	infoIncidentID := infoIncidentResp.Result[0].IncidentID
	t.Logf("Created 'info' incident with ID: %d for component %d", infoIncidentID, incidentComponentID)

	// Assertions.
	t.Log("Step 5: Perform assertions")
	assert.NotEqual(t, initialIncidentID, infoIncidentID, "Info incident should have a new, distinct ID")
	assert.NotEqual(t, maintenanceIncidentID, infoIncidentID, "Info incident should have a new, distinct ID from maintenance")

	// Verify the 'info' incident.
	fetchedInfoIncident := v2GetIncident(t, r, infoIncidentID)
	assert.Equal(t, infoTitle, fetchedInfoIncident.Title)
	assert.Equal(t, infoDescription, fetchedInfoIncident.Description)
	assert.Equal(t, event.TypeInformation, fetchedInfoIncident.Type)
	assert.Equal(t, infoImpact, *fetchedInfoIncident.Impact)
	assert.Contains(t, fetchedInfoIncident.Components, incidentComponentID)
	require.NotNil(t, fetchedInfoIncident.EndDate)
	assert.True(t, infoEndDate.Truncate(time.Second).Equal(fetchedInfoIncident.EndDate.Truncate(time.Second)))

	// Verify the initial 'incident' event is still open.
	fetchedInitialIncident := v2GetIncident(t, r, initialIncidentID)
	assert.Equal(t, initialIncidentTitle, fetchedInitialIncident.Title)
	assert.Equal(t, initialIncidentDescription, fetchedInitialIncident.Description)
	assert.Equal(t, event.TypeIncident, fetchedInitialIncident.Type)
	assert.Nil(t, fetchedInitialIncident.EndDate, "Initial 'incident' event should still be open")
	assert.Contains(t, fetchedInitialIncident.Components, incidentComponentID, "Initial 'incident' should still have its component")
	assert.Len(t, fetchedInitialIncident.Components, 1, "Initial 'incident' should only have its original component")

	// Verify the planned 'maintenance' event is still scheduled.
	fetchedMaintenanceIncident := v2GetIncident(t, r, maintenanceIncidentID)
	assert.Equal(t, maintenanceTitle, fetchedMaintenanceIncident.Title)
	assert.Equal(t, maintenanceDescription, fetchedMaintenanceIncident.Description)
	assert.Equal(t, event.TypeMaintenance, fetchedMaintenanceIncident.Type)
	require.NotNil(t, fetchedMaintenanceIncident.EndDate, "Maintenance event should have an end date")
	assert.True(t, maintenanceEndDate.Truncate(time.Second).Equal(fetchedMaintenanceIncident.EndDate.Truncate(time.Second)), "Maintenance end date mismatch")
	assert.Contains(t, fetchedMaintenanceIncident.Components, incidentComponentID, "Maintenance event should still have its component")
	assert.Len(t, fetchedMaintenanceIncident.Components, 1, "Maintenance event should only have its original component")
}

func TestV2GetComponentsAvailability(t *testing.T) {
	truncateIncidents(t)
	t.Logf("start to test GET %s", v2AvailabilityEndpoint)
	r, _ := initTests(t)

	// Incident preparation
	t.Log("create an incident")

	components := []int{7}
	impact := 3
	system := false
	now := time.Now().UTC()

	// Use relative dates within the 12-month availability window.
	// Pick 3 months ago as anchor; calculate midpoints to get exactly 50% downtime per month.
	month1Start := time.Date(now.Year(), now.Month()-3, 1, 0, 0, 0, 0, time.UTC)
	month1Days := time.Date(month1Start.Year(), month1Start.Month()+1, 1, 0, 0, 0, 0, time.UTC).Sub(month1Start)
	month1End := month1Start.Add(month1Days / 2) // exactly half the month

	month2Start := time.Date(now.Year(), now.Month()-2, 1, 0, 0, 0, 0, time.UTC)
	month2Days := time.Date(month2Start.Year(), month2Start.Month()+1, 1, 0, 0, 0, 0, time.UTC).Sub(month2Start)
	month2Mid := month2Start.Add(month2Days / 2)

	month3Start := time.Date(now.Year(), now.Month()-1, 1, 0, 0, 0, 0, time.UTC)
	month3Days := time.Date(month3Start.Year(), month3Start.Month()+1, 1, 0, 0, 0, 0, time.UTC).Sub(month3Start)
	month3Mid := month3Start.Add(month3Days / 2)

	// Incident N1: covers exactly first half of month (now-3)
	incidentCreateDataN1 := v2.IncidentData{
		Title:      "Test incident for dns N1",
		Impact:     &impact,
		Components: components,
		StartDate:  month1Start,
		EndDate:    nil,
		System:     &system,
		Type:       event.TypeIncident,
	}

	resultN1 := v2CreateIncident(t, r, &incidentCreateDataN1)
	require.NotNil(t, resultN1, "v2CreateIncident returned nil")
	assert.Len(t, resultN1.Result, len(incidentCreateDataN1.Components))

	incidentN1 := v2GetIncident(t, r, resultN1.Result[0].IncidentID)
	incidentN1.EndDate = &month1End
	v2PatchIncident(t, r, incidentN1)
	t.Logf("Incident N1 patched: %v - %v", month1Start, month1End)

	// Incident N2: covers second half of month (now-2) + first half of month (now-1)
	incidentCreateDataN2 := v2.IncidentData{
		Title:      "Test incident for dns N2",
		Impact:     &impact,
		Components: components,
		StartDate:  month2Mid,
		EndDate:    nil,
		System:     &system,
		Type:       event.TypeIncident,
	}

	resultN2 := v2CreateIncident(t, r, &incidentCreateDataN2)
	require.NotNil(t, resultN2, "v2CreateIncident returned nil")
	assert.Len(t, resultN2.Result, len(incidentCreateDataN2.Components))

	incidentN2 := v2GetIncident(t, r, resultN2.Result[0].IncidentID)
	incidentN2.EndDate = &month3Mid
	v2PatchIncident(t, r, incidentN2)
	t.Logf("Incident N2 patched: %v - %v", month2Mid, month3Mid)

	// Test case 1: Successful availability listing
	t.Log("Test case 1: List availability successfully")
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, v2AvailabilityEndpoint, nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var availability struct {
		Data []v2.ComponentAvailability `json:"data"`
	}
	err := json.Unmarshal(w.Body.Bytes(), &availability)
	require.NoError(t, err)
	assert.NotEmpty(t, availability)

	// Test case 2: Check if the availability data is correct
	// All 3 target months should have exactly 50% availability
	targetMonths := map[int]bool{
		int(month1Start.Month()): true,
		int(month2Start.Month()): true,
		int(month3Start.Month()): true,
	}

	for _, compAvail := range availability.Data {
		if compAvail.ID == 7 {
			checkComponentAvailability(t, compAvail, targetMonths)
		}
	}
}

func checkComponentAvailability(t *testing.T, compAvail v2.ComponentAvailability, targetMonths map[int]bool) {
	for _, avail := range compAvail.Availability {
		if _, ok := targetMonths[avail.Month]; ok {
			assert.InEpsilon(t, 50.00000, avail.Percentage, 0.00001,
				"Availability percentage should be 50% for the target months")
			// t.Logf("Availability for %v: %d-%d: %.2f%%", compAvail.Name, avail.Year, avail.Month, avail.Percentage)
		} else {
			assert.InEpsilon(t, 100.00000, avail.Percentage, 0.00001,
				"Availability percentage should be 100% for all months except the target months")
		}
	}
}

func TestV2PatchIncidentUpdateHandler(t *testing.T) {
	t.Log("start to test PATCH /v2/incidents/:incidentID/updates/:updateID")
	r, _ := initTests(t)

	// Clean up database before test to ensure a clean state for this test case.
	truncateIncidents(t)

	components := []int{1}
	impact := 1
	title := "Incident for testing update patch"
	startDate := time.Now().UTC()
	system := false
	incidentCreateData := v2.IncidentData{
		Title:      title,
		Impact:     &impact,
		Components: components,
		StartDate:  startDate,
		System:     &system,
		Type:       event.TypeIncident,
	}

	createResp := v2CreateIncident(t, r, &incidentCreateData)
	require.NotNil(t, createResp, "Failed to create incident for test")
	require.Len(t, createResp.Result, 1)
	incidentID := createResp.Result[0].IncidentID

	// The created incident has one update with index 0
	initialIncident := v2GetIncident(t, r, incidentID)
	require.Len(t, initialIncident.Updates, 1)

	testCases := []struct {
		name           string
		incidentID     int
		updateIndex    int
		body           string
		expectedStatus int
		expectedBody   string
		checkResponse  func(t *testing.T, body []byte)
	}{
		{
			name:           "Successful update",
			incidentID:     incidentID,
			updateIndex:    0,
			body:           `{"text": "The text of this update has been successfully changed."}`,
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, body []byte) {
				var update v2.EventUpdateData
				err := json.Unmarshal(body, &update)
				require.NoError(t, err)
				assert.Equal(t, 0, update.ID)
				assert.Equal(t, "The text of this update has been successfully changed.", update.Text)
				assert.Equal(t, event.IncidentDetected, update.Status)
			},
		},
		{
			name:           "Incident not found",
			incidentID:     99999,
			updateIndex:    0,
			body:           `{"text": "This should fail."}`,
			expectedStatus: http.StatusNotFound,
			expectedBody:   fmt.Sprintf(`{"errMsg":"%s"}`, apiErrors.ErrIncidentDSNotExist),
		},
		{
			name:           "Update index not found",
			incidentID:     incidentID,
			updateIndex:    99,
			body:           `{"text": "This should also fail."}`,
			expectedStatus: http.StatusNotFound,
			expectedBody:   fmt.Sprintf(`{"errMsg":"%s"}`, apiErrors.ErrUpdateDSNotExist),
		},
		{
			name:           "Invalid update index (negative)",
			incidentID:     incidentID,
			updateIndex:    -1,
			body:           `{"text": "This should also fail."}`,
			expectedStatus: http.StatusBadRequest,
			expectedBody:   (`{"errMsg":"Key: 'updateData.UpdateID' Error:Field validation for 'UpdateID' failed on the 'gte' tag"}`),
		},
		{
			name:           "Empty text in body",
			incidentID:     incidentID,
			updateIndex:    0,
			body:           `{"text": ""}`,
			expectedStatus: http.StatusBadRequest,
			expectedBody:   (`{"errMsg":"Key: 'PatchEventUpdateData.Text' Error:Field validation for 'Text' failed on the 'required' tag"}`),
		},
		{
			name:           "Missing text field in body",
			incidentID:     incidentID,
			updateIndex:    0,
			body:           `{}`,
			expectedStatus: http.StatusBadRequest,
			expectedBody:   (`{"errMsg":"Key: 'PatchEventUpdateData.Text' Error:Field validation for 'Text' failed on the 'required' tag"}`),
		},
		{
			name:           "ff",
			incidentID:     incidentID,
			updateIndex:    0,
			body:           `{"text": "invalid json`,
			expectedStatus: http.StatusBadRequest,
			expectedBody:   `{"errMsg":"unexpected EOF"}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			url := fmt.Sprintf("/v2/incidents/%d/updates/%d", tc.incidentID, tc.updateIndex)
			req, _ := http.NewRequest(http.MethodPatch, url, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+adminToken)

			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			assert.Equal(t, tc.expectedStatus, w.Code)

			if tc.checkResponse != nil {
				tc.checkResponse(t, w.Body.Bytes())
			}

			if tc.expectedBody != "" {
				assert.JSONEq(t, tc.expectedBody, w.Body.String())
			}
		})
	}
}
