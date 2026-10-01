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

	v2 "github.com/stackmon/otc-status-dashboard/internal/api/v2"
	"github.com/stackmon/otc-status-dashboard/internal/event"
)

// V2EventsListResponse defines the expected structure for the GET /v2/events endpoint.
type V2EventsListResponse struct {
	Data       []*v2.Incident `json:"data"`
	Message    string         `json:"message,omitempty"`
	Pagination *struct {
		PageIndex      int `json:"pageIndex"`
		RecordsPerPage int `json:"recordsPerPage"`
		TotalRecords   int `json:"totalRecords"`
		TotalPages     int `json:"totalPages"`
	} `json:"pagination,omitempty"`
}

func TestV2PostEventsHandlerNegative(t *testing.T) {
	t.Log("start to test incident creation and check json data for /v2/events")
	r := initTests(t)

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
  "start_date":"2031-11-25T09:32:14.075Z",
  "end_date":"2031-11-25T10:32:14.075Z",
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
		req, _ := http.NewRequest(http.MethodPost, v2EventsEndpoint, strings.NewReader(c.JSON))
		req.Header.Set("Authorization", "Bearer "+adminToken)
		r.ServeHTTP(w, req)

		assert.Equal(t, c.ExpectedCode, w.Code)
		assert.Equal(t, c.Expected, w.Body.String())
	}
}

func TestV2PostEventsHandler(t *testing.T) {
	t.Log("start to test incident creation for /v2/events")
	r := initTests(t)

	t.Log("check if all incidents have end date, if not, set it to start date + 1ms")
	incidents := v2GetEvents(t, r)
	for _, inc := range incidents {
		if inc.EndDate == nil {
			endDate := inc.StartDate.Add(time.Millisecond * 1).UTC()
			inc.EndDate = &endDate
			v2PatchEvent(t, r, inc)
		}
		if inc.Type == event.TypeMaintenance {
			t.Log("the component is maintenance, cancel it")
			v2PatchEvent(t, r, inc, event.MaintenanceCancelled)
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

	result := v2CreateEvent(t, r, &incidentCreateData)
	require.NotNil(t, result, "v2CreateEvent returned nil")

	assert.Len(t, result.Result, len(incidentCreateData.Components))
	assert.Empty(t, result.Result[0].Error)
	assert.Empty(t, result.Result[1].Error)
	assert.Equal(t, len(incidents)+1, result.Result[0].IncidentID)
	assert.Equal(t, len(incidents)+1, result.Result[1].IncidentID)

	t.Log("check created incident data, incident id: ", result.Result[0].IncidentID)
	incident := v2GetEvent(t, r, result.Result[0].IncidentID)
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
	result = v2CreateEvent(t, r, &incidentCreateData)
	require.NotNil(t, result, "v2CreateEvent returned nil")
	assert.Equal(t, len(incidents)+2, result.Result[0].IncidentID)
	assert.Equal(t, len(incidents)+2, result.Result[1].IncidentID)

	oldIncident := v2GetEvent(t, r, result.Result[0].IncidentID-1)
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

	incidentN3 := v2GetEvent(t, r, result.Result[0].IncidentID)
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

	result = v2CreateEvent(t, r, &incidentCreateData)
	require.NotNil(t, result, "v2CreateEvent returned nil")
	assert.Equal(t, len(incidents)+3, result.Result[0].IncidentID)
	assert.Equal(t, len(incidents)+3, result.Result[1].IncidentID)

	maintenanceIncident := v2GetEvent(t, r, result.Result[0].IncidentID)
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

	incidentN3 = v2GetEvent(t, r, result.Result[0].IncidentID-1)
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
	result = v2CreateEvent(t, r, &incidentCreateData)
	require.NotNil(t, result, "v2CreateEvent returned nil")
	// ID should be incidentN3.ID + 2 (maintenance + this new incident)
	expectedID := incidentN3.ID + 2
	assert.Equal(t, expectedID, result.Result[0].IncidentID)
	assert.Equal(t, 3, result.Result[0].ComponentID)
}

func TestV2PatchEventHandlerNegative(t *testing.T) {
	t.Log("start to test negative cases for incident patching and check json data for /v2/events/42")
	r := initTests(t)

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

	resp := v2CreateEvent(t, r, &incidentCreateData)
	require.NotNil(t, resp, "v2CreateEvent returned nil")
	incID10 := resp.Result[0].IncidentID

	// New events are created with version=1 by default
	version := 1

	type testCase struct {
		ExpectedCode int
		Expected     string
		JSON         string
	}

	jsWrongOpenedStatus := fmt.Sprintf(`{
"title": "OpenStack Upgrade in regions EU-DE/EU-NL",
 "impact": 1,
 "message": "Any message why the incident was updated.",
 "status": "in_progres",
 "update_date": "2024-12-11T14:46:03.877Z",
 "start_date": "2024-12-11T14:46:03.877Z",
 "end_date": "2024-12-11T14:46:03.877Z",
"type": "incident",
"version": %d
}`, version)
	jsWrongOpenedStartDate := fmt.Sprintf(`{
 "impact": 1,
 "message": "Any message why the incident was updated.",
 "status": "analysing",
 "update_date": "2024-12-11T14:46:03.877Z",
 "start_date": "2024-12-11T14:46:03.877Z",
 "type": "incident",
 "version": %d
}`, version)
	jsWrongOpenedStatusForChangingImpact := fmt.Sprintf(`{
"impact": 0,
"message": "Any message why the event was updated.",
"status": "analysing",
"update_date": "2024-12-11T14:46:03.877Z",
"type": "maintenance",
"version": %d
}`, version)
	jsWrongOpenedMaintenanceImpact := fmt.Sprintf(`{
 "impact": 0,
 "message": "Any message why the event was updated.",
 "status": "impact changed",
 "update_date": "2024-12-11T14:46:03.877Z",
 "type": "maintenance",
 "version": %d
}`, version)
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

		url := fmt.Sprintf("/v2/events/%d", incID10)

		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodPatch, url, strings.NewReader(c.JSON))
		req.Header.Set("Authorization", "Bearer "+adminToken)
		r.ServeHTTP(w, req)

		assert.Equal(t, c.ExpectedCode, w.Code)
		assert.Equal(t, c.Expected, w.Body.String())
	}
}

func TestV2PatchEventHandler(t *testing.T) {
	t.Log("start to test incident patching")
	r := initTests(t)

	components := []int{1}
	impact := 1
	title := "Test incident for patching test"
	description := "Test case Patch. Any description for incident"
	startDate := time.Now().AddDate(0, 0, -2).UTC()
	system := false

	internalPatch := func(id int, p *v2.PatchIncidentData) *v2.Incident {
		d, err := json.Marshal(p)
		require.NoError(t, err)

		url := fmt.Sprintf("/v2/events/%d", id)
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodPatch, url, bytes.NewReader(d))
		req.Header.Set("Authorization", "Bearer "+adminToken)

		r.ServeHTTP(w, req)
		assert.Equal(t, 200, w.Code)

		inc := &v2.Incident{}
		err = json.Unmarshal(w.Body.Bytes(), inc)
		require.NoError(t, err)
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

	resp := v2CreateEvent(t, r, &incidentCreateData)
	require.NotNil(t, resp, "v2CreateEvent returned nil")
	incID := resp.Result[0].IncidentID

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
	assert.Nil(t, inc.Version, "Version must not be exposed for non-maintenance events")

	newImpact := 2
	t.Logf("patching incident impact, from %d to %d", impact, newImpact)

	pData.Impact = &newImpact
	pData.Status = event.IncidentImpactChanged

	inc = internalPatch(incID, &pData)
	assert.Equal(t, newImpact, *inc.Impact)
	assert.Nil(t, inc.Version, "Version must not be exposed for non-maintenance events")

	t.Logf("close incident")
	pData.Status = event.IncidentResolved
	updateDate := time.Now().UTC()
	pData.UpdateDate = updateDate

	inc = internalPatch(incID, &pData)
	require.NotNil(t, inc.EndDate)
	assert.Equal(t, updateDate.Truncate(time.Microsecond), inc.EndDate.Truncate(time.Microsecond))
	assert.Nil(t, inc.Version, "Version must not be exposed for non-maintenance events")

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
	assert.Nil(t, inc.Version, "Version must not be exposed for non-maintenance events")

	t.Logf("reopen closed incident")

	pData.Status = event.IncidentReopened
	pData.StartDate = nil
	pData.EndDate = nil
	inc = internalPatch(incID, &pData)
	assert.Nil(t, inc.EndDate)
	assert.Nil(t, inc.Version, "Version must not be exposed for non-maintenance events")

	t.Logf("final close the test incident")

	pData.Status = event.IncidentResolved
	inc = internalPatch(incID, &pData)
	assert.Equal(t, event.IncidentResolved, inc.Status)
	assert.NotNil(t, inc.EndDate)
}

func TestV2PostEventExtractHandler(t *testing.T) {
	t.Log("start to test component extraction from incident for the endpoint /v2/events/42/extract")
	r := initTests(t)

	t.Log("check if all incidents have end date, if not, set it to start date + 1ms")
	incidents := v2GetEvents(t, r)
	for _, inc := range incidents {
		if inc.EndDate == nil {
			endDate := inc.StartDate.Add(time.Millisecond * 1).UTC()
			inc.EndDate = &endDate
			v2PatchEvent(t, r, inc)
		}
		if inc.Type == event.TypeMaintenance {
			t.Log("the component is maintenance, cancel it")
			v2PatchEvent(t, r, inc, event.MaintenanceCancelled)
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
	result := v2CreateEvent(t, r, &incidentCreateData)
	require.NotNil(t, result, "v2CreateEvent returned nil")

	t.Logf("prepare to extract components: %d from incident %d", 2, result.Result[0].IncidentID)
	type IncidentData struct {
		Components []int `json:"components"`
	}
	movedComponents := IncidentData{Components: []int{2}}
	data, err := json.Marshal(movedComponents)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, v2EventsEndpoint+fmt.Sprintf("/%d/extract", result.Result[0].IncidentID), bytes.NewReader(data))
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
	createdInc := v2GetEvent(t, r, result.Result[0].IncidentID)
	assert.Equal(t, "The incident is detected.", createdInc.Updates[0].Text)
	assert.Equal(t, fmt.Sprintf("Cloud Container Engine (Container, EU-NL, cce) moved to <a href='/incidents/%d'>Test component extraction for component dcs</a>", newInc.ID), createdInc.Updates[1].Text)

	t.Log("start negative case, try to extract all components from the incident, should return error")
	// start negative case
	movedComponents = IncidentData{Components: []int{1}}
	data, err = json.Marshal(movedComponents)
	require.NoError(t, err)

	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodPost, v2EventsEndpoint+fmt.Sprintf("/%d/extract", result.Result[0].IncidentID), bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.JSONEq(t, `{"errMsg":"can not move all components to the new incident, keep at least one"}`, w.Body.String())
}

func v2CreateEvent(t *testing.T, r *gin.Engine, inc *v2.IncidentData) *v2.PostIncidentResp {
	t.Helper()

	data, err := json.Marshal(inc)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, v2EventsEndpoint, bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Logf("v2CreateEvent error: %s", w.Body.String())
		return nil
	}

	assert.Equal(t, http.StatusOK, w.Code)

	respCreated := &v2.PostIncidentResp{}
	err = json.Unmarshal(w.Body.Bytes(), respCreated)
	require.NoError(t, err)

	return respCreated
}

func v2GetEvent(t *testing.T, r *gin.Engine, id int) *v2.Incident {
	t.Helper()
	url := fmt.Sprintf("/v2/events/%d", id)
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

func v2GetEvents(t *testing.T, r *gin.Engine) []*v2.Incident {
	t.Helper()
	url := "/v2/events?limit=50&page=1"
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Response now includes pagination
	type response struct {
		Data       []*v2.Incident         `json:"data"`
		Pagination map[string]interface{} `json:"pagination"`
	}

	resp := &response{}
	err := json.Unmarshal(w.Body.Bytes(), resp)
	require.NoError(t, err)

	return resp.Data
}

func v2PatchEvent(t *testing.T, r *gin.Engine, inc *v2.Incident, status ...event.Status) {
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

	url := fmt.Sprintf("/v2/events/%d", inc.ID)
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

func TestV2GetEventsFilteredHandler(t *testing.T) {
	t.Log("start to test GET /v2/events with filters and pagination")
	r := initTests(t)

	// First, get all incidents to understand current state
	allIncidents := v2GetEvents(t, r)
	allIDs := make([]int, len(allIncidents))
	for i, inc := range allIncidents {
		allIDs[i] = inc.ID
	}
	totalCount := len(allIncidents)
	t.Logf("Total incidents in DB: %d, IDs: %v", totalCount, allIDs)

	// Build dynamic expectations based on actual data
	// Incident from dump_test.sql: ID=1, impact=1, system=true, component=1, start_date=2025-05-22

	// Filter incidents by impact=1
	var impact1IDs []int
	for _, inc := range allIncidents {
		if inc.Impact != nil && *inc.Impact == 1 {
			impact1IDs = append(impact1IDs, inc.ID)
		}
	}

	// Filter incidents by impact=2
	var impact2IDs []int
	for _, inc := range allIncidents {
		if inc.Impact != nil && *inc.Impact == 2 {
			impact2IDs = append(impact2IDs, inc.ID)
		}
	}

	// Filter incidents by system=true
	var systemTrueIDs []int
	for _, inc := range allIncidents {
		if inc.System != nil && *inc.System {
			systemTrueIDs = append(systemTrueIDs, inc.ID)
		}
	}

	// Filter incidents by system=false
	var systemFalseIDs []int
	for _, inc := range allIncidents {
		if inc.System != nil && !*inc.System {
			systemFalseIDs = append(systemFalseIDs, inc.ID)
		}
	}

	type filterTestCase struct {
		name          string
		queryParams   map[string]string
		expectedIDs   []int
		expectedCount int
	}

	testCases := []filterTestCase{
		{
			name:          "No filters",
			queryParams:   nil,
			expectedIDs:   allIDs,
			expectedCount: totalCount,
		},
		{
			name:          "Filter by impact minor (1)",
			queryParams:   map[string]string{"impact": "1"},
			expectedIDs:   impact1IDs,
			expectedCount: len(impact1IDs),
		},
		{
			name:          "Filter by impact major (2)",
			queryParams:   map[string]string{"impact": "2"},
			expectedIDs:   impact2IDs,
			expectedCount: len(impact2IDs),
		},
		{
			name:          "Filter by non-existent component_id 99",
			queryParams:   map[string]string{"components": "99"},
			expectedIDs:   []int{},
			expectedCount: 0,
		},
		{
			name:          "Filter by system true",
			queryParams:   map[string]string{"system": "true"},
			expectedIDs:   systemTrueIDs,
			expectedCount: len(systemTrueIDs),
		},
		{
			name:          "Filter by system false",
			queryParams:   map[string]string{"system": "false"},
			expectedIDs:   systemFalseIDs,
			expectedCount: len(systemFalseIDs),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req, _ := http.NewRequest(http.MethodGet, v2EventsEndpoint, nil)
			req.Header.Set("Authorization", "Bearer "+adminToken)

			q := req.URL.Query()
			// Add pagination to get all results in one page
			q.Add("limit", "50")
			q.Add("page", "1")
			for k, v := range tc.queryParams {
				q.Add(k, v)
			}
			req.URL.RawQuery = q.Encode()

			r.ServeHTTP(w, req)

			assert.Equal(t, http.StatusOK, w.Code, "Unexpected status code for: "+tc.name)

			// For paginated events endpoint
			var responseData V2EventsListResponse
			err := json.Unmarshal(w.Body.Bytes(), &responseData)
			require.NoError(t, err, "Failed to unmarshal response for: "+tc.name)

			actualIncidents := responseData.Data
			assert.Len(t, actualIncidents, tc.expectedCount, "Unexpected number of events for: "+tc.name)

			// Verify pagination metadata exists when there are results
			if tc.expectedCount > 0 {
				require.NotNil(t, responseData.Pagination, "Expected pagination object for: "+tc.name)
				// Verify total records matches expected count
				assert.Equal(t, tc.expectedCount, responseData.Pagination.TotalRecords, "Unexpected total records for: "+tc.name)
			}

			actualIDs := make([]int, len(actualIncidents))
			for i, inc := range actualIncidents {
				actualIDs[i] = inc.ID
			}
			assert.ElementsMatch(t, tc.expectedIDs, actualIDs, "Unexpected event IDs for: "+tc.name)
		})
	}
}

func TestV2GetEventsHandler(t *testing.T) {
	t.Logf("start to test GET %s with pagination", v2EventsEndpoint)
	r := initTests(t)

	type V2EventsListResponseLocal struct {
		Data       []*v2.Incident `json:"data"`
		Pagination struct {
			PageIndex      int `json:"pageIndex"`
			RecordsPerPage int `json:"recordsPerPage"`
			TotalRecords   int `json:"totalRecords"`
			TotalPages     int `json:"totalPages"`
		} `json:"pagination"`
	}

	// Get all incidents for better debugging from /v2/events endpoint
	allIncidents := v2GetEvents(t, r)
	t.Logf("Initial incidents in DB: %+v", len(allIncidents))
	totalIncidents := len(allIncidents)

	testCases := []struct {
		name               string
		queryParams        string
		expectedStatusCode int
		expectedLimit      int
		expectedPage       int
	}{
		{
			name:               "Default pagination",
			queryParams:        "",
			expectedStatusCode: http.StatusOK,
			expectedLimit:      50, // default limit
			expectedPage:       1,  // default page
		},
		{
			name:               "Pagination with limit 10, page 1",
			queryParams:        "?limit=10&page=1",
			expectedStatusCode: http.StatusOK,
			expectedLimit:      10,
			expectedPage:       1,
		},
		{
			name:               "Pagination with limit 10, page 2",
			queryParams:        "?limit=10&page=2",
			expectedStatusCode: http.StatusOK,
			expectedLimit:      10,
			expectedPage:       2,
		},
		{
			name:               "Pagination with limit 20, page 1",
			queryParams:        "?limit=20&page=1",
			expectedStatusCode: http.StatusOK,
			expectedLimit:      20,
			expectedPage:       1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req, _ := http.NewRequest(http.MethodGet, v2EventsEndpoint+tc.queryParams, nil)
			req.Header.Set("Authorization", "Bearer "+adminToken)
			r.ServeHTTP(w, req)

			assert.Equal(t, tc.expectedStatusCode, w.Code)

			var response V2EventsListResponseLocal
			err := json.Unmarshal(w.Body.Bytes(), &response)
			require.NoError(t, err)

			expectedItems := totalIncidents - (tc.expectedPage-1)*tc.expectedLimit
			if expectedItems < 0 {
				expectedItems = 0
			}
			if expectedItems > tc.expectedLimit {
				expectedItems = tc.expectedLimit
			}
			expectedPages := totalIncidents / tc.expectedLimit
			if totalIncidents%tc.expectedLimit != 0 {
				expectedPages++
			}
			if expectedPages == 0 {
				expectedPages = 1
			}

			assert.Len(t, response.Data, expectedItems)
			assert.Equal(t, totalIncidents, response.Pagination.TotalRecords)
			assert.Equal(t, expectedPages, response.Pagination.TotalPages)
			assert.Equal(t, tc.expectedLimit, response.Pagination.RecordsPerPage)
			assert.Equal(t, tc.expectedPage, response.Pagination.PageIndex)
		})
	}
}

func TestV2PostEventsMaintenanceHandler(t *testing.T) {
	t.Log("start to test maintenance creation for /v2/events")
	r := initTests(t)

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

	result := v2CreateEvent(t, r, &incidentCreateData)
	require.NotNil(t, result, "v2CreateEvent returned nil")
	assert.Len(t, incidentCreateData.Components, len(result.Result))

	incident := v2GetEvent(t, r, result.Result[0].IncidentID)
	assert.Equal(t, incidentCreateData.StartDate.Truncate(time.Microsecond), incident.StartDate)
	assert.Equal(t, incidentCreateData.EndDate.Truncate(time.Microsecond), *incident.EndDate)
	assert.Equal(t, title, incident.Title)
	assert.Equal(t, impact, *incident.Impact)
	assert.Equal(t, system, *incident.System)
	assert.Equal(t, description, incident.Description)
	assert.NotNil(t, incident.Updates)
	assert.Equal(t, event.MaintenancePlanned, incident.Updates[0].Status)
}

func TestV2PostEventsInfoWithExistingEventsHandler(t *testing.T) {
	t.Log("start to test 'info' incident creation when an 'incident' and a 'maintenance' for the same component already exist")
	r := initTests(t)

	// 1. Preparation: Close any existing open incidents for a clean state.
	incidentsBeforeTest := v2GetEvents(t, r)
	for _, inc := range incidentsBeforeTest {
		if inc.EndDate == nil {
			t.Logf("Closing pre-existing open incident ID: %d for test setup", inc.ID)
			endDate := inc.StartDate.Add(time.Hour * 1).UTC()
			inc.EndDate = &endDate
			v2PatchEvent(t, r, inc)
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

	initialIncidentResp := v2CreateEvent(t, r, &initialIncidentData)
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

	maintenanceIncidentResp := v2CreateEvent(t, r, &maintenanceIncidentData)
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

	infoIncidentResp := v2CreateEvent(t, r, &infoIncidentData)
	require.NotNil(t, infoIncidentResp, "Failed to create 'info' incident")
	require.Len(t, infoIncidentResp.Result, 1, "'Info' incident response should have one result")
	infoIncidentID := infoIncidentResp.Result[0].IncidentID
	t.Logf("Created 'info' incident with ID: %d for component %d", infoIncidentID, incidentComponentID)

	// Assertions.
	t.Log("Step 5: Perform assertions")
	assert.NotEqual(t, initialIncidentID, infoIncidentID, "Info incident should have a new, distinct ID")
	assert.NotEqual(t, maintenanceIncidentID, infoIncidentID, "Info incident should have a new, distinct ID from maintenance")

	// Verify the 'info' incident.
	fetchedInfoIncident := v2GetEvent(t, r, infoIncidentID)
	assert.Equal(t, infoTitle, fetchedInfoIncident.Title)
	assert.Equal(t, infoDescription, fetchedInfoIncident.Description)
	assert.Equal(t, event.TypeInformation, fetchedInfoIncident.Type)
	assert.Equal(t, infoImpact, *fetchedInfoIncident.Impact)
	assert.Contains(t, fetchedInfoIncident.Components, incidentComponentID)
	require.NotNil(t, fetchedInfoIncident.EndDate)
	assert.True(t, infoEndDate.Truncate(time.Second).Equal(fetchedInfoIncident.EndDate.Truncate(time.Second)))

	// Verify the initial 'incident' event is still open.
	fetchedInitialIncident := v2GetEvent(t, r, initialIncidentID)
	assert.Equal(t, initialIncidentTitle, fetchedInitialIncident.Title)
	assert.Equal(t, initialIncidentDescription, fetchedInitialIncident.Description)
	assert.Equal(t, event.TypeIncident, fetchedInitialIncident.Type)
	assert.Nil(t, fetchedInitialIncident.EndDate, "Initial 'incident' event should still be open")
	assert.Contains(t, fetchedInitialIncident.Components, incidentComponentID, "Initial 'incident' should still have its component")
	assert.Len(t, fetchedInitialIncident.Components, 1, "Initial 'incident' should only have its original component")

	// Verify the planned 'maintenance' event is still scheduled.
	fetchedMaintenanceIncident := v2GetEvent(t, r, maintenanceIncidentID)
	assert.Equal(t, maintenanceTitle, fetchedMaintenanceIncident.Title)
	assert.Equal(t, maintenanceDescription, fetchedMaintenanceIncident.Description)
	assert.Equal(t, event.TypeMaintenance, fetchedMaintenanceIncident.Type)
	require.NotNil(t, fetchedMaintenanceIncident.EndDate, "Maintenance event should have an end date")
	assert.True(t, maintenanceEndDate.Truncate(time.Second).Equal(fetchedMaintenanceIncident.EndDate.Truncate(time.Second)), "Maintenance end date mismatch")
	assert.Contains(t, fetchedMaintenanceIncident.Components, incidentComponentID, "Maintenance event should still have its component")
	assert.Len(t, fetchedMaintenanceIncident.Components, 1, "Maintenance event should only have its original component")
}

func TestV2PatchEventUpdateHandler(t *testing.T) {
	t.Log("start to test PATCH /v2/events/:incidentID/updates/:updateID")
	r := initTests(t)

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

	createResp := v2CreateEvent(t, r, &incidentCreateData)
	require.NotNil(t, createResp, "Failed to create incident for test")
	require.Len(t, createResp.Result, 1)
	incidentID := createResp.Result[0].IncidentID

	// The created incident has one update with index 0
	initialIncident := v2GetEvent(t, r, incidentID)
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
			expectedBody:   `{"errMsg":"event does not exist"}`,
		},
		{
			name:           "Update index not found",
			incidentID:     incidentID,
			updateIndex:    99,
			body:           `{"text": "This should also fail."}`,
			expectedStatus: http.StatusNotFound,
			expectedBody:   `{"errMsg":"update does not exist"}`,
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
			url := fmt.Sprintf("/v2/events/%d/updates/%d", tc.incidentID, tc.updateIndex)
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
