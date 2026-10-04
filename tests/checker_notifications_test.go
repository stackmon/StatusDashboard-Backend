package tests

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/stackmon/otc-status-dashboard/internal/checker"
	"github.com/stackmon/otc-status-dashboard/internal/conf"
	"github.com/stackmon/otc-status-dashboard/internal/db"
	"github.com/stackmon/otc-status-dashboard/internal/event"
	"github.com/stackmon/otc-status-dashboard/internal/notification"
)

// newTestChecker builds a checker on a fresh pool and a publisher wired to
// the same pool, mirroring the app's wiring.
func newTestChecker(t *testing.T) *checker.Checker {
	t.Helper()

	d, err := db.New(&conf.Config{DB: databaseURL})
	require.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	ncfg, err := notification.ConfigFromConf(notifCheckerConfig())
	require.NoError(t, err)

	return checker.New(d, zap.NewNop(), notification.NewPublisher(ncfg, d))
}

func notifCheckerConfig() *conf.Config {
	return &conf.Config{
		DB:     databaseURL,
		WebURL: "https://status.example.com",
		SMTP:   conf.SMTPConfig{Host: "smtp", Port: "587", From: "sd@com.com", Timeout: "30s"},
		Notifications: conf.NotificationsConfig{
			Enabled: true, LeaseTimeout: "60s", MaxAttempts: "5", BackoffInterval: "5m",
			SmodEmail: "smod@com.com", EmailsOperators: "ops@com.com", EmailsAdmins: "admin@com.com",
		},
	}
}

func TestChecker_ReviewedToPlanned_EnqueuesStatusChangedToCreator(t *testing.T) {
	truncateIncidents(t)

	// Use a router WITHOUT a publisher so the create + approval produce no outbox rows;
	// only the checker transition should enqueue.
	r := initTests(t)
	resp := createEventOK(t, r, maintenanceData(), creatorTokenA) // -> pending_review
	eventID := resp.Result[0].IncidentID
	transitionTo(t, r, eventID, event.MaintenanceReviewed, adminToken) // -> reviewed

	g := openRawDB(t)

	// No outbox rows yet (publisher was off during API calls).
	require.Equal(t, int64(0), outboxCount(t, g, eventID))

	chk := newTestChecker(t)

	require.NoError(t, chk.CheckMaintenance()) // reviewed -> planned

	rows := queryOutbox(t, g, "incident_id = $1", eventID)
	require.Len(t, rows, 1, "one notification per real transition")
	assert.Equal(t, db.NotificationKindStatusChanged, rows[0].Kind)
	assert.Equal(t, "test@example.com", rows[0].Recipient, "planned notifies creator only")
	assert.Equal(t, "checker", rows[0].Payload["actor"])
}

func TestChecker_NoTransition_EnqueuesNothing(t *testing.T) {
	truncateIncidents(t)

	r := initTests(t)
	resp := createEventOK(t, r, maintenanceData(), adminToken) // admin -> planned (future start)
	eventID := resp.Result[0].IncidentID

	g := openRawDB(t)

	chk := newTestChecker(t)

	// Planned with a future start date: the checker computes planned again -> no change.
	require.NoError(t, chk.CheckMaintenance())

	assert.Equal(t, int64(0), outboxCount(t, g, eventID), "no notification without a real transition")
}
