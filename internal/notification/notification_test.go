package notification

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackmon/otc-status-dashboard/internal/conf"
	"github.com/stackmon/otc-status-dashboard/internal/db"
	"github.com/stackmon/otc-status-dashboard/internal/event"
)

func TestKindForStatus(t *testing.T) {
	//nolint:exhaustive // duplicate string constants for Info/Maintenance
	cases := map[event.Status]string{
		event.MaintenancePendingReview: db.NotificationKindPendingReview,
		event.MaintenanceReviewed:      db.NotificationKindReviewed,
		event.MaintenancePlanned:       db.NotificationKindStatusChanged,
		event.MaintenanceInProgress:    db.NotificationKindStatusChanged,
		event.MaintenanceCompleted:     db.NotificationKindStatusChanged,
		event.MaintenanceCancelled:     db.NotificationKindStatusChanged,
	}
	for status, want := range cases {
		assert.Equal(t, want, KindForStatus(status), "status %s", status)
	}
}

func TestBackoff_ProgressionAndCap(t *testing.T) {
	base := 5 * time.Minute
	fn := Backoff(base)

	// Delays are jittered by ±backoffJitter, so assert the window, not an exact point.
	assertWithinJitter := func(attempts int, want time.Duration) {
		t.Helper()
		before := time.Now().UTC()
		got := fn(attempts).Sub(before)
		tolerance := time.Duration(float64(want)*backoffJitter) + time.Second
		assert.InDeltaf(t, float64(want), float64(got), float64(tolerance),
			"attempt %d: got %s, want %s ±%s", attempts, got, want, tolerance)
	}

	assertWithinJitter(1, 5*time.Minute)
	assertWithinJitter(2, 10*time.Minute)
	assertWithinJitter(3, 20*time.Minute)

	// Large attempt count is capped at maxBackoff (2h).
	assertWithinJitter(20, maxBackoff)
}

func TestBackoff_JitterSpreadsRetries(t *testing.T) {
	fn := Backoff(5 * time.Minute)

	seen := make(map[time.Time]struct{})
	for range 20 {
		seen[fn(1)] = struct{}{}
	}

	// Without jitter every caller would queue the retry at the same instant.
	assert.Greater(t, len(seen), 1, "jitter must spread simultaneous failures")
}

func TestConfigFromConf_Disabled(t *testing.T) {
	c := &conf.Config{}
	cfg, err := ConfigFromConf(c)
	require.NoError(t, err)
	assert.False(t, cfg.Enabled)
}

func TestConfigFromConf_ParsesEnabled(t *testing.T) {
	c := &conf.Config{
		WebURL: "https://status.example.com/",
		SMTP: conf.SMTPConfig{
			Host: "smtp.otc", Port: "587", From: "sd@com.com",
			User: "u", Password: "p", TLS: true, Timeout: "30s",
		},
		Notifications: conf.NotificationsConfig{
			Enabled: true, LeaseTimeout: "60s", MaxAttempts: "5", BackoffInterval: "5m",
			SmodEmail: "support@com.com", EmailsOperators: "ops1@com.com, ops2@com.com",
			EmailsAdmins: "admin@com.com",
		},
	}

	cfg, err := ConfigFromConf(c)
	require.NoError(t, err)
	assert.True(t, cfg.Enabled)
	assert.Equal(t, 587, cfg.Port)
	assert.Equal(t, 30*time.Second, cfg.Timeout)
	assert.Equal(t, 60*time.Second, cfg.LeaseTimeout)
	assert.Equal(t, 5, cfg.MaxAttempts)
	assert.Equal(t, 5*time.Minute, cfg.BackoffBase)
	assert.Equal(t, []string{"ops1@com.com", "ops2@com.com"}, cfg.ReviewOperators)
	assert.Equal(t, "https://status.example.com", cfg.BaseURL, "trailing slash trimmed")
}
