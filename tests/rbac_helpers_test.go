package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/stackmon/otc-status-dashboard/internal/api"
	apiErrors "github.com/stackmon/otc-status-dashboard/internal/api/errors"
	"github.com/stackmon/otc-status-dashboard/internal/api/rbac"
	v2 "github.com/stackmon/otc-status-dashboard/internal/api/v2"
	"github.com/stackmon/otc-status-dashboard/internal/conf"
	"github.com/stackmon/otc-status-dashboard/internal/db"
	"github.com/stackmon/otc-status-dashboard/internal/event"
)

const (
	creatorRole  = "sd_creators"
	operatorRole = "sd_operators"
	adminRole    = "sd_admins"
	reporterRole = "sd_reporters"
	// unmappedRole is a valid project role in Zitadel that the backend does not
	// map to any RBAC tier.
	unmappedRole = "sd_readers"
)

// testRBACService builds the RBAC service with the same role mapping as the
// production configuration, including the reporter tier.
func testRBACService() *rbac.Service {
	return rbac.New(rbac.Config{
		Creators:  creatorRole,
		Operators: operatorRole,
		Admins:    adminRole,
		Reporters: reporterRole,
	})
}

// initRBACTests sets up a router with the RBAC protected v2 routes and the
// role mapping of the production configuration.
func initRBACTests(t *testing.T) *gin.Engine {
	t.Helper()

	d, err := db.New(&conf.Config{DB: databaseURL})
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.NoRoute(apiErrors.Return404)
	r.Use(api.ErrorHandle())

	logger, _ := zap.NewDevelopment()
	authn := testIDP.provider(t, testRBACService().RoleNames()...)
	rbacSvc := testRBACService()

	v2Api := r.Group("v2")

	v2Api.GET("events",
		api.SetJWTClaims(authn, logger),
		v2.GetEventsHandler(d, logger, rbacSvc))
	v2Api.POST("events",
		api.AuthenticationMW(authn, logger),
		api.RBACAuthorizationMW(rbacSvc, logger),
		api.ValidateComponentsMW(d, logger),
		v2.PostIncidentHandler(d, logger))
	v2Api.GET("events/:eventID",
		api.SetJWTClaims(authn, logger),
		api.CheckEventExistenceMW(d, logger),
		v2.GetIncidentHandler(d, logger, rbacSvc))
	v2Api.PATCH("events/:eventID",
		api.AuthenticationMW(authn, logger),
		api.RBACAuthorizationMW(rbacSvc, logger),
		api.DenyReporterScopeMW(rbacSvc, logger),
		api.CheckEventExistenceMW(d, logger),
		v2.PatchIncidentHandler(d, logger))
	v2Api.POST("events/:eventID/extract",
		api.AuthenticationMW(authn, logger),
		api.RBACAuthorizationMW(rbacSvc, logger),
		api.DenyReporterScopeMW(rbacSvc, logger),
		api.CheckEventExistenceMW(d, logger),
		api.ValidateComponentsMW(d, logger),
		v2.PostIncidentExtractHandler(d, logger))
	v2Api.POST("components",
		api.AuthenticationMW(authn, logger),
		api.DenyReporterScopeMW(rbacSvc, logger),
		v2.PostComponentHandler(d, logger))

	return r
}

// Tokens for each role used across the RBAC tests.
var (
	adminToken    = tokenForRole("admin-user", adminRole)
	operatorToken = tokenForRole("operator-user", operatorRole)
	creatorTokenA = tokenForRole("user-a", creatorRole)
	creatorTokenB = tokenForRole("user-b", creatorRole)
	reporterToken = tokenForRole("reporter-user", reporterRole)
	noRoleToken   = tokenForRole("norole-user", unmappedRole)
)

// ---------------------------------------------------------------------------
// Event data factories
// ---------------------------------------------------------------------------

func maintenanceData() v2.IncidentData {
	impact := 0
	system := false
	startDate := time.Now().Add(time.Hour).UTC()
	endDate := time.Now().Add(2 * time.Hour).UTC()

	return v2.IncidentData{
		Title:        "RBAC test maintenance",
		Description:  "Integration test event",
		ContactEmail: "test@example.com",
		Impact:       &impact,
		Components:   []int{1, 2},
		StartDate:    startDate,
		EndDate:      &endDate,
		System:       &system,
		Type:         event.TypeMaintenance,
	}
}

func incidentData() v2.IncidentData {
	impact := 1
	system := false
	startDate := time.Now().UTC()

	return v2.IncidentData{
		Title:      "RBAC test incident",
		Impact:     &impact,
		Components: []int{1},
		StartDate:  startDate,
		System:     &system,
		Type:       event.TypeIncident,
	}
}

func infoEventData() v2.IncidentData {
	impact := 0
	system := false
	startDate := time.Now().Add(time.Hour).UTC()
	endDate := time.Now().Add(2 * time.Hour).UTC()

	return v2.IncidentData{
		Title:       "RBAC test info event",
		Description: "Integration test informational event",
		Impact:      &impact,
		Components:  []int{1},
		StartDate:   startDate,
		EndDate:     &endDate,
		System:      &system,
		Type:        event.TypeInformation,
	}
}

func patchData(status event.Status, version *int) *v2.PatchIncidentData {
	return &v2.PatchIncidentData{
		Message:    "test update",
		Status:     status,
		UpdateDate: time.Now().UTC(),
		Version:    version,
	}
}

func intPtr(v int) *int { return &v }

// ---------------------------------------------------------------------------
// HTTP request helpers
// ---------------------------------------------------------------------------

func createEvent(t *testing.T, r *gin.Engine, inc v2.IncidentData, token string) (*httptest.ResponseRecorder, *v2.PostIncidentResp) {
	t.Helper()
	data, err := json.Marshal(inc)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/v2/events", bytes.NewReader(data))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		return w, nil
	}

	resp := &v2.PostIncidentResp{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), resp))
	return w, resp
}

// createEventOK is a convenience wrapper that asserts creation succeeded.
func createEventOK(t *testing.T, r *gin.Engine, inc v2.IncidentData, token string) *v2.PostIncidentResp {
	t.Helper()
	w, resp := createEvent(t, r, inc, token)
	require.Equal(t, http.StatusOK, w.Code, "create event failed: %s", w.Body.String())
	require.NotNil(t, resp)
	return resp
}

func getEvent(t *testing.T, r *gin.Engine, id int, token string) (*httptest.ResponseRecorder, *v2.Incident) {
	t.Helper()
	url := fmt.Sprintf("/v2/events/%d", id)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		return w, nil
	}
	var inc v2.Incident
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &inc))
	return w, &inc
}

func getEventOK(t *testing.T, r *gin.Engine, id int, token string) *v2.Incident {
	t.Helper()
	w, inc := getEvent(t, r, id, token)
	require.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, inc)
	return inc
}

func patchEvent(t *testing.T, r *gin.Engine, id int, patch *v2.PatchIncidentData, token string) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(patch)
	require.NoError(t, err)

	url := fmt.Sprintf("/v2/events/%d", id)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPatch, url, bytes.NewReader(data))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	r.ServeHTTP(w, req)
	return w
}

func listEvents(t *testing.T, r *gin.Engine, token string) []v2.Incident {
	t.Helper()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/v2/events?limit=50&page=1", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		return nil
	}
	var resp struct {
		Data []v2.Incident `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp.Data
}

func eventVersion(inc *v2.Incident) int {
	if inc.Version != nil {
		return *inc.Version
	}
	return 1
}

func lastStatus(inc *v2.Incident) event.Status {
	if len(inc.Updates) == 0 {
		return inc.Status
	}
	return inc.Updates[len(inc.Updates)-1].Status
}

func incNow() time.Time { return time.Now().UTC() }

// transitionTo patches an event to the given status and returns the refreshed
// event. Asserts success.
func transitionTo(t *testing.T, r *gin.Engine, eventID int, status event.Status, token string) *v2.Incident {
	t.Helper()
	inc := getEventOK(t, r, eventID, token)
	w := patchEvent(t, r, eventID, patchData(status, intPtr(eventVersion(inc))), token)
	require.Equal(t, http.StatusOK, w.Code, "transition to %s failed: %s", status, w.Body.String())
	return getEventOK(t, r, eventID, token)
}

// assertPatchStatus sends a PATCH and asserts the expected HTTP status code.
func assertPatchStatus(t *testing.T, r *gin.Engine, eventID int, status event.Status, version *int, token string, wantHTTP int) {
	t.Helper()
	w := patchEvent(t, r, eventID, patchData(status, version), token)
	assert.Equal(t, wantHTTP, w.Code, "PATCH to %s: want HTTP %d, got %d; body: %s",
		status, wantHTTP, w.Code, w.Body.String())
}

// extractComponents sends POST /v2/events/:eventID/extract.
func extractComponents(t *testing.T, r *gin.Engine, eventID int, componentIDs []int, token string) *httptest.ResponseRecorder {
	t.Helper()
	body := v2.PostIncidentExtractData{Components: componentIDs}
	data, err := json.Marshal(body)
	require.NoError(t, err)

	url := fmt.Sprintf("/v2/events/%d/extract", eventID)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	r.ServeHTTP(w, req)
	return w
}

// incidentDataMultiComponent creates an incident with two components for extract tests.
func incidentDataMultiComponent() v2.IncidentData {
	impact := 1
	system := false
	startDate := time.Now().UTC()

	return v2.IncidentData{
		Title:      "Extract test incident",
		Impact:     &impact,
		Components: []int{1, 2},
		StartDate:  startDate,
		System:     &system,
		Type:       event.TypeIncident,
	}
}
