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

func TestAvailabilityWindow(t *testing.T) {
	now := time.Date(2026, time.November, 12, 15, 30, 0, 0, time.UTC)
	defaultStart := time.Date(2025, time.December, 1, 0, 0, 0, 0, time.UTC)
	withinWindow := now.AddDate(0, -3, 0)
	tooEarly := now.AddDate(0, -13, 0)

	tests := []struct {
		name      string
		start     *time.Time
		end       *time.Time
		wantStart time.Time
		wantEnd   time.Time
		wantErr   error
	}{
		{
			name:      "default window",
			wantStart: defaultStart,
			wantEnd:   now,
		},
		{
			name:      "custom window inside the default range",
			start:     &withinWindow,
			end:       &now,
			wantStart: withinWindow,
			wantEnd:   now,
		},
		{
			name:    "start after end",
			start:   &now,
			end:     &withinWindow,
			wantErr: errors.ErrAvailabilityQueryInvalidFormat,
		},
		{
			name:    "window longer than a year",
			start:   &tooEarly,
			wantErr: errors.ErrAvailabilityQueryInvalidFormat,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			start, end, err := availabilityWindow(tc.start, tc.end, now)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.True(t, tc.wantStart.Equal(start), "unexpected window start: %v", start)
			assert.True(t, tc.wantEnd.Equal(end), "unexpected window end: %v", end)
		})
	}
}

func TestAvailabilityMonths(t *testing.T) {
	start := time.Date(2026, time.March, 15, 10, 0, 0, 0, time.UTC)
	end := time.Date(2026, time.May, 2, 0, 0, 0, 0, time.UTC)

	months := availabilityMonths(start, end)

	require.Len(t, months, 3)
	assert.Equal(t, time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC), months[0])
	assert.Equal(t, time.Date(2026, time.April, 1, 0, 0, 0, 0, time.UTC), months[1])
	assert.Equal(t, time.Date(2026, time.May, 1, 0, 0, 0, 0, time.UTC), months[2])
}

func TestBuildMonthlyAvailability(t *testing.T) {
	const (
		precisionFactor = 100000.0
		fullPercentage  = 100.0
		roundFactor     = 0.5
	)

	now := time.Date(2026, time.November, 12, 15, 30, 0, 0, time.UTC)
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -11, 0)
	months := availabilityMonths(start, now)
	require.Len(t, months, 12)

	stDate := time.Date(start.Year(), start.Month(), 21, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(start.Year(), start.Month()+1, 2, 20, 0, 0, 0, time.UTC)
	firstMonthDowntime := time.Date(stDate.Year(), stDate.Month()+1, 1, 0, 0, 0, 0, time.UTC).Sub(stDate).Hours()
	secondMonthDowntime := endDate.Sub(time.Date(endDate.Year(), endDate.Month(), 1, 0, 0, 0, 0, time.UTC)).Hours()
	downtime := map[availabilityMonth]float64{
		monthKey(stDate):  firstMonthDowntime,
		monthKey(endDate): secondMonthDowntime,
	}

	calculateExpectedAvailability := func(downtimeHours, totalHours float64) float64 {
		availability := fullPercentage - (downtimeHours / totalHours * fullPercentage)
		return float64(int(availability*precisionFactor+roundFactor)) / precisionFactor
	}
	firstMonthAvailability := calculateExpectedAvailability(
		firstMonthDowntime,
		hoursInMonth(stDate.Year(), int(stDate.Month())),
	)
	secondMonthAvailability := calculateExpectedAvailability(
		secondMonthDowntime,
		hoursInMonth(endDate.Year(), int(endDate.Month())),
	)

	result := buildMonthlyAvailability(months, downtime)
	require.Len(t, result, 12)

	for _, item := range result {
		want := fullPercentage
		switch {
		case item.Year == stDate.Year() && item.Month == int(stDate.Month()):
			want = firstMonthAvailability
		case item.Year == endDate.Year() && item.Month == int(endDate.Month()):
			want = secondMonthAvailability
		}
		assert.InEpsilon(t, want, item.Percentage, 0.0001)
	}

	t.Run("ignores downtime outside the window", func(t *testing.T) {
		outside := map[availabilityMonth]float64{monthKey(start.AddDate(0, -1, 0)): 1000}

		for _, item := range buildMonthlyAvailability(months, outside) {
			assert.InEpsilon(t, fullPercentage, item.Percentage, 0.0001)
		}
	})
}

func TestSortComponentAvailability(t *testing.T) {
	availability := []MonthlyAvailability{
		{Year: 2025, Month: 12},
		{Year: 2026, Month: 1},
		{Year: 2025, Month: 11},
	}

	sortComponentAvailability(availability)

	assert.Equal(t, []MonthlyAvailability{
		{Year: 2026, Month: 1},
		{Year: 2025, Month: 12},
		{Year: 2025, Month: 11},
	}, availability)
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
