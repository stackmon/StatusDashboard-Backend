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

	v1 "github.com/stackmon/otc-status-dashboard/internal/api/v1"
	"github.com/stackmon/otc-status-dashboard/internal/db"
	"github.com/stackmon/otc-status-dashboard/internal/event"
)

func TestV1GetIncidentsHandler(t *testing.T) {
	t.Log("start to test GET /v1/incidents")
	r, _ := initTests(t)
	restoreFixtureIncident(t)

	var response = `[{"id":1,"text":"Closed incident without any update","impact":1,"start_date":"2025-05-22 10:12","end_date":"2025-05-22 11:12","updates":[{"status":"resolved","text":"close incident","timestamp":"2025-05-22 11:12"}]}]`

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/v1/incidents", nil)

	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Equal(t, response, w.Body.String())
}

func TestV1GetComponentsStatusHandler(t *testing.T) {
	t.Log("start to test GET /v1/component_status")
	r, _ := initTests(t)
	restoreFixtureIncident(t)

	var response = `[{"id":1,"attributes":[{"name":"region","value":"EU-DE"},{"name":"category","value":"Container"},{"name":"type","value":"cce"}],"name":"Cloud Container Engine","incidents":[{"id":1,"text":"Closed incident without any update","impact":1,"start_date":"2025-05-22 10:12","end_date":"2025-05-22 11:12","updates":[{"status":"resolved","text":"close incident","timestamp":"2025-05-22 11:12"}]}]},{"id":2,"attributes":[{"name":"region","value":"EU-NL"},{"name":"category","value":"Container"},{"name":"type","value":"cce"}],"name":"Cloud Container Engine","incidents":[]},{"id":3,"attributes":[{"name":"region","value":"EU-DE"},{"name":"category","value":"Compute"},{"name":"type","value":"ecs"}],"name":"Elastic Cloud Server","incidents":[]},{"id":4,"attributes":[{"name":"region","value":"EU-NL"},{"name":"category","value":"Compute"},{"name":"type","value":"ecs"}],"name":"Elastic Cloud Server","incidents":[]},{"id":5,"attributes":[{"name":"region","value":"EU-DE"},{"name":"category","value":"Database"},{"name":"type","value":"dcs"}],"name":"Distributed Cache Service","incidents":[]},{"id":6,"attributes":[{"name":"region","value":"EU-NL"},{"name":"category","value":"Database"},{"name":"type","value":"dcs"}],"name":"Distributed Cache Service","incidents":[]}]`

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/v1/component_status", nil)

	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Equal(t, response, w.Body.String())
}

func TestV1PostComponentsStatusHandlerNegative(t *testing.T) {
	t.Log("start to test incident creation and check json data for /v1/component_status")
	r, _ := initTests(t)

	type testCase struct {
		ExpectedCode int
		Expected     string
		JSON         string
	}

	testCases := map[string]*testCase{
		"negative testcase, region is invalid": {
			JSON:         `{"name":"Distributed Cache Service","text":"Incident","impact": 2,"attributes": [{"name":"region","value":"EU-NL123"}]}`,
			Expected:     `{"errMsg":"component does not exist"}`,
			ExpectedCode: 400,
		},
		"negative testcase, attribute region is missing": {
			JSON:         `{"name":"Distributed Cache Service","text":"Incident","impact": 2,"attributes": [{"name":"type","value":"dcs"}]}`,
			Expected:     `{"errMsg":"component attribute region is missing or invalid"}`,
			ExpectedCode: 400,
		},
		"negative testcase, component name is wrong": {
			JSON:         `{"name":"New Distributed Cache Service","text":"Incident","impact": 2,"attributes": [{"name":"region","value":"EU-NL"}]}`,
			Expected:     `{"errMsg":"component does not exist"}`,
			ExpectedCode: 400,
		},
	}

	for title, c := range testCases {
		t.Logf("start test case: %s\n", title)

		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodPost, "/v1/component_status", strings.NewReader(c.JSON))
		req.Header.Set("Authorization", "Bearer "+adminToken)
		r.ServeHTTP(w, req)

		assert.Equal(t, c.ExpectedCode, w.Code)
		assert.Equal(t, c.Expected, w.Body.String())
	}
}

func TestV1PostComponentsStatusHandler(t *testing.T) {
	t.Log("start to test incident creation, modification by /v1/component_status")
	r, dbIns := initTests(t)

	compNameDCS := "Distributed Cache Service"
	compAttrEUNL := []*v1.ComponentAttribute{{Name: "region", Value: "EU-NL"}}
	compNameCCE := "Cloud Container Engine"
	compNameECS := "Elastic Cloud Server"

	impact1 := 1
	title := "Test incident creation for api V1, main flow"
	mainTitle := title

	compCreateData := &v1.ComponentStatusPost{
		Name:       compNameDCS,
		Impact:     impact1,
		Text:       title,
		Attributes: compAttrEUNL,
	}

	t.Log("create an incident: ", compCreateData)
	incID2, _ := createIncidentByComponentV1(t, r, compCreateData)

	t.Log("create a new incident with the same component and the same impact, should get an error")
	_, body := createIncidentByComponentV1(t, r, compCreateData)
	confStruct := &v1.ConflictResponse{}
	err := json.Unmarshal(body, confStruct)
	require.NoError(t, err)

	checkConflictMsgV1(t, confStruct, incID2, title)

	t.Log("create a new incident with the same component and higher impact, should update the impact")
	compCreateData.Impact = 2
	incID2Existed, body := createIncidentByComponentV1(t, r, compCreateData)
	assert.Equal(t, incID2, incID2Existed)
	newInc := &v1.Incident{}
	err = json.Unmarshal(body, newInc)
	require.NoError(t, err)
	assert.Equal(t, compCreateData.Impact, *newInc.Impact)
	assert.Len(t, newInc.Updates, 1)
	assert.Equal(t, event.OutDatedSystem, newInc.Updates[0].Status)
	assert.Equal(t, "impact changed from 1 to 2", newInc.Updates[0].Text)
	assert.NotNil(t, newInc.Updates[0].Timestamp)

	t.Log("create a new incident with another component and same impact, should add component to the existed incident 2")
	compCreateData = &v1.ComponentStatusPost{
		Name:       compNameCCE,
		Impact:     2,
		Text:       title,
		Attributes: compAttrEUNL,
	}
	incID2Existed, body = createIncidentByComponentV1(t, r, compCreateData)
	assert.Equal(t, incID2, incID2Existed)
	newInc = &v1.Incident{}
	err = json.Unmarshal(body, newInc)
	require.NoError(t, err)
	//TODO: it's strange, that we can't check the count of components, fix it (maybe never, because this api is outdated)
	assert.Equal(t, compCreateData.Impact, *newInc.Impact)
	assert.Len(t, newInc.Updates, 2)
	for _, u := range newInc.Updates {
		if strings.HasPrefix(u.Text, "Cloud Container Engine") {
			assert.Equal(t, "Cloud Container Engine (Container, EU-NL, cce) added", u.Text)
			assert.Equal(t, event.OutDatedSystem, u.Status)
		}
	}

	t.Log("create a new incident with another component and higher impact, should create a new incident with higher impact")
	title = "Test incident creation for api V1 with higher impact, new incident with ECS"
	compCreateData = &v1.ComponentStatusPost{
		Name:       compNameECS,
		Impact:     3,
		Text:       title,
		Attributes: compAttrEUNL,
	}
	incID3, _ := createIncidentByComponentV1(t, r, compCreateData)
	assert.NotEqual(t, incID2, incID3)

	t.Log("close incident with impact 3, id: ", incID3)
	closeIncidentV1(t, r, dbIns, incID3)

	t.Log("start to test component movement between incidents")

	t.Log("extract component to the new incident with higher impact")
	title = "Test component extraction for api V1, move DCS from incident 2 to 4"
	compCreateData = &v1.ComponentStatusPost{
		Name:       compNameDCS,
		Impact:     3,
		Text:       title,
		Attributes: compAttrEUNL,
	}
	incID4, _ := createIncidentByComponentV1(t, r, compCreateData, mainTitle)
	assert.Equal(t, incID2, incID4)
	checkIncidentsDataAfterMoveV1(t, r, incID2)

	t.Log("extract component to the existed incident with higher impact, close the old incident")
	compCreateData = &v1.ComponentStatusPost{
		Name:       compNameCCE,
		Impact:     3,
		Text:       title,
		Attributes: compAttrEUNL,
	}
	incID4, _ = createIncidentByComponentV1(t, r, compCreateData, mainTitle)
	assert.Equal(t, incID2, incID4)
	checkIncidentsDataAfterMoveAndClosedIncidentV1(t, r, incID2)

	t.Log("decrease incident impact from 3 to 2")
	decreaseIncidentImpactV1(t, r, dbIns, incID4)

	t.Log("create an incident with another components with higher impact")

	attrEUDE := []*v1.ComponentAttribute{{Name: "region", Value: "EU-DE"}}
	title = "Test incident for moving component between incidents, move dcs_UE-NL from 4 to current"
	compCreateData = &v1.ComponentStatusPost{
		Name:       compNameDCS,
		Impact:     3,
		Text:       title,
		Attributes: attrEUDE,
	}
	incID5, _ := createIncidentByComponentV1(t, r, compCreateData)
	assert.NotEqual(t, incID4, incID5)
	moveTitle := title

	t.Log("Test moving component to the incident with the same impact")
	compCreateData.Name = compNameCCE
	activeIncidentID, _ := createIncidentByComponentV1(t, r, compCreateData)
	assert.Equal(t, incID5, activeIncidentID)

	incidents := getIncidentsAPIV1(t, r)
	assert.Len(t, incidents, 4)

	t.Log("send create request, should move component to the incident with higher impact")
	compCreateData = &v1.ComponentStatusPost{
		Name:       compNameDCS,
		Impact:     3,
		Text:       title,
		Attributes: compAttrEUNL,
	}
	_, _ = createIncidentByComponentV1(t, r, compCreateData)
	checkIncidentsDataAfterMovingComponentBetweenIncidentsV1(t, r, dbIns, incID2, incID5, mainTitle, moveTitle)
}

func TestV1MaintenancePreventCreation(t *testing.T) {
	t.Log("start to test incident creation, modification by /v1/component_status")
	r, dbIns := initTests(t)

	t.Log("close all incidents, to allow create a new maintenance")

	incidents := getIncidentsAPIV1(t, r)
	for _, inc := range incidents {
		if inc.ID == 1 {
			// skip the closed, predefined incident
			continue
		}
		closeIncidentV1(t, r, dbIns, inc.ID)
	}

	t.Log("create a maintenance for the component with DB access")

	mTitle := "Test maintenance for check incident creation prevention"
	startTime := time.Now().UTC().Truncate(time.Microsecond)
	endTime := startTime.Add(time.Minute * 2)
	impact0 := 0

	compNameDCS := "Distributed Cache Service"
	compAttrEUNL := []*v1.ComponentAttribute{{Name: "region", Value: "EU-NL"}}
	compDBAttrEUNL := &db.ComponentAttr{
		Name: "region", Value: "EU-NL",
	}

	storedComponent, err := dbIns.GetComponentFromNameAttrs(compNameDCS, compDBAttrEUNL)
	require.NoError(t, err)

	m := &db.Incident{
		Text:      &mTitle,
		StartDate: &startTime,
		EndDate:   &endTime,
		Impact:    &impact0,
		System:    false,
		Type:      event.TypeMaintenance,
		Components: []db.Component{
			*storedComponent,
		},
	}

	maintenanceID, err := dbIns.SaveIncident(m)
	require.NoError(t, err)

	title := "Test incident creation with existed maintenance"

	impact1 := 1
	compCreateData := &v1.ComponentStatusPost{
		Name:       compNameDCS,
		Impact:     impact1,
		Text:       title,
		Attributes: compAttrEUNL,
	}

	t.Log("create an incident with existed maintenance, shouldn't get an error, return an existed incident ", compCreateData)
	data, err := json.Marshal(compCreateData)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/v1/component_status", bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	respCreated := &v1.Incident{}
	err = json.Unmarshal(w.Body.Bytes(), respCreated)
	require.NoError(t, err)

	require.Equal(t, int(maintenanceID), respCreated.ID)
	assert.Equal(t, mTitle, respCreated.Text)
	assert.Equal(t, impact0, *respCreated.Impact)
}

func createIncidentByComponentV1(
	t *testing.T,
	r *gin.Engine,
	inc *v1.ComponentStatusPost,
	expectedText ...string,
) (int, []byte) {
	t.Helper()

	data, err := json.Marshal(inc)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/v1/component_status", bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		return 0, w.Body.Bytes()
	}

	assert.Equal(t, http.StatusCreated, w.Code)

	respCreated := &v1.Incident{}
	err = json.Unmarshal(w.Body.Bytes(), respCreated)
	require.NoError(t, err)

	assert.Equal(t, inc.Impact, *respCreated.Impact)
	text := inc.Text
	if len(expectedText) > 0 {
		text = expectedText[0]
	}
	assert.Equal(t, text, respCreated.Text)
	assert.Nil(t, respCreated.EndDate)

	return respCreated.ID, w.Body.Bytes()
}

func checkConflictMsgV1(t *testing.T, confStruct *v1.ConflictResponse, incID int, text string) {
	t.Helper()
	assert.Equal(t, "Incident with this the component already exists", confStruct.Msg)
	assert.Equal(t, "Check your request parameters", confStruct.Details)
	assert.Equal(t, incID, confStruct.ExistingIncidentID)
	assert.Equal(t, text, confStruct.ExistingIncidentTitle)
}

func closeIncidentV1(t *testing.T, r *gin.Engine, dbIns *db.DB, id int) {
	t.Helper()
	tNow := time.Now().UTC()
	current, err := dbIns.GetIncident(id)
	require.NoError(t, err)
	version := 1
	if current.Version != nil {
		version = *current.Version
	}
	inc := &db.Incident{
		ID:      uint(id),
		EndDate: &tNow,
		Status:  event.IncidentResolved,
		Version: &version,
		Statuses: []db.IncidentStatus{
			{
				IncidentID: uint(id),
				Status:     "resolved",
				Text:       "closed for a next test",
				Timestamp:  tNow,
			},
		},
	}
	err = dbIns.ModifyIncident(inc)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/v1/incidents", nil)

	r.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)

	incidents := getIncidentsAPIV1(t, r)

	for _, i := range incidents {
		if i.ID == id {
			endTime := time.Time(*i.EndDate)
			assert.Equal(t, tNow.YearDay(), endTime.YearDay())
			assert.Equal(t, tNow.Hour(), endTime.Hour())
			assert.Equal(t, tNow.Minute(), endTime.Minute())
		}
	}
}

func checkIncidentsDataAfterMoveV1(t *testing.T, r *gin.Engine, incidentID int) {
	t.Helper()

	incidents := getIncidentsAPIV1(t, r)

	for _, inc := range incidents {
		if inc.ID == incidentID {
			assert.Nil(t, inc.EndDate)
			assert.Equal(t, 3, *inc.Impact)
			require.Len(t, inc.Updates, 3)
			assert.Equal(t, event.OutDatedSystem, inc.Updates[2].Status)
			assert.Equal(t, "impact changed from 2 to 3", inc.Updates[2].Text)
		}
	}
}

func checkIncidentsDataAfterMovingComponentBetweenIncidentsV1(
	t *testing.T,
	r *gin.Engine,
	dbIns *db.DB,
	fromID int,
	toID int,
	fromTitle string,
	toTitle string,
) {
	t.Helper()

	incidents := getIncidentsAPIV1(t, r)

	for _, inc := range incidents {
		switch inc.ID {
		case fromID:
			assert.NotNil(t, inc.EndDate)
			assert.Equal(t, 2, *inc.Impact)
			require.NotEmpty(t, inc.Updates)
			latest := inc.Updates[len(inc.Updates)-1]
			assert.Equal(t, event.IncidentResolved, latest.Status)
			assert.Equal(t, fmt.Sprintf("Distributed Cache Service (Database, EU-NL, dcs) moved to <a href='/incidents/%d'>%s</a>, Incident closed by system", toID, toTitle), latest.Text)
		case toID:
			assert.Nil(t, inc.EndDate)
			assert.Equal(t, 3, *inc.Impact)
			require.NotEmpty(t, inc.Updates)
			latest := inc.Updates[len(inc.Updates)-1]
			assert.Equal(t, event.OutDatedSystem, latest.Status)
			assert.Equal(t, fmt.Sprintf("Distributed Cache Service (Database, EU-NL, dcs) moved from <a href='/incidents/%d'>%s</a>", fromID, fromTitle), latest.Text)
		}
	}

	inc, err := dbIns.GetIncident(fromID)
	require.NoError(t, err)
	assert.NotEmpty(t, inc.Components)

	inc, err = dbIns.GetIncident(toID)
	require.NoError(t, err)
	assert.NotEmpty(t, inc.Components)
}

func checkIncidentsDataAfterMoveAndClosedIncidentV1(t *testing.T, r *gin.Engine, incidentID int) {
	t.Helper()

	incidents := getIncidentsAPIV1(t, r)

	for _, inc := range incidents {
		if inc.ID == incidentID {
			assert.Nil(t, inc.EndDate)
			assert.Equal(t, 3, *inc.Impact)
			require.Len(t, inc.Updates, 4)
			assert.Equal(t, event.OutDatedSystem, inc.Updates[2].Status)
			assert.Equal(t, "impact changed from 2 to 3", inc.Updates[2].Text)
			assert.Equal(t, event.OutDatedSystem, inc.Updates[3].Status)
			assert.Equal(t, "Cloud Container Engine (Container, EU-NL, cce) added", inc.Updates[3].Text)
		}
	}
}

func getIncidentsAPIV1(t *testing.T, r *gin.Engine) []*v1.Incident {
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/v1/incidents", nil)

	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	var incidents []*v1.Incident
	err := json.Unmarshal(w.Body.Bytes(), &incidents)
	require.NoError(t, err)

	return incidents
}

func decreaseIncidentImpactV1(t *testing.T, r *gin.Engine, dbIns *db.DB, id int) {
	t.Helper()
	impact := 2
	current, err := dbIns.GetIncident(id)
	require.NoError(t, err)
	version := 1
	if current.Version != nil {
		version = *current.Version
	}
	inc := &db.Incident{ID: uint(id), Impact: &impact, Version: &version}

	err = dbIns.ModifyIncident(inc)
	require.NoError(t, err)

	incidents := getIncidentsAPIV1(t, r)
	for _, i := range incidents {
		if i.ID == id {
			assert.Equal(t, impact, *i.Impact)
		}
	}

	require.NoError(t, err)
}
