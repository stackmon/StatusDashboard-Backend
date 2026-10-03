package tests

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackmon/otc-status-dashboard/internal/db"
	"github.com/stackmon/otc-status-dashboard/internal/event"
)

// TestE2E_CreateMaintenanceDeliversToAllRecipients ties the whole pipeline together:
// API create -> outbox rows -> worker drain -> sender delivers each recipient.
func TestE2E_CreateMaintenanceDeliversToAllRecipients(t *testing.T) {
	truncateIncidents(t)
	ctx := context.Background()

	r, g := initNotifRouter(t)
	resp := createEventOK(t, r, maintenanceData(), creatorTokenA) // pending_review
	eventID := resp.Result[0].IncidentID
	require.Len(t, outboxRecipients(t, g, eventID), 4)

	d, _ := newNotifDB(t)
	fake := &fakeSender{}
	require.NoError(t, testWorker(t, d, fake, 3).Drain(ctx))

	assert.ElementsMatch(t,
		[]string{"smod@com.com", "ops@com.com", "admin@com.com", "test@example.com"},
		fake.recipients())

	assert.Equal(t, int64(0), outboxNotSentCount(t, g, eventID), "every recipient delivered")
}

// TestE2E_ReviewedTransitionNotifiesReviewAudience covers the `reviewed` kind row set.
func TestE2E_ReviewedTransitionNotifiesReviewAudience(t *testing.T) {
	truncateIncidents(t)

	r, g := initNotifRouter(t)
	resp := createEventOK(t, r, maintenanceData(), creatorTokenA) // pending_review
	eventID := resp.Result[0].IncidentID
	transitionTo(t, r, eventID, event.MaintenanceReviewed, adminToken) // -> reviewed

	assert.ElementsMatch(t,
		[]string{"smod@com.com", "ops@com.com", "admin@com.com", "test@example.com"},
		outboxRecipientsByKind(t, g, eventID, db.NotificationKindReviewed))
}

// TestE2E_LifecycleTransitionNotifiesCreatorOnly verifies that no lifecycle transition
// ever reaches the review audience — every status_changed row targets the creator.
func TestE2E_LifecycleTransitionNotifiesCreatorOnly(t *testing.T) {
	truncateIncidents(t)

	r, g := initNotifRouter(t)
	resp := createEventOK(t, r, maintenanceData(), adminToken) // admin -> planned
	eventID := resp.Result[0].IncidentID
	transitionTo(t, r, eventID, event.MaintenanceCancelled, adminToken) // planned -> cancelled

	rows := queryOutbox(t, g, "incident_id = $1", eventID)
	require.NotEmpty(t, rows)
	for i := range rows {
		assert.Equal(t, db.NotificationKindStatusChanged, rows[i].Kind)
		assert.Equal(t, "test@example.com", rows[i].Recipient, "lifecycle notifies creator only")
	}
}
