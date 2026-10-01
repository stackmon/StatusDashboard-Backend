package tests

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackmon/otc-status-dashboard/internal/event"
	"github.com/stackmon/otc-status-dashboard/internal/notification"
)

func TestPublisher_RepeatedPublishDedups(t *testing.T) {
	ctx := context.Background()
	d, g := newNotifDB(t)
	incID := seedIncident(t, d)

	pub := notification.NewPublisher(notification.Config{Enabled: true}, d)
	ch := notification.Change{
		IncidentID:   incID,
		OldStatus:    event.MaintenancePlanned,
		NewStatus:    event.MaintenanceInProgress,
		ContactEmail: "creator@com.com",
	}

	require.NoError(t, pub.PublishTx(ctx, nil, ch))
	require.NoError(t, pub.PublishTx(ctx, nil, ch), "a repeated publish must not fail the caller's transaction")

	assert.Equal(t, int64(1), outboxCount(t, g, int(incID)), "the same transition must not enqueue a second row")
}

func TestPublisher_NewTransitionEnqueuesAgain(t *testing.T) {
	ctx := context.Background()
	d, g := newNotifDB(t)
	incID := seedIncident(t, d)

	pub := notification.NewPublisher(notification.Config{Enabled: true}, d)
	base := notification.Change{IncidentID: incID, ContactEmail: "creator@com.com"}

	inProgress := base
	inProgress.OldStatus, inProgress.NewStatus = event.MaintenancePlanned, event.MaintenanceInProgress
	require.NoError(t, pub.PublishTx(ctx, nil, inProgress))

	completed := base
	completed.OldStatus, completed.NewStatus = event.MaintenanceInProgress, event.MaintenanceCompleted
	require.NoError(t, pub.PublishTx(ctx, nil, completed))

	assert.Equal(t, int64(2), outboxCount(t, g, int(incID)), "a distinct transition notifies again")
}

func TestPublisher_ContactEmailOutsideAllowListIsDropped(t *testing.T) {
	ctx := context.Background()
	d, g := newNotifDB(t)
	incID := seedIncident(t, d)

	pub := notification.NewPublisher(notification.Config{
		Enabled:        true,
		AllowedDomains: []string{"example.com"},
	}, d)

	err := pub.PublishTx(ctx, nil, notification.Change{
		IncidentID:   incID,
		NewStatus:    event.MaintenancePlanned,
		ContactEmail: "creator@gmail.com",
	})
	require.NoError(t, err)

	assert.Equal(t, int64(0), outboxCount(t, g, int(incID)),
		"the allow-list must gate contact_email on every publish path, not just the API boundary")
}
