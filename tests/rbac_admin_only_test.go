package tests

import (
	"net/http"
	"testing"

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

// initTestsAdminOnly sets up a router where only the admin RBAC role is
// configured. Creator and operator roles are empty strings.
func initTestsAdminOnly(t *testing.T) *gin.Engine {
	t.Helper()

	d, err := db.New(&conf.Config{DB: databaseURL})
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.NoRoute(apiErrors.Return404)
	r.Use(api.ErrorHandle())

	logger, _ := zap.NewDevelopment()
	authn := testIDP.provider(t, testRBACService().RoleNames()...)
	rbacSvc := rbac.New(rbac.Config{Admins: adminRole})

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
		api.CheckEventExistenceMW(d, logger),
		v2.PatchIncidentHandler(d, logger))

	return r
}

// ---------------------------------------------------------------------------
// Admin-only config: admin can perform all operations
// ---------------------------------------------------------------------------

func TestAdminOnly_AdminCRUD(t *testing.T) {
	r := initTestsAdminOnly(t)

	t.Run("admin POST creates event", func(t *testing.T) {
		truncateIncidents(t)
		w, resp := createEvent(t, r, maintenanceData(), adminToken)
		assert.Equal(t, http.StatusOK, w.Code)
		require.NotNil(t, resp)
	})

	t.Run("admin GET list returns events", func(t *testing.T) {
		truncateIncidents(t)
		createEventOK(t, r, maintenanceData(), adminToken)

		events := listEvents(t, r, adminToken)
		assert.NotEmpty(t, events)
	})

	t.Run("admin GET single event", func(t *testing.T) {
		truncateIncidents(t)
		resp := createEventOK(t, r, maintenanceData(), adminToken)
		eventID := resp.Result[0].IncidentID

		w, inc := getEvent(t, r, eventID, adminToken)
		assert.Equal(t, http.StatusOK, w.Code)
		require.NotNil(t, inc)
	})

	t.Run("admin PATCH transitions event", func(t *testing.T) {
		truncateIncidents(t)
		resp := createEventOK(t, r, maintenanceData(), adminToken)
		eventID := resp.Result[0].IncidentID

		inc := getEventOK(t, r, eventID, adminToken)
		assertPatchStatus(t, r, eventID, event.MaintenanceInProgress,
			intPtr(eventVersion(inc)), adminToken, http.StatusOK)
	})
}

// ---------------------------------------------------------------------------
// Admin-only config: creator/operator tokens are rejected on write ops
// ---------------------------------------------------------------------------

func TestAdminOnly_CreatorRejected(t *testing.T) {
	r := initTestsAdminOnly(t)

	t.Run("creator POST rejected with 403", func(t *testing.T) {
		truncateIncidents(t)
		w, _ := createEvent(t, r, maintenanceData(), creatorTokenA)
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("creator PATCH rejected with 403", func(t *testing.T) {
		truncateIncidents(t)
		resp := createEventOK(t, r, maintenanceData(), adminToken)
		eventID := resp.Result[0].IncidentID

		inc := getEventOK(t, r, eventID, adminToken)
		assertPatchStatus(t, r, eventID, event.MaintenanceInProgress,
			intPtr(eventVersion(inc)), creatorTokenA, http.StatusForbidden)
	})
}

func TestAdminOnly_OperatorRejected(t *testing.T) {
	r := initTestsAdminOnly(t)

	t.Run("operator POST rejected with 403", func(t *testing.T) {
		truncateIncidents(t)
		w, _ := createEvent(t, r, maintenanceData(), operatorToken)
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("operator PATCH rejected with 403", func(t *testing.T) {
		truncateIncidents(t)
		resp := createEventOK(t, r, maintenanceData(), adminToken)
		eventID := resp.Result[0].IncidentID

		inc := getEventOK(t, r, eventID, adminToken)
		assertPatchStatus(t, r, eventID, event.MaintenanceInProgress,
			intPtr(eventVersion(inc)), operatorToken, http.StatusForbidden)
	})
}

// ---------------------------------------------------------------------------
// Admin-only config: unauthenticated GET still works (soft auth)
// ---------------------------------------------------------------------------

func TestAdminOnly_UnauthenticatedGET(t *testing.T) {
	r := initTestsAdminOnly(t)
	truncateIncidents(t)

	// Create a planned event (visible to unauth).
	resp := createEventOK(t, r, maintenanceData(), adminToken)
	eventID := resp.Result[0].IncidentID

	t.Run("unauth GET list succeeds", func(t *testing.T) {
		events := listEvents(t, r, "")
		assert.NotEmpty(t, events)
	})

	t.Run("unauth GET single event succeeds", func(t *testing.T) {
		w, inc := getEvent(t, r, eventID, "")
		assert.Equal(t, http.StatusOK, w.Code)
		require.NotNil(t, inc)
	})
}
