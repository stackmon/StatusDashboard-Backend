package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/stackmon/otc-status-dashboard/internal/api"
	apiErrors "github.com/stackmon/otc-status-dashboard/internal/api/errors"
	v2 "github.com/stackmon/otc-status-dashboard/internal/api/v2"
	"github.com/stackmon/otc-status-dashboard/internal/conf"
	"github.com/stackmon/otc-status-dashboard/internal/db"
)

// setOutbox mutates an outbox row by dedup key without touching updated_at
// (UpdateColumns skips autoUpdateTime), so tests can craft ages and states.
func setOutbox(t *testing.T, g *gorm.DB, dedup string, cols map[string]any) {
	t.Helper()
	require.NoError(t, g.Model(&db.NotificationOutbox{}).Where("dedup_key = ?", dedup).UpdateColumns(cols).Error)
}

func enqueueWithState(t *testing.T, d *db.DB, g *gorm.DB, incID uint, recipient string, cols map[string]any) string {
	t.Helper()
	row := newOutboxRow(incID, recipient)
	require.NoError(t, d.Enqueue(context.Background(), nil, row))
	if len(cols) > 0 {
		setOutbox(t, g, row.DedupKey, cols)
	}
	return row.DedupKey
}

func TestGetNotificationStats(t *testing.T) {
	truncateIncidents(t)
	ctx := context.Background()
	d, g := newNotifDB(t)
	incID := seedIncident(t, d)

	enqueueWithState(t, d, g, incID, "p1@com.com", nil) // pending
	enqueueWithState(t, d, g, incID, "p2@com.com", nil) // pending
	enqueueWithState(t, d, g, incID, "s1@com.com", map[string]any{"status": db.NotificationStatusSent})
	enqueueWithState(t, d, g, incID, "f1@com.com", map[string]any{"status": db.NotificationStatusFailed, "last_error": "smtp down"})
	enqueueWithState(t, d, g, incID, "stale@com.com", map[string]any{
		"status": db.NotificationStatusProcessing, "locked_at": time.Now().UTC().Add(-5 * time.Minute),
	})

	stats, err := d.GetNotificationStats(ctx, time.Minute)
	require.NoError(t, err)
	assert.Equal(t, int64(2), stats.Pending)
	assert.Equal(t, int64(1), stats.Processing)
	assert.Equal(t, int64(1), stats.Sent)
	assert.Equal(t, int64(1), stats.Failed)
	assert.Equal(t, int64(1), stats.StaleProcessing)
	assert.Equal(t, int64(0), stats.RetryBacklog)
	assert.GreaterOrEqual(t, stats.OldestPendingAgeSeconds, float64(0))
}

func TestListNotificationsByStatus(t *testing.T) {
	truncateIncidents(t)
	ctx := context.Background()
	d, g := newNotifDB(t)
	incID := seedIncident(t, d)

	enqueueWithState(t, d, g, incID, "ok@com.com", nil)
	enqueueWithState(t, d, g, incID, "bad@com.com", map[string]any{"status": db.NotificationStatusFailed, "last_error": "x"})

	failed, err := d.ListNotificationsByStatus(ctx, db.NotificationStatusFailed, 100)
	require.NoError(t, err)
	require.Len(t, failed, 1)
	assert.Equal(t, "bad@com.com", failed[0].Recipient)

	// Pending rows are the usual symptom of a stuck relay, so they must be listable too.
	pending, err := d.ListNotificationsByStatus(ctx, db.NotificationStatusPending, 100)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, "ok@com.com", pending[0].Recipient)

	limited, err := d.ListNotificationsByStatus(ctx, db.NotificationStatusPending, 0)
	require.NoError(t, err)
	assert.Empty(t, limited)
}

func TestEnsureNotificationSchema(t *testing.T) {
	d, _ := newNotifDB(t)
	require.NoError(t, d.EnsureNotificationSchema(), "migrations are applied in the test DB")
}

func TestRedriveFailed_AllAndByID(t *testing.T) {
	truncateIncidents(t)
	ctx := context.Background()
	d, g := newNotifDB(t)
	incID := seedIncident(t, d)

	failedCols := map[string]any{"status": db.NotificationStatusFailed, "attempts": 3, "last_error": "boom"}
	a := enqueueWithState(t, d, g, incID, "a@com.com", failedCols)
	b := enqueueWithState(t, d, g, incID, "b@com.com", failedCols)

	// Re-drive only row a.
	var rowA db.NotificationOutbox
	require.NoError(t, g.Where("dedup_key = ?", a).First(&rowA).Error)
	n, err := d.RedriveFailed(ctx, rowA.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)

	got := fetchByDedup(t, g, a)
	assert.Equal(t, db.NotificationStatusPending, got.Status)
	assert.Equal(t, 0, got.Attempts)
	require.NotNil(t, got.NextAttemptAt)
	assert.Nil(t, got.LastError)
	// row b untouched
	assert.Equal(t, db.NotificationStatusFailed, fetchByDedup(t, g, b).Status)

	// Re-drive the rest (all remaining failed).
	n, err = d.RedriveFailed(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
	assert.Equal(t, db.NotificationStatusPending, fetchByDedup(t, g, b).Status)
}

func TestDeleteSentBefore(t *testing.T) {
	truncateIncidents(t)
	ctx := context.Background()
	d, g := newNotifDB(t)
	incID := seedIncident(t, d)

	old := time.Now().UTC().Add(-40 * 24 * time.Hour)
	enqueueWithState(t, d, g, incID, "oldsent@com.com", map[string]any{"status": db.NotificationStatusSent, "updated_at": old})
	enqueueWithState(t, d, g, incID, "newsent@com.com", map[string]any{"status": db.NotificationStatusSent})
	enqueueWithState(t, d, g, incID, "failed@com.com", map[string]any{"status": db.NotificationStatusFailed, "updated_at": old})

	cutoff := time.Now().UTC().Add(-30 * 24 * time.Hour)
	deleted, err := d.DeleteSentBefore(ctx, cutoff, 500)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted, "only the old sent row is pruned")

	var remaining int64
	require.NoError(t, g.Model(&db.NotificationOutbox{}).Where("incident_id = ?", incID).Count(&remaining).Error)
	assert.Equal(t, int64(2), remaining, "recent sent + failed kept")
}

// --- API endpoints ---

func initNotifOpsRouter(t *testing.T) (*gin.Engine, *db.DB, *gorm.DB) {
	t.Helper()

	d, err := db.New(&conf.Config{DB: databaseURL})
	require.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	g, err := gorm.Open(gormpostgres.New(gormpostgres.Config{DSN: databaseURL}), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := g.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(2)
	t.Cleanup(func() { _ = sqlDB.Close() })

	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.NoRoute(apiErrors.Return404)
	r.Use(api.ErrorHandle())

	logger := zap.NewNop()
	prov := testIDP.provider(t, testRBACService().RoleNames()...)
	rbacSvc := testRBACService()

	v2Api := r.Group("v2")
	v2Api.GET("notifications/stats",
		api.AuthenticationMW(prov, logger),
		api.RBACAuthorizationMW(rbacSvc, logger),
		v2.GetNotificationStatsHandler(d, logger))
	v2Api.POST("notifications/redrive",
		api.AuthenticationMW(prov, logger),
		api.RBACAuthorizationMW(rbacSvc, logger),
		v2.RedriveNotificationsHandler(d, logger))
	v2Api.GET("notifications/failed",
		api.AuthenticationMW(prov, logger),
		api.RBACAuthorizationMW(rbacSvc, logger),
		v2.GetFailedNotificationsHandler(d, logger))

	return r, d, g
}

func TestAPI_ListNotifications_StatusAndLimit(t *testing.T) {
	truncateIncidents(t)
	r, d, g := initNotifOpsRouter(t)
	incID := seedIncident(t, d)
	enqueueWithState(t, d, g, incID, "stuck@com.com", map[string]any{"attempts": 3})
	enqueueWithState(t, d, g, incID, "dead@com.com",
		map[string]any{"status": db.NotificationStatusFailed, "last_error": "x"})

	list := func(query string) (int, []db.NotificationOutbox) {
		t.Helper()
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodGet, "/v2/notifications/failed"+query, nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		r.ServeHTTP(w, req)

		var body struct {
			Data []db.NotificationOutbox `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		return w.Code, body.Data
	}

	t.Run("defaults to failed", func(t *testing.T) {
		code, rows := list("")
		require.Equal(t, http.StatusOK, code)
		require.Len(t, rows, 1)
		assert.Equal(t, "dead@com.com", rows[0].Recipient)
	})

	t.Run("pending rows are reachable", func(t *testing.T) {
		code, rows := list("?status=pending")
		require.Equal(t, http.StatusOK, code)
		require.Len(t, rows, 1)
		assert.Equal(t, "stuck@com.com", rows[0].Recipient)
	})

	t.Run("limit caps the page", func(t *testing.T) {
		code, rows := list("?status=pending&limit=1")
		require.Equal(t, http.StatusOK, code)
		assert.Len(t, rows, 1)
	})

	t.Run("unknown status is rejected", func(t *testing.T) {
		code, _ := list("?status=bogus")
		assert.Equal(t, http.StatusBadRequest, code)
	})

	t.Run("out-of-range limit is rejected", func(t *testing.T) {
		code, _ := list("?limit=0")
		assert.Equal(t, http.StatusBadRequest, code)
		code, _ = list("?limit=100000")
		assert.Equal(t, http.StatusBadRequest, code)
	})
}

func TestAPI_NotificationStats_AdminOK(t *testing.T) {
	truncateIncidents(t)
	r, d, g := initNotifOpsRouter(t)
	incID := seedIncident(t, d)
	enqueueWithState(t, d, g, incID, "f@com.com", map[string]any{"status": db.NotificationStatusFailed, "last_error": "x"})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/v2/notifications/stats", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var stats db.NotificationStats
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &stats))
	assert.Equal(t, int64(1), stats.Failed)
}

func TestAPI_NotificationStats_NonAdminForbidden(t *testing.T) {
	truncateIncidents(t)
	r, _, _ := initNotifOpsRouter(t)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/v2/notifications/stats", nil)
	req.Header.Set("Authorization", "Bearer "+creatorTokenA)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestAPI_RedriveNotifications_Admin(t *testing.T) {
	truncateIncidents(t)
	r, d, g := initNotifOpsRouter(t)
	incID := seedIncident(t, d)
	dedup := enqueueWithState(t, d, g, incID, "f@com.com",
		map[string]any{"status": db.NotificationStatusFailed, "attempts": 5, "last_error": "x"})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/v2/notifications/redrive", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Redriven int64 `json:"redriven"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, int64(1), resp.Redriven)
	assert.Equal(t, db.NotificationStatusPending, fetchByDedup(t, g, dedup).Status)
}

func TestAPI_RedriveNotifications_EmptyBodyRedrivesAll(t *testing.T) {
	truncateIncidents(t)
	r, d, g := initNotifOpsRouter(t)
	incID := seedIncident(t, d)
	failedCols := map[string]any{"status": db.NotificationStatusFailed, "attempts": 5, "last_error": "x"}
	a := enqueueWithState(t, d, g, incID, "a@com.com", failedCols)
	b := enqueueWithState(t, d, g, incID, "b@com.com", failedCols)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/v2/notifications/redrive", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Redriven int64 `json:"redriven"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, int64(2), resp.Redriven)
	assert.Equal(t, db.NotificationStatusPending, fetchByDedup(t, g, a).Status)
	assert.Equal(t, db.NotificationStatusPending, fetchByDedup(t, g, b).Status)
}

func TestAPI_RedriveNotifications_MalformedBodyRejected(t *testing.T) {
	truncateIncidents(t)
	r, d, g := initNotifOpsRouter(t)
	incID := seedIncident(t, d)
	dedup := enqueueWithState(t, d, g, incID, "f@com.com",
		map[string]any{"status": db.NotificationStatusFailed, "attempts": 5, "last_error": "x"})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/v2/notifications/redrive", bytes.NewReader([]byte(`{invalid}`)))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, db.NotificationStatusFailed, fetchByDedup(t, g, dedup).Status, "no redrive on malformed body")
}

func TestAPI_RedriveNotifications_TooManyIDsRejected(t *testing.T) {
	truncateIncidents(t)
	r, d, g := initNotifOpsRouter(t)
	incID := seedIncident(t, d)
	dedup := enqueueWithState(t, d, g, incID, "f@com.com",
		map[string]any{"status": db.NotificationStatusFailed, "attempts": 5, "last_error": "x"})

	ids := make([]uint, 1001)
	for i := range ids {
		ids[i] = uint(i + 1)
	}
	payload, err := json.Marshal(map[string]any{"ids": ids})
	require.NoError(t, err)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/v2/notifications/redrive", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, db.NotificationStatusFailed, fetchByDedup(t, g, dedup).Status, "no redrive when the id list is too large")
}
