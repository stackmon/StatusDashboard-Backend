package v2

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackmon/otc-status-dashboard/internal/api/errors"
	"github.com/stackmon/otc-status-dashboard/internal/db"
	"github.com/stackmon/otc-status-dashboard/internal/event"
)

func TestGetIncidentsHandler(t *testing.T) {
	r, m, _ := initTests(t)

	startDate := "2024-09-01T11:45:26.371Z"
	endDate := "2024-09-04T11:45:26.371Z"

	testTime, err := time.Parse(time.RFC3339, startDate)
	require.NoError(t, err)

	prepareIncident(t, m, testTime)

	var response = `{"data":[{"id":1,"title":"Incident title A","description":"Description A","impact":0,"components":[150],"start_date":"%s","end_date":"%s","system":false,"type":"maintenance","updates":[{"id":0,"status":"resolved","text":"Issue solved.","timestamp":"%s"}]},{"id":2,"title":"Incident title B","description":"Description B","impact":3,"components":[151],"start_date":"%s","end_date":"%s","system":false,"type":"incident","updates":[{"id":0,"status":"resolved","text":"Issue solved.","timestamp":"%s"}]}]}`

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/v2/incidents", nil)

	r.ServeHTTP(w, req)

	assert.Equal(t, 200, w.Code)
	assert.Equal(t, fmt.Sprintf(response, startDate, endDate, endDate, startDate, endDate, endDate), w.Body.String())
}

func TestGetIncidentsHandlerFilters(t *testing.T) {
	startDate := "2025-03-01T11:45:26.371Z"
	endDate := "2025-03-04T11:45:26.371Z"
	testTime, err := time.Parse(time.RFC3339, startDate)
	require.NoError(t, err)
	testEndTime, err := time.Parse(time.RFC3339, endDate)
	require.NoError(t, err)

	impact0 := 0
	impact3 := 3
	systemFalse := false
	systemTrue := true

	// Mock data setup
	incidentA := db.Incident{
		ID:          1,
		Text:        &[]string{"Incident title A"}[0],
		Description: &[]string{"Description A"}[0],
		StartDate:   &testTime,
		EndDate:     &testEndTime,
		Impact:      &impact0, // Maintenance
		Type:        event.TypeMaintenance,
		System:      systemFalse,
		Components: []db.Component{
			{
				ID:   150,
				Name: "Component A",
				Attrs: []db.ComponentAttr{
					{ID: 859, ComponentID: 150, Name: "category", Value: "A"},
					{ID: 860, ComponentID: 150, Name: "region", Value: "A"},
					{ID: 861, ComponentID: 150, Name: "type", Value: "b"},
				},
			},
		},
		Statuses: []db.IncidentStatus{{ID: 1, IncidentID: 1, Timestamp: testEndTime, Text: "Maintenance completed.", Status: "completed"}},
	}
	incidentB := db.Incident{
		ID:          2,
		Text:        &[]string{"Incident title B"}[0],
		Description: &[]string{"Description B"}[0],
		StartDate:   &testTime,
		EndDate:     nil,      // IsActive
		Impact:      &impact3, // Incident
		Type:        event.TypeIncident,
		System:      systemTrue,
		Components: []db.Component{
			{
				ID:   151,
				Name: "Component B",
				Attrs: []db.ComponentAttr{
					{ID: 862, ComponentID: 151, Name: "category", Value: "B"},
					{ID: 863, ComponentID: 151, Name: "region", Value: "B"},
					{ID: 864, ComponentID: 151, Name: "type", Value: "a"},
				},
			},
		},
		Statuses: []db.IncidentStatus{{ID: 2, IncidentID: 2, Timestamp: testTime, Text: "Incident analysing.", Status: "analysing"}},
	}

	// Expected JSON responses (simplified for brevity)
	responseA := fmt.Sprintf(`{"data":[{"id":1,"title":"Incident title A","description":"Description A","impact":0,"components":[150],"start_date":"%s","end_date":"%s","system":false,"type":"maintenance","updates":[{"id":0,"status":"completed","text":"Maintenance completed.","timestamp":"%s"}]}]}`, startDate, endDate, endDate)
	responseB := fmt.Sprintf(`{"data":[{"id":2,"title":"Incident title B","description":"Description B","impact":3,"components":[151],"start_date":"%s","system":true,"type":"incident","updates":[{"id":0,"status":"analysing","text":"Incident analysing.","timestamp":"%s"}]}]}`, startDate, startDate)
	responseEmpty := `{"data":[]}`
	isActiveTrue := true

	testCases := []struct {
		name           string
		url            string
		mockSetup      func(m sqlmock.Sqlmock, params *db.IncidentsParams)
		expectedStatus int
		expectedBody   string
	}{
		{
			name: "Filter by type=maintenance",
			url:  "/v2/incidents?type=maintenance",
			mockSetup: func(m sqlmock.Sqlmock, params *db.IncidentsParams) {
				params.Types = []string{event.TypeMaintenance}
				prepareMockForIncidents(t, m, []*db.Incident{&incidentA})
			},
			expectedStatus: http.StatusOK,
			expectedBody:   responseA,
		},
		{
			name: "Filter by type=incident",
			url:  "/v2/incidents?type=incident",
			mockSetup: func(m sqlmock.Sqlmock, params *db.IncidentsParams) {
				params.Types = []string{event.TypeIncident}
				prepareMockForIncidents(t, m, []*db.Incident{&incidentB})
			},
			expectedStatus: http.StatusOK,
			expectedBody:   responseB,
		},
		{
			name: "Filter by opened=true",
			url:  "/v2/incidents?opened=true",
			mockSetup: func(m sqlmock.Sqlmock, params *db.IncidentsParams) {
				params.IsActive = &isActiveTrue
				prepareMockForIncidents(t, m, []*db.Incident{&incidentB})
			},
			expectedStatus: http.StatusOK,
			expectedBody:   responseB,
		},
		{
			name:           "Filter by active=false",
			url:            "/v2/incidents?active=false",
			mockSetup:      func(_ sqlmock.Sqlmock, _ *db.IncidentsParams) {}, // No DB call expected
			expectedStatus: http.StatusBadRequest,
			expectedBody:   fmt.Sprintf(`{"errMsg":"%s"}`, errors.ErrIncidentFQueryInvalidFormat),
		},
		{
			name: "Filter by impact=3",
			url:  "/v2/incidents?impact=3",
			mockSetup: func(m sqlmock.Sqlmock, params *db.IncidentsParams) {
				params.Impact = &impact3
				prepareMockForIncidents(t, m, []*db.Incident{&incidentB})
			},
			expectedStatus: http.StatusOK,
			expectedBody:   responseB,
		},
		{
			name: "Filter by system=true",
			url:  "/v2/incidents?system=true",
			mockSetup: func(m sqlmock.Sqlmock, params *db.IncidentsParams) {
				params.IsSystem = &systemTrue
				prepareMockForIncidents(t, m, []*db.Incident{&incidentB})
			},
			expectedStatus: http.StatusOK,
			expectedBody:   responseB,
		},
		{
			name: "Filter by components=151",
			url:  "/v2/incidents?components=151",
			mockSetup: func(m sqlmock.Sqlmock, params *db.IncidentsParams) {
				params.ComponentIDs = []int{151}
				prepareMockForIncidents(t, m, []*db.Incident{&incidentB})
			},
			expectedStatus: http.StatusOK,
			expectedBody:   responseB,
		},
		{
			name: "Filter by status=analysing",
			url:  "/v2/incidents?status=analysing",
			mockSetup: func(m sqlmock.Sqlmock, params *db.IncidentsParams) {
				params.Status = &incidentB.Statuses[0].Status
				prepareMockForIncidents(t, m, []*db.Incident{&incidentB})
			},
			expectedStatus: http.StatusOK,
			expectedBody:   responseB,
		},
		{
			name: "Filter combination: type=incident&active=true",
			url:  "/v2/incidents?type=incident&active=true",
			mockSetup: func(m sqlmock.Sqlmock, params *db.IncidentsParams) {
				params.Types = []string{event.TypeIncident}
				params.IsActive = &isActiveTrue
				prepareMockForIncidents(t, m, []*db.Incident{&incidentB})
			},
			expectedStatus: http.StatusOK,
			expectedBody:   responseB,
		},
		{
			name: "Filter combination: status=analysing&impact=3",
			url:  "/v2/incidents?status=analysing&impact=3",
			mockSetup: func(m sqlmock.Sqlmock, params *db.IncidentsParams) {
				params.Status = &incidentB.Statuses[0].Status
				prepareMockForIncidents(t, m, []*db.Incident{&incidentB})
			},
			expectedStatus: http.StatusOK,
			expectedBody:   responseB,
		},
		{
			name: "Filter wrong status paramter: status=resurrected",
			url:  "/v2/incidents?status=resurrected",
			mockSetup: func(m sqlmock.Sqlmock, params *db.IncidentsParams) {
				params.Status = &incidentB.Statuses[0].Status
				prepareMockForIncidents(t, m, []*db.Incident{})
			},
			expectedStatus: http.StatusBadRequest,
			expectedBody:   fmt.Sprintf(`{"errMsg":"%s"}`, errors.ErrIncidentFQueryInvalidFormat),
		},
		{
			name: "Filter combination: no results",
			url:  "/v2/incidents?impact=1",
			mockSetup: func(m sqlmock.Sqlmock, params *db.IncidentsParams) {
				impact1 := 1
				params.Impact = &impact1
				prepareMockForIncidents(t, m, []*db.Incident{}) // Empty slice
			},
			expectedStatus: http.StatusOK,
			expectedBody:   responseEmpty,
		},
		{
			name:           "Invalid filter: type=invalid",
			url:            "/v2/incidents?type=invalid",
			mockSetup:      func(_ sqlmock.Sqlmock, _ *db.IncidentsParams) {}, // No DB call expected
			expectedStatus: http.StatusBadRequest,
			expectedBody:   fmt.Sprintf(`{"errMsg":"%s"}`, errors.ErrIncidentFQueryInvalidFormat),
		},
		{
			name:           "Invalid filter: active=maybe",
			url:            "/v2/incidents?active=maybe",
			mockSetup:      func(_ sqlmock.Sqlmock, _ *db.IncidentsParams) {},
			expectedStatus: http.StatusBadRequest,
			expectedBody:   fmt.Sprintf(`{"errMsg":"%s"}`, errors.ErrIncidentFQueryInvalidFormat),
		},
		{
			name:           "Invalid filter: active=false",
			url:            "/v2/incidents?active=false",
			mockSetup:      func(_ sqlmock.Sqlmock, _ *db.IncidentsParams) {},
			expectedStatus: http.StatusBadRequest,
			expectedBody:   fmt.Sprintf(`{"errMsg":"%s"}`, errors.ErrIncidentFQueryInvalidFormat),
		},
		{
			name:           "Invalid filter: impact=abc",
			url:            "/v2/incidents?impact=abc",
			mockSetup:      func(_ sqlmock.Sqlmock, _ *db.IncidentsParams) {},
			expectedStatus: http.StatusBadRequest,
			expectedBody:   fmt.Sprintf(`{"errMsg":"%s"}`, errors.ErrIncidentFQueryInvalidFormat),
		},
		{
			name:           "Invalid filter: impact=5",
			url:            "/v2/incidents?impact=5",
			mockSetup:      func(_ sqlmock.Sqlmock, _ *db.IncidentsParams) {},
			expectedStatus: http.StatusBadRequest,
			expectedBody:   fmt.Sprintf(`{"errMsg":"%s"}`, errors.ErrIncidentFQueryInvalidFormat),
		},
		{
			name:           "Invalid filter: components=abc",
			url:            "/v2/incidents?components=abc",
			mockSetup:      func(_ sqlmock.Sqlmock, _ *db.IncidentsParams) {},
			expectedStatus: http.StatusBadRequest,
			expectedBody:   fmt.Sprintf(`{"errMsg":"%s"}`, errors.ErrIncidentFQueryInvalidFormat),
		},
		{
			name:           "Invalid filter: components=2147483649",
			url:            "/v2/incidents?components=2147483649",
			mockSetup:      func(_ sqlmock.Sqlmock, _ *db.IncidentsParams) {},
			expectedStatus: http.StatusBadRequest,
			expectedBody:   fmt.Sprintf(`{"errMsg":"%s"}`, errors.ErrIncidentFQueryInvalidFormat),
		},
		{
			name:           "Invalid filter: start_date after end_date",
			url:            fmt.Sprintf("/v2/incidents?start_date=%s&end_date=%s", endDate, startDate), // Swapped start and end dates
			mockSetup:      func(_ sqlmock.Sqlmock, _ *db.IncidentsParams) {},
			expectedStatus: http.StatusBadRequest,
			expectedBody:   fmt.Sprintf(`{"errMsg":"%s"}`, errors.ErrIncidentFQueryInvalidFormat),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			r, m, _ := initTests(t)
			if tc.expectedStatus == http.StatusOK {
				params := &db.IncidentsParams{} // Expected params for the mock
				tc.mockSetup(m, params)
			}
			w := httptest.NewRecorder()
			req, _ := http.NewRequest(http.MethodGet, tc.url, nil)
			r.ServeHTTP(w, req)
			t.Logf("Test Case: %s - Expected Response: %s", tc.name, tc.expectedBody)
			t.Logf("Test Case: %s - Actual Response: %s", tc.name, w.Body.String())
			assert.Equal(t, tc.expectedStatus, w.Code)
			assert.JSONEq(t, tc.expectedBody, w.Body.String())
			assert.NoError(t, m.ExpectationsWereMet())
		})
	}
}

func TestReturn404Handler(t *testing.T) {
	r, _, _ := initTests(t)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/anyendpoint", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, 404, w.Code)
	assert.JSONEq(t, `{"errMsg":"page not found"}`, w.Body.String())
}

func TestGetEventsHandler(t *testing.T) {
	const totalEvents = 60
	const maintenanceCount = 10
	const infoCount = 10

	r, m, _ := initTests(t)

	mockEvents := make([]*db.Incident, 0, totalEvents)
	startTime := time.Now().UTC().Add(-time.Hour * 24 * 30)

	for i := 1; i <= totalEvents; i++ {
		eventTitle := fmt.Sprintf("Event %d", i)
		eventDescription := fmt.Sprintf("Description for Event %d", i)
		eventStartTime := startTime.Add(time.Hour * time.Duration(i))
		eventEndTime := eventStartTime.Add(time.Minute * 5)
		componentID := 150 + (i % 5)
		var eventType string
		var impact int
		var status event.Status
		var statusText string

		switch {
		case i <= maintenanceCount:
			eventType = event.TypeMaintenance
			impact = 0
			status = event.MaintenancePlanned
			statusText = event.MaintenancePlannedStatusText()
		case i <= maintenanceCount+infoCount:
			eventType = event.TypeInformation
			impact = 0
			status = event.InfoPlanned
			statusText = event.InfoPlannedStatusText()
		default:
			eventType = event.TypeIncident
			impact = (i % 3) + 1 // Cycle through 1, 2, 3
			status = event.IncidentDetected
			statusText = event.IncidentDetectedStatusText()
		}

		textPtr := new(string)
		*textPtr = eventTitle
		descPtr := new(string)
		*descPtr = eventDescription
		impactPtr := new(int)
		*impactPtr = impact
		startTimePtr := new(time.Time)
		*startTimePtr = eventStartTime
		endTimePtr := new(time.Time)
		*endTimePtr = eventEndTime

		mockEvent := &db.Incident{
			ID:          uint(i),
			Text:        textPtr,
			Description: descPtr,
			StartDate:   startTimePtr,
			EndDate:     endTimePtr,
			Impact:      impactPtr,
			Type:        eventType,
			System:      false,
			Components: []db.Component{
				{ID: uint(componentID), Name: fmt.Sprintf("Component %d", componentID)},
			},
			Statuses: []db.IncidentStatus{
				{ID: uint(i), IncidentID: uint(i), Status: status, Text: statusText, Timestamp: eventStartTime},
			},
			Status: status,
		}
		mockEvents = append(mockEvents, mockEvent)
	}

	testCases := []struct {
		name           string
		url            string
		limit          int
		page           int
		expectedCount  int
		expectedPages  int
		expectedStatus int
	}{
		{
			name:           "Get first page with limit 20",
			url:            "/v2/events?limit=20&page=1",
			limit:          20,
			page:           1,
			expectedCount:  20,
			expectedPages:  3,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Get second page with limit 20",
			url:            "/v2/events?limit=20&page=2",
			limit:          20,
			page:           2,
			expectedCount:  20,
			expectedPages:  3,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Get last page with limit 20",
			url:            "/v2/events?limit=20&page=3",
			limit:          20,
			page:           3,
			expectedCount:  20,
			expectedPages:  3,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Get page beyond total pages",
			url:            "/v2/events?limit=20&page=4",
			limit:          20,
			page:           4,
			expectedCount:  0,
			expectedPages:  3,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Get events with default limit (50)",
			url:            "/v2/events?page=1",
			limit:          50,
			page:           1,
			expectedCount:  50,
			expectedPages:  2,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Get events with invalid limit (150), returns validation error",
			url:            "/v2/events?limit=150&page=1",
			limit:          150,
			page:           1,
			expectedCount:  0,
			expectedPages:  0,
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Get page that is out of bounds but should return empty data",
			url:            "/v2/events?limit=50&page=3",
			limit:          50,
			page:           3,
			expectedCount:  0,
			expectedPages:  2,
			expectedStatus: http.StatusOK,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			reversedEvents := make([]*db.Incident, totalEvents)
			for i := range totalEvents {
				reversedEvents[i] = mockEvents[totalEvents-1-i]
			}

			start := (tc.page - 1) * tc.limit
			end := start + tc.limit
			if start > totalEvents {
				start = totalEvents
			}
			if end > totalEvents {
				end = totalEvents
			}
			paginatedMock := reversedEvents[start:end]

			if tc.expectedStatus == http.StatusOK {
				prepareMockForEvents(t, m, paginatedMock, totalEvents)
			}

			w := httptest.NewRecorder()
			req, _ := http.NewRequest(http.MethodGet, tc.url, nil)
			r.ServeHTTP(w, req)

			t.Logf("Response Body: %s", w.Body.String())

			assert.Equal(t, tc.expectedStatus, w.Code)

			if tc.name == "Get events with invalid limit (150), returns validation error" {
				assert.JSONEq(t, fmt.Sprintf(`{"errMsg":"%s"}`, errors.ErrIncidentFQueryInvalidFormat), w.Body.String())
			}

			assert.Equal(t, tc.expectedStatus, w.Code)

			if tc.expectedStatus == http.StatusOK {
				var response struct {
					Data       []Incident `json:"data"`
					Pagination struct {
						PageIndex      int   `json:"pageIndex"`
						RecordsPerPage int   `json:"recordsPerPage"`
						TotalRecords   int64 `json:"totalRecords"`
						TotalPages     int   `json:"totalPages"`
					} `json:"pagination"`
				}
				err := json.Unmarshal(w.Body.Bytes(), &response)
				require.NoError(t, err)

				assert.Len(t, response.Data, tc.expectedCount)
				assert.Equal(t, tc.page, response.Pagination.PageIndex)
				assert.Equal(t, tc.limit, response.Pagination.RecordsPerPage)
				assert.Equal(t, int64(totalEvents), response.Pagination.TotalRecords)
				assert.Equal(t, tc.expectedPages, response.Pagination.TotalPages)
			}

			assert.NoError(t, m.ExpectationsWereMet())
		})
	}
}

func TestGetComponentsAvailabilityHandler(t *testing.T) {
	r, m, _ := initTests(t)
	// Mocking data for testing

	currentTime := time.Now().UTC()
	year, month, _ := currentTime.Date()

	firstDayOfLastMonth := time.Date(year, month-1, 1, 0, 0, 0, 0, time.UTC)
	testTime := firstDayOfLastMonth
	prepareAvailability(t, m, testTime)
	expectedAvailability := ""

	for i := range [12]int{} {
		availYear, availMonth := getYearAndMonth(year, int(month), i)
		percentage := 100
		// For the second month (current month in test setup), set percentage to 0
		if i == 1 {
			percentage = 0
		}
		expectedAvailability += fmt.Sprintf(`{"year":%d,"month":%d,"percentage":%d},`, availYear, availMonth, percentage)
	}

	// Remove trailing comma
	expectedAvailability = expectedAvailability[:len(expectedAvailability)-1]

	response := fmt.Sprintf(`{"data":[{"id":151,"name":"Component B","availability":[%s],"region":"B"}]}`, expectedAvailability)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/v2/availability", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	assert.Equal(t, response, w.Body.String())
}

func TestCalculateAvailability(t *testing.T) {
	type testCase struct {
		testDescription string
		Component       *db.Component
		Result          []*MonthlyAvailability
	}

	impact := 3
	now := time.Now().UTC()
	periodStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -11, 0)

	comp := db.Component{
		ID:        150,
		Name:      "DataArts",
		Incidents: []*db.Incident{},
	}

	compForPeriod := comp
	stDate := time.Date(periodStart.Year(), periodStart.Month(), 21, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(periodStart.Year(), periodStart.Month()+1, 2, 20, 0, 0, 0, time.UTC)
	compForPeriod.Incidents = append(compForPeriod.Incidents, &db.Incident{
		ID:        1,
		StartDate: &stDate,
		EndDate:   &endDate,
		Impact:    &impact,
	})

	const (
		precisionFactor = 100000.0
		fullPercentage  = 100.0
		roundFactor     = 0.5
	)

	calculateExpectedAvailability := func(downtimeHours, totalHours float64) float64 {
		availability := fullPercentage - (downtimeHours / totalHours * fullPercentage)
		return float64(int(availability*precisionFactor+roundFactor)) / precisionFactor
	}

	firstMonthHours := hoursInMonth(stDate.Year(), int(stDate.Month()))
	secondMonthHours := hoursInMonth(endDate.Year(), int(endDate.Month()))
	firstMonthAvailability := calculateExpectedAvailability(
		time.Date(stDate.Year(), stDate.Month()+1, 1, 0, 0, 0, 0, time.UTC).Sub(stDate).Hours(),
		firstMonthHours,
	)
	secondMonthAvailability := calculateExpectedAvailability(
		endDate.Sub(time.Date(endDate.Year(), endDate.Month(), 1, 0, 0, 0, 0, time.UTC)).Hours(),
		secondMonthHours,
	)

	testCases := []testCase{
		{
			testDescription: "Test case: first month (availability drop) and next month (availability drop)",
			Component:       &compForPeriod,
			Result: func() []*MonthlyAvailability {
				results := make([]*MonthlyAvailability, 12)

				for i := range [12]int{} {
					year, month := getYearAndMonth(now.Year(), int(now.Month()), 11-i)
					results[i] = &MonthlyAvailability{
						Year:       year,
						Month:      month,
						Percentage: 100,
					}
					if year == stDate.Year() && month == int(stDate.Month()) {
						results[i] = &MonthlyAvailability{
							Month:      month,
							Percentage: firstMonthAvailability,
						}
					}
					if year == endDate.Year() && month == int(endDate.Month()) {
						results[i] = &MonthlyAvailability{
							Month:      month,
							Percentage: secondMonthAvailability,
						}
					}
				}
				return results
			}(),
		},
	}

	for _, tc := range testCases {
		result, err := calculateAvailability(tc.Component)
		require.NoError(t, err)

		t.Logf("Test '%s': Calculated availability: %+v", tc.testDescription, result)

		assert.Len(t, result, 12)
		for i, r := range result {
			assert.InEpsilon(t, tc.Result[i].Percentage, r.Percentage, 0.0001)
		}
	}
}

func TestValidateEventCreationDescriptionLength(t *testing.T) {
	impact := 1
	system := false

	makeIncident := func(description string) IncidentData {
		return IncidentData{
			Title:       "description boundary test",
			Description: description,
			Impact:      &impact,
			Components:  []int{1},
			StartDate:   time.Now().Add(-time.Hour).UTC(),
			System:      &system,
			Type:        event.TypeIncident,
		}
	}

	t.Run("description with 1500 characters is valid", func(t *testing.T) {
		err := validateEventCreation(makeIncident(strings.Repeat("a", 1500)))
		assert.NoError(t, err)
	})

	t.Run("description with 1501 characters is invalid", func(t *testing.T) {
		err := validateEventCreation(makeIncident(strings.Repeat("a", 1501)))
		require.Error(t, err)
		assert.Equal(t, errors.ErrIncidentDescriptionTooLong, err)
	})
}

func TestCheckPatchDataDescriptionLength(t *testing.T) {
	impact := 2
	stored := &db.Incident{
		Type:   event.TypeIncident,
		Impact: &impact,
	}

	validDesc := strings.Repeat("a", 1500)
	overLongDesc := strings.Repeat("a", 1501)

	testCases := []struct {
		name        string
		description *string
		expectError bool
		expectedErr error
	}{
		{
			name:        "description nil is valid",
			description: nil,
			expectError: false,
		},
		{
			name:        "description with 1500 characters is valid",
			description: &validDesc,
			expectError: false,
		},
		{
			name:        "description with 1501 characters returns ErrIncidentDescriptionTooLong",
			description: &overLongDesc,
			expectError: true,
			expectedErr: errors.ErrIncidentDescriptionTooLong,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			incoming := &PatchIncidentData{
				Status:      event.IncidentDetected,
				Description: tc.description,
			}
			err := checkPatchData(incoming, stored)
			if tc.expectError {
				require.Error(t, err)
				assert.Equal(t, tc.expectedErr, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestCheckPatchDataTypeChangeForbidden verifies that PATCH rejects any attempt
// to change the event type.
func TestCheckPatchDataTypeChangeForbidden(t *testing.T) {
	impact := 2
	stored := &db.Incident{
		Type:   event.TypeIncident,
		Impact: &impact,
	}

	testCases := []struct {
		name        string
		incomingTyp string
		expectError bool
	}{
		{
			name:        "type omitted is valid",
			incomingTyp: "",
			expectError: false,
		},
		{
			name:        "unchanged type is valid",
			incomingTyp: event.TypeIncident,
			expectError: false,
		},
		{
			name:        "incident to maintenance returns ErrIncidentPatchTypeForbidden",
			incomingTyp: event.TypeMaintenance,
			expectError: true,
		},
		{
			name:        "incident to info returns ErrIncidentPatchTypeForbidden",
			incomingTyp: event.TypeInformation,
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			incoming := &PatchIncidentData{
				Status: event.IncidentDetected,
				Type:   tc.incomingTyp,
			}
			err := checkPatchData(incoming, stored)
			if tc.expectError {
				require.Error(t, err)
				assert.Equal(t, errors.ErrIncidentPatchTypeForbidden, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

// TestPatchEventTypeChangeHandler verifies that PATCH /v2/events/:eventID returns HTTP 400
// when the request tries to change the event type.
func TestPatchEventTypeChangeHandler(t *testing.T) {
	impact := 2
	testTime := time.Now().UTC().Add(-time.Hour)
	storedIncident := &db.Incident{
		ID:        112,
		Text:      &[]string{"Test Incident"}[0],
		Impact:    &impact,
		Type:      event.TypeIncident,
		StartDate: &testTime,
	}

	r := initRouterWithStoredEvent(t, storedIncident)

	updateDate := time.Now().UTC().Format(time.RFC3339)
	body := fmt.Sprintf(
		`{"status":"detecting","message":"test message","update_date":%q,"type":"maintenance"}`,
		updateDate,
	)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPatch, "/v2/events/112", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
}

// TestPatchEventDescriptionTooLongHandler verifies that PATCH /v2/events/:eventID returns HTTP 400
// when the incoming description exceeds the 1500-character maximum.
func TestPatchEventDescriptionTooLongHandler(t *testing.T) {
	impact := 2
	testTime := time.Now().UTC().Add(-time.Hour)
	storedIncident := &db.Incident{
		ID:        111,
		Text:      &[]string{"Test Incident"}[0],
		Impact:    &impact,
		Type:      event.TypeIncident,
		StartDate: &testTime,
	}

	r := initRouterWithStoredEvent(t, storedIncident)

	overLongDesc := strings.Repeat("a", 1501)
	updateDate := time.Now().UTC().Format(time.RFC3339)
	body := fmt.Sprintf(
		`{"status":"detecting","message":"test message","update_date":%q,"description":%q}`,
		updateDate, overLongDesc,
	)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPatch, "/v2/events/111", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.JSONEq(t, `{"errMsg":"event description should be 1500 characters or fewer"}`, w.Body.String())
}

func TestPatchEventUpdateHandler(t *testing.T) {
	startDate := "2025-08-01T11:45:26.371Z"
	endDate := "2025-08-04T11:45:26.371Z"
	testTime, err := time.Parse(time.RFC3339, startDate)
	require.NoError(t, err)
	testEndTime, err := time.Parse(time.RFC3339, endDate)
	require.NoError(t, err)

	eventID := 111
	impact2 := 2 // Incident
	systemTrue := true
	updateID1 := 87
	updateID2 := 88
	updateIndex1 := 0
	updateIndex2 := 1
	version1 := 1

	// Mock data setup
	incidentA := db.Incident{
		ID:          uint(eventID),
		Text:        &[]string{"Incident title A"}[0],
		Description: &[]string{"Description A"}[0],
		StartDate:   &testTime,
		EndDate:     &testEndTime,
		Impact:      &impact2,
		Type:        event.TypeIncident,
		System:      systemTrue,
		Version:     &version1,
		Components: []db.Component{
			{
				ID:   151,
				Name: "Component A",
				Attrs: []db.ComponentAttr{
					{ID: 462, ComponentID: 151, Name: "category", Value: "A"},
					{ID: 463, ComponentID: 151, Name: "region", Value: "A"},
					{ID: 464, ComponentID: 151, Name: "type", Value: "a"},
				},
			},
		},
		Statuses: []db.IncidentStatus{
			{ID: uint(updateID1), IncidentID: 111, Status: "analysing", Text: "Incident analysing.", Timestamp: testTime},
			{ID: uint(updateID2), IncidentID: 111, Status: "resolved", Text: "Incident resolved.", Timestamp: testEndTime},
		},
	}

	responseAfterFirst := fmt.Sprintf(
		`{"id":%d,"status":"analysing","text":"Updated: analysing","timestamp":"%s"}`,
		updateIndex1, startDate,
	)
	responseAfterSecond := fmt.Sprintf(
		`{"id":%d,"status":"resolved","text":"Updated: resolved","timestamp":"%s"}`,
		updateIndex2, endDate,
	)

	testCases := []struct {
		name           string
		url            string
		body           string
		mockSetup      func(m sqlmock.Sqlmock)
		expectedStatus int
		expectedBody   string
	}{
		// Tests for /v2/incidents endpoint (deprecated)
		{
			name: "Update incident update id=0 via /v2/incidents",
			url:  fmt.Sprintf("incidents/111/updates/%d", updateIndex1),
			body: `{"text": "Updated: analysing"}`,
			mockSetup: func(m sqlmock.Sqlmock) {
				prepareMockForPatchEventUpdate(
					t, m, &incidentA,
					uint(updateID1),
					"Updated: analysing",
					updateIndex1,
				)
			},
			expectedStatus: http.StatusOK,
			expectedBody:   responseAfterFirst,
		},
		{
			name: "Update incident update id=1 via /v2/incidents",
			url:  fmt.Sprintf("incidents/111/updates/%d", updateIndex2),
			body: `{"text": "Updated: resolved"}`,
			mockSetup: func(m sqlmock.Sqlmock) {
				prepareMockForPatchEventUpdate(
					t, m, &incidentA,
					uint(updateID2),
					"Updated: resolved",
					updateIndex2,
				)
			},
			expectedStatus: http.StatusOK,
			expectedBody:   responseAfterSecond,
		},
		// Tests for /v2/events endpoint (new)
		{
			name: "Update event update id=0 via /v2/events",
			url:  fmt.Sprintf("events/111/updates/%d", updateIndex1),
			body: `{"text": "Updated: analysing"}`,
			mockSetup: func(m sqlmock.Sqlmock) {
				prepareMockForPatchEventUpdate(
					t, m, &incidentA,
					uint(updateID1),
					"Updated: analysing",
					updateIndex1,
				)
			},
			expectedStatus: http.StatusOK,
			expectedBody:   responseAfterFirst,
		},
		{
			name: "Update event update id=1 via /v2/events",
			url:  fmt.Sprintf("events/111/updates/%d", updateIndex2),
			body: `{"text": "Updated: resolved"}`,
			mockSetup: func(m sqlmock.Sqlmock) {
				prepareMockForPatchEventUpdate(
					t, m, &incidentA,
					uint(updateID2),
					"Updated: resolved",
					updateIndex2,
				)
			},
			expectedStatus: http.StatusOK,
			expectedBody:   responseAfterSecond,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			r, m, _ := initTests(t)
			tc.mockSetup(m)

			w := httptest.NewRecorder()
			req, _ := http.NewRequest(http.MethodPatch, fmt.Sprintf("/v2/%s", tc.url), strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")

			r.ServeHTTP(w, req)

			t.Logf("Test Case: %s - Expected Response: %s", tc.name, tc.expectedBody)
			t.Logf("Test Case: %s - Actual Response: %s", tc.name, w.Body.String())

			assert.Equal(t, tc.expectedStatus, w.Code)
			assert.JSONEq(t, tc.expectedBody, w.Body.String())
			assert.NoError(t, m.ExpectationsWereMet())
		})
	}
}

// TestPatchIncidentVersionConflict tests the optimistic locking mechanism for maintenance event updates.
// This test verifies that when a PATCH request is sent with an outdated version number,
// the API correctly returns HTTP 409 Conflict.
//
// Scenario:
// - Maintenance event in database has version = 2
// - PATCH request sent with version = 1 (outdated)
// - UPDATE query returns 0 affected rows (no match because version doesn't match)
// - API returns HTTP 409 Conflict with "version conflict" message.
func TestPatchIncidentVersionConflict(t *testing.T) {
	startDate := "2025-08-01T11:45:26.371Z"
	updateDate := "2025-08-02T11:45:26.371Z"
	testTime, err := time.Parse(time.RFC3339, startDate)
	require.NoError(t, err)

	eventID := 222
	impact0 := 0 // Maintenance
	systemFalse := false
	versionInDB := 2      // Version in database
	versionInRequest := 1 // Version in PATCH request (outdated)

	// Mock data setup - maintenance in DB has version 2 with planned status
	maintenanceInDB := db.Incident{
		ID:          uint(eventID),
		Text:        &[]string{"Maintenance title"}[0],
		Description: &[]string{"Description"}[0],
		StartDate:   &testTime,
		EndDate:     nil,
		Impact:      &impact0,
		Type:        event.TypeMaintenance,
		System:      systemFalse,
		Version:     &versionInDB,
		Components: []db.Component{
			{
				ID:   151,
				Name: "Component A",
				Attrs: []db.ComponentAttr{
					{ID: 462, ComponentID: 151, Name: "category", Value: "A"},
					{ID: 463, ComponentID: 151, Name: "region", Value: "A"},
					{ID: 464, ComponentID: 151, Name: "type", Value: "a"},
				},
			},
		},
		Statuses: []db.IncidentStatus{
			{ID: 91, IncidentID: uint(eventID), Status: event.MaintenancePlanned, Text: "Maintenance planned.", Timestamp: testTime},
		},
	}

	r, m, _ := initTests(t)

	// Mock GetIncident for EventExistenceCheck middleware
	rowsInc, incidentIDs, componentIDs := prepareIncidentRows([]*db.Incident{&maintenanceInDB})
	m.ExpectQuery(`^SELECT (.+) FROM "incident"`).WillReturnRows(rowsInc)

	rowsIncComp, rowsComp, rowsCompAttr, rowsStatus := prepareRelatedRows([]*db.Incident{&maintenanceInDB})
	m.ExpectQuery(`^SELECT (.+) FROM "incident_component_relation"`).
		WithArgs(incidentIDs...).
		WillReturnRows(rowsIncComp)
	m.ExpectQuery(`^SELECT (.+) FROM "component"`).
		WithArgs(componentIDs...).
		WillReturnRows(rowsComp)
	m.ExpectQuery("^SELECT (.+) FROM \"component_attribute\"").
		WillReturnRows(rowsCompAttr)
	m.ExpectQuery(`^SELECT (.+) FROM "incident_status"`).
		WithArgs(incidentIDs...).
		WillReturnRows(rowsStatus)

	// Mock the UPDATE query that will return 0 rows affected (version mismatch)
	// The UPDATE uses WHERE id = ? AND version = ?, so with version=1 it won't match (DB has version=2)
	m.ExpectBegin()
	m.ExpectExec(`^UPDATE "incident" SET .+ WHERE id = \$\d+ AND version = \$\d+$`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
								eventID, versionInRequest).
		WillReturnResult(sqlmock.NewResult(0, 0)) // 0 rows affected - version conflict!
	m.ExpectRollback()

	// Prepare PATCH request with version 1 (but DB has version 2)
	requestBody := fmt.Sprintf(`{
		"message": "Maintenance update",
		"status": "in_progress",
		"update_date": "%s",
		"version": %d
	}`, updateDate, versionInRequest)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPatch, fmt.Sprintf("/v2/events/%d", eventID), strings.NewReader(requestBody))
	req.Header.Set("Content-Type", "application/json")

	r.ServeHTTP(w, req)

	t.Logf("Response: %s", w.Body.String())

	// Assert that we get 409 Conflict with "version conflict" message
	assert.Equal(t, http.StatusConflict, w.Code, "Expected HTTP 409 Conflict for version mismatch")
	assert.Contains(t, w.Body.String(), "version conflict", "Response should contain 'version conflict' message")
	assert.NoError(t, m.ExpectationsWereMet())
}

// TestPatchIncidentVersionConflictIgnoredForNonMaintenance tests that version conflict
// does NOT block updates for incident and info types (version check is only enforced for maintenance).
func TestPatchIncidentVersionConflictIgnoredForNonMaintenance(t *testing.T) {
	testCases := []struct {
		name        string
		eventType   string
		impact      int
		status      event.Status
		patchStatus string
	}{
		{
			name:        "incident type ignores version conflict",
			eventType:   event.TypeIncident,
			impact:      2,
			status:      event.IncidentAnalysing,
			patchStatus: "fixing",
		},
		{
			name:        "info type ignores version conflict",
			eventType:   event.TypeInformation,
			impact:      0,
			status:      event.InfoPlanned,
			patchStatus: "active",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			startDate := "2025-08-01T11:45:26.371Z"
			updateDate := "2025-08-02T11:45:26.371Z"
			testTime, err := time.Parse(time.RFC3339, startDate)
			require.NoError(t, err)

			eventID := 223
			systemFalse := false
			versionInDB := 2
			versionInRequest := 1 // Outdated version

			eventInDB := db.Incident{
				ID:          uint(eventID),
				Text:        &[]string{"Event title"}[0],
				Description: &[]string{"Description"}[0],
				StartDate:   &testTime,
				EndDate:     nil,
				Impact:      &tc.impact,
				Type:        tc.eventType,
				System:      systemFalse,
				Version:     &versionInDB,
				Components: []db.Component{
					{
						ID:   151,
						Name: "Component A",
						Attrs: []db.ComponentAttr{
							{ID: 462, ComponentID: 151, Name: "category", Value: "A"},
							{ID: 463, ComponentID: 151, Name: "region", Value: "A"},
							{ID: 464, ComponentID: 151, Name: "type", Value: "a"},
						},
					},
				},
				Statuses: []db.IncidentStatus{
					{ID: 91, IncidentID: uint(eventID), Status: tc.status, Text: "Status.", Timestamp: testTime},
				},
			}

			r, m, _ := initTests(t)

			// Mock for EventExistenceCheck middleware
			rowsInc, incidentIDs, componentIDs := prepareIncidentRows([]*db.Incident{&eventInDB})
			m.ExpectQuery(`^SELECT (.+) FROM "incident"`).WillReturnRows(rowsInc)

			rowsIncComp, rowsComp, rowsCompAttr, rowsStatus := prepareRelatedRows([]*db.Incident{&eventInDB})
			m.ExpectQuery(`^SELECT (.+) FROM "incident_component_relation"`).
				WithArgs(incidentIDs...).
				WillReturnRows(rowsIncComp)
			m.ExpectQuery(`^SELECT (.+) FROM "component"`).
				WithArgs(componentIDs...).
				WillReturnRows(rowsComp)
			m.ExpectQuery("^SELECT (.+) FROM \"component_attribute\"").
				WillReturnRows(rowsCompAttr)
			m.ExpectQuery(`^SELECT (.+) FROM "incident_status"`).
				WithArgs(incidentIDs...).
				WillReturnRows(rowsStatus)

			// For non-maintenance, UPDATE does NOT include version in WHERE clause
			m.ExpectBegin()
			m.ExpectExec(`^UPDATE "incident" SET .+ WHERE id = \$\d+$`).
				WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
					sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
					eventID).
				WillReturnResult(sqlmock.NewResult(0, 1))
			m.ExpectQuery(`^INSERT INTO "incident_status"`).
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(92))
			m.ExpectCommit()

			// Mock for GetIncident after update (reload)
			rowsInc2, incidentIDs2, componentIDs2 := prepareIncidentRows([]*db.Incident{&eventInDB})
			m.ExpectQuery(`^SELECT (.+) FROM "incident"`).WillReturnRows(rowsInc2)

			rowsIncComp2, rowsComp2, rowsCompAttr2, rowsStatus2 := prepareRelatedRows([]*db.Incident{&eventInDB})
			m.ExpectQuery(`^SELECT (.+) FROM "incident_component_relation"`).
				WithArgs(incidentIDs2...).
				WillReturnRows(rowsIncComp2)
			m.ExpectQuery(`^SELECT (.+) FROM "component"`).
				WithArgs(componentIDs2...).
				WillReturnRows(rowsComp2)
			m.ExpectQuery("^SELECT (.+) FROM \"component_attribute\"").
				WillReturnRows(rowsCompAttr2)
			m.ExpectQuery(`^SELECT (.+) FROM "incident_status"`).
				WithArgs(incidentIDs2...).
				WillReturnRows(rowsStatus2)

			requestBody := fmt.Sprintf(`{
				"message": "Event update",
				"status": "%s",
				"update_date": "%s",
				"version": %d
			}`, tc.patchStatus, updateDate, versionInRequest)

			w := httptest.NewRecorder()
			req, _ := http.NewRequest(http.MethodPatch, fmt.Sprintf("/v2/events/%d", eventID), strings.NewReader(requestBody))
			req.Header.Set("Content-Type", "application/json")

			r.ServeHTTP(w, req)

			t.Logf("Response: %s", w.Body.String())

			assert.Equal(t, http.StatusOK, w.Code, "Expected HTTP 200 OK - version conflict should be ignored for non-maintenance")
			assert.NoError(t, m.ExpectationsWereMet())
		})
	}
}

func TestModifyEventUpdate(t *testing.T) {
	_, m, d := initTests(t)
	startDate := "2025-08-01T11:45:26.371Z"
	testTime, err := time.Parse(time.RFC3339, startDate)
	require.NoError(t, err)

	createdAt := testTime
	modifiedAt := time.Now().UTC()

	eventID := 111
	updateID := 87
	updatedText := "Updated: analysing"

	status := db.IncidentStatus{
		ID:         uint(updateID),
		IncidentID: uint(eventID),
		Status:     "analysing",
		Text:       "Incident analysing.",
		Timestamp:  testTime,
		CreatedAt:  &createdAt,
		ModifiedAt: &createdAt,
	}
	prepareMockForModifyEventUpdate(t, m, status, updatedText, modifiedAt)

	returningRows := sqlmock.NewRows([]string{"id", "incident_id", "text", "status", "timestamp", "created_at", "modified_at"}).
		AddRow(status.ID, status.IncidentID, updatedText, status.Status, status.Timestamp, createdAt, modifiedAt)
	m.ExpectQuery(`^SELECT \* FROM "incident_status" WHERE id = \$1 AND incident_id = \$2`).
		WithArgs(status.ID, status.IncidentID).
		WillReturnRows(returningRows)

	status.Text = updatedText

	updated, err := d.ModifyEventUpdate(status)
	require.NoError(t, err)
	require.NotZero(t, updated.ID)
	require.Equal(t, updatedText, updated.Text)
	require.NotNil(t, updated.CreatedAt)
	require.NotNil(t, updated.ModifiedAt)
	require.Equal(t, createdAt, *updated.CreatedAt)
	require.Equal(t, modifiedAt.Truncate(time.Microsecond), updated.ModifiedAt.Truncate(time.Microsecond))
	require.True(t, updated.ModifiedAt.After(*updated.CreatedAt) || updated.ModifiedAt.Equal(*updated.CreatedAt))
}
