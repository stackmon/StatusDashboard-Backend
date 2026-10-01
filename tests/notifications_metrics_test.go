package tests

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/stackmon/otc-status-dashboard/internal/db"
	"github.com/stackmon/otc-status-dashboard/internal/notification"
)

func TestMetrics_EndpointExposesNotificationSeries(t *testing.T) {
	truncateIncidents(t)
	ctx := context.Background()
	d, g := newNotifDB(t)
	incID := seedIncident(t, d)

	// A failed row feeds the outbox_failed gauge.
	enqueueWithState(t, d, g, incID, "bad@com.com",
		map[string]any{"status": db.NotificationStatusFailed, "last_error": "x"})

	// A pending row that the worker will deliver, bumping sent_total.
	require.NoError(t, d.Enqueue(ctx, nil, newOutboxRow(incID, "ok@com.com")))

	metrics := notification.NewMetrics()
	reg := prometheus.NewRegistry()
	metrics.MustRegister(reg)
	reg.MustRegister(notification.NewStatsCollector(d, time.Minute))

	w, err := notification.NewWorker(notification.Config{
		Enabled: true, LeaseTimeout: time.Minute, MaxAttempts: 3,
		BackoffBase: 5 * time.Minute, Timeout: 30 * time.Second,
	}, d, &fakeSender{}, zap.NewNop(), metrics)
	require.NoError(t, err)
	require.NoError(t, w.Drain(ctx))

	rec := httptest.NewRecorder()
	promhttp.HandlerFor(reg, promhttp.HandlerOpts{}).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "notification_attempts_total")
	assert.Contains(t, body, "notification_delivery_duration_seconds")
	assert.Contains(t, body, `notification_sent_total{kind="pending_review"} 1`)
	assert.Contains(t, body, "notification_outbox_failed 1")
	assert.Contains(t, body, "notification_outbox_pending")
}
