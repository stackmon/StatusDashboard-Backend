package v2

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackmon/otc-status-dashboard/internal/api/errors"
	"github.com/stackmon/otc-status-dashboard/internal/db"
	"github.com/stackmon/otc-status-dashboard/internal/event"
)

func TestReturn404Handler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.NoRoute(errors.Return404)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/anyendpoint", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, 404, w.Code)
	assert.JSONEq(t, `{"errMsg":"page not found"}`, w.Body.String())
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
