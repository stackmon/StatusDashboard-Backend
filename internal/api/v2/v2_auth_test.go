package v2

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/stackmon/otc-status-dashboard/internal/api/rbac"
	"github.com/stackmon/otc-status-dashboard/internal/db"
)

func TestCreatorFieldExposedToAuthenticated(t *testing.T) {
	d, m, err := db.NewWithMock()
	require.NoError(t, err)
	log := zap.NewNop()

	now := time.Now().UTC()
	creator := "user@example.com"
	contactEmail := "contact@example.com"

	m.ExpectQuery(`^SELECT count\(\*\) FROM "incident"`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	rowsInc := sqlmock.NewRows([]string{"id", "text", "description", "start_date", "end_date", "impact", "system", "type", "created_by", "contact_email"}).
		AddRow(1, "Title", "Desc", now, now.Add(1*time.Hour), 1, false, "maintenance", creator, contactEmail)
	m.ExpectQuery(`^SELECT (.+) FROM "incident"`).WillReturnRows(rowsInc)

	m.ExpectQuery(`^SELECT (.+) FROM "incident_component_relation"`).WillReturnRows(sqlmock.NewRows([]string{"incident_id", "component_id"}))
	m.ExpectQuery(`^SELECT (.+) FROM "incident_status"`).WillReturnRows(sqlmock.NewRows([]string{"id", "incident_id", "timestamp", "text", "status"}))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest(http.MethodGet, "/v2/events", nil)

	c.Set("userIDRoles", []string{"operators"})

	handler := GetEventsHandler(d, log, rbac.New(rbac.Config{Operators: "operators"}))
	handler(c)

	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), `"creator":"user@example.com"`)
}

func TestCreatorFieldHiddenFromUnauthenticated(t *testing.T) {
	d, m, err := db.NewWithMock()
	require.NoError(t, err)
	log := zap.NewNop()

	now := time.Now().UTC()
	creator := "user@example.com"
	contactEmail := "contact@example.com"

	m.ExpectQuery(`^SELECT count\(\*\) FROM "incident"`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	rowsInc := sqlmock.NewRows([]string{"id", "text", "description", "start_date", "end_date", "impact", "system", "type", "created_by", "contact_email"}).
		AddRow(1, "Title", "Desc", now, now.Add(1*time.Hour), 1, false, "maintenance", creator, contactEmail)
	m.ExpectQuery(`^SELECT (.+) FROM "incident"`).WillReturnRows(rowsInc)

	m.ExpectQuery(`^SELECT (.+) FROM "incident_component_relation"`).WillReturnRows(sqlmock.NewRows([]string{"incident_id", "component_id"}))
	m.ExpectQuery(`^SELECT (.+) FROM "incident_status"`).WillReturnRows(sqlmock.NewRows([]string{"id", "incident_id", "timestamp", "text", "status"}))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest(http.MethodGet, "/v2/events", nil)

	handler := GetEventsHandler(d, log, rbac.New(rbac.Config{Operators: "operators"}))
	handler(c)

	assert.Equal(t, 200, w.Code)
	assert.NotContains(t, w.Body.String(), "creator")
	assert.NotContains(t, w.Body.String(), "user@example.com")
}

func TestContactEmailHiddenFromUnauthenticated(t *testing.T) {
	d, m, err := db.NewWithMock()
	require.NoError(t, err)
	log := zap.NewNop()

	now := time.Now().UTC()
	creator := "user@example.com"
	contactEmail := "contact@example.com"

	m.ExpectQuery(`^SELECT count\(\*\) FROM "incident"`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	rowsInc := sqlmock.NewRows([]string{"id", "text", "description", "start_date", "end_date", "impact", "system", "type", "created_by", "contact_email"}).
		AddRow(1, "Title", "Desc", now, now.Add(1*time.Hour), 1, false, "maintenance", creator, contactEmail)
	m.ExpectQuery(`^SELECT (.+) FROM "incident"`).WillReturnRows(rowsInc)

	m.ExpectQuery(`^SELECT (.+) FROM "incident_component_relation"`).WillReturnRows(sqlmock.NewRows([]string{"incident_id", "component_id"}))
	m.ExpectQuery(`^SELECT (.+) FROM "incident_status"`).WillReturnRows(sqlmock.NewRows([]string{"id", "incident_id", "timestamp", "text", "status"}))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest(http.MethodGet, "/v2/events", nil)

	handler := GetEventsHandler(d, log, rbac.New(rbac.Config{Operators: "operators"}))
	handler(c)

	assert.Equal(t, 200, w.Code)
	assert.NotContains(t, w.Body.String(), "contact_email")
	assert.NotContains(t, w.Body.String(), "contact@example.com")
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
