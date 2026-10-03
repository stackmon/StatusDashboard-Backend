package tests

import (
	"database/sql"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/stackmon/otc-status-dashboard/internal/api"
	apiErrors "github.com/stackmon/otc-status-dashboard/internal/api/errors"
	v2 "github.com/stackmon/otc-status-dashboard/internal/api/v2"
	"github.com/stackmon/otc-status-dashboard/internal/conf"
	"github.com/stackmon/otc-status-dashboard/internal/db"
	"github.com/stackmon/otc-status-dashboard/internal/event"
	"github.com/stackmon/otc-status-dashboard/internal/notification"
)

// initNotifRouter builds a maintenance router with a real, enabled notification
// publisher wired into the create/patch handlers, plus a raw handle to verify
// the outbox.
func initNotifRouter(t *testing.T) (*gin.Engine, *sql.DB) {
	t.Helper()

	d, err := db.New(&conf.Config{DB: databaseURL})
	require.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	pub := notification.NewPublisher(notification.Config{
		Enabled:         true,
		ReviewSMOD:      "smod@com.com",
		ReviewOperators: []string{"ops@com.com"},
		ReviewAdmins:    []string{"admin@com.com"},
		BaseURL:         "https://status.example.com",
	}, d)

	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.NoRoute(apiErrors.Return404)
	r.Use(api.ErrorHandle())

	logger, _ := zap.NewDevelopment()
	prov := testIDP.provider(t, testRBACService().RoleNames()...)
	rbacSvc := testRBACService()

	v2Api := r.Group("v2")
	v2Api.POST("events",
		api.AuthenticationMW(prov, logger),
		api.RBACAuthorizationMW(rbacSvc, logger),
		api.ValidateComponentsMW(d, logger),
		v2.PostIncidentHandler(d, logger, pub))
	v2Api.GET("events/:eventID",
		api.SetJWTClaims(prov, logger),
		api.CheckEventExistenceMW(d, logger),
		v2.GetIncidentHandler(d, logger, rbacSvc))
	v2Api.PATCH("events/:eventID",
		api.AuthenticationMW(prov, logger),
		api.RBACAuthorizationMW(rbacSvc, logger),
		api.CheckEventExistenceMW(d, logger),
		v2.PatchIncidentHandler(d, logger, pub))

	return r, openRawDB(t)
}

func TestAPI_CreatorCreateMaintenance_EnqueuesReviewAudienceAndCreator(t *testing.T) {
	truncateIncidents(t)
	r, g := initNotifRouter(t)

	resp := createEventOK(t, r, maintenanceData(), creatorTokenA)
	eventID := resp.Result[0].IncidentID

	// Creator submission -> pending_review -> review audience + creator (contact_email).
	assert.ElementsMatch(t,
		[]string{"smod@com.com", "ops@com.com", "admin@com.com", "test@example.com"},
		outboxRecipients(t, g, eventID))
}

func TestAPI_AdminCreateMaintenance_EnqueuesCreatorOnly(t *testing.T) {
	truncateIncidents(t)
	r, g := initNotifRouter(t)

	resp := createEventOK(t, r, maintenanceData(), adminToken)
	eventID := resp.Result[0].IncidentID

	// Admin submission bypasses review -> planned -> creator only.
	assert.Equal(t, []string{"test@example.com"}, outboxRecipients(t, g, eventID))
}

func TestAPI_PatchUnchangedStatus_NoNotification(t *testing.T) {
	truncateIncidents(t)
	r, g := initNotifRouter(t)

	// Admin submission bypasses review -> planned.
	resp := createEventOK(t, r, maintenanceData(), adminToken)
	eventID := resp.Result[0].IncidentID
	created := getEventOK(t, r, eventID, adminToken)
	before := outboxCount(t, g, eventID)
	require.Positive(t, before, "create enqueued a notification")

	newTitle := "updated title"
	patch := patchData(created.Status, created.Version)
	patch.Title = &newTitle
	w := patchEvent(t, r, eventID, patch, adminToken)
	require.Equal(t, http.StatusOK, w.Code, "patch failed: %s", w.Body.String())

	assert.Equal(t, before, outboxCount(t, g, eventID), "unchanged status must not enqueue a notification")
}

func TestAPI_PatchStatusChange_EnqueuesNotification(t *testing.T) {
	truncateIncidents(t)
	r, g := initNotifRouter(t)

	resp := createEventOK(t, r, maintenanceData(), adminToken)
	eventID := resp.Result[0].IncidentID
	created := getEventOK(t, r, eventID, adminToken)
	before := outboxCount(t, g, eventID)

	next := event.MaintenanceInProgress
	if created.Status == next {
		next = event.MaintenanceCompleted
	}
	w := patchEvent(t, r, eventID, patchData(next, created.Version), adminToken)
	require.Equal(t, http.StatusOK, w.Code, "patch failed: %s", w.Body.String())

	assert.Greater(t, outboxCount(t, g, eventID), before, "status change must enqueue a notification")
}

func TestAPI_FailedPatchVersionConflict_NoNewOutboxRow(t *testing.T) {
	truncateIncidents(t)
	r, g := initNotifRouter(t)

	resp := createEventOK(t, r, maintenanceData(), adminToken)
	eventID := resp.Result[0].IncidentID
	before := outboxCount(t, g, eventID)

	// Wrong version -> 409 Conflict -> transaction rolls back, no enqueue.
	w := patchEvent(t, r, eventID, patchData(event.MaintenanceInProgress, intPtr(999)), adminToken)
	require.Equal(t, http.StatusConflict, w.Code)

	assert.Equal(t, before, outboxCount(t, g, eventID), "no new outbox row on version conflict")
}
