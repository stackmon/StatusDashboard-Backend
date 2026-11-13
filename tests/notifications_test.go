package tests

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackmon/otc-status-dashboard/internal/conf"
	"github.com/stackmon/otc-status-dashboard/internal/db"
)

// newNotifDB returns the DB under test plus a raw handle for seeding and
// verification against the real Postgres database.
func newNotifDB(t *testing.T) (*db.DB, *sql.DB) {
	t.Helper()

	d, err := db.New(&conf.Config{DB: databaseURL})
	require.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	return d, openRawDB(t)
}

// seedIncident inserts a minimal maintenance incident to satisfy the outbox FK
// and returns its id.
func seedIncident(t *testing.T, d *db.DB) uint {
	t.Helper()

	text := "notif-test maintenance"
	start := time.Now().UTC()
	impact := 0
	id, err := d.SaveIncident(context.Background(), &db.Incident{
		Text:      &text,
		StartDate: &start,
		Impact:    &impact,
		System:    false,
		Type:      "maintenance",
	})
	require.NoError(t, err)
	return id
}

// newOutboxRow builds a pending outbox row with a unique dedup key.
func newOutboxRow(incidentID uint, recipient string) db.NotificationOutbox {
	changeID := uuid.NewString()
	return db.NotificationOutbox{
		Kind:       db.NotificationKindPendingReview,
		IncidentID: incidentID,
		Recipient:  recipient,
		Payload:    map[string]any{"title": "test"},
		ChangeID:   changeID,
		DedupKey:   fmt.Sprintf("%s:%s:%s", changeID, db.NotificationKindPendingReview, recipient),
		Status:     db.NotificationStatusPending,
	}
}

func TestEnqueue_Success(t *testing.T) {
	ctx := context.Background()
	d, g := newNotifDB(t)
	incID := seedIncident(t, d)

	row := newOutboxRow(incID, "creator@com.com")
	require.NoError(t, d.Enqueue(ctx, nil, row))

	stored := fetchByDedup(t, g, row.DedupKey)
	assert.Equal(t, db.NotificationStatusPending, stored.Status)
	assert.Equal(t, incID, stored.IncidentID)
	assert.Equal(t, 0, stored.Attempts)
	assert.Equal(t, map[string]any{"title": "test"}, stored.Payload)
	// timestamptz stores UTC; the persisted instant must be recent.
	assert.WithinDuration(t, time.Now().UTC(), stored.CreatedAt.UTC(), 30*time.Second)
}

func TestEnqueue_DuplicateDedupKey(t *testing.T) {
	ctx := context.Background()
	d, _ := newNotifDB(t)
	incID := seedIncident(t, d)

	row := newOutboxRow(incID, "creator@com.com")
	require.NoError(t, d.Enqueue(ctx, nil, row))

	err := d.Enqueue(ctx, nil, row)
	require.ErrorIs(t, err, db.ErrNotificationDuplicate)
}

func TestEnqueue_MissingDedupKey(t *testing.T) {
	ctx := context.Background()
	d, _ := newNotifDB(t)
	incID := seedIncident(t, d)

	row := newOutboxRow(incID, "creator@com.com")
	row.DedupKey = ""
	require.Error(t, d.Enqueue(ctx, nil, row))
}

func TestClaimPending_MarksProcessing(t *testing.T) {
	ctx := context.Background()
	d, g := newNotifDB(t)
	incID := seedIncident(t, d)

	row := newOutboxRow(incID, "ops@com.com")
	require.NoError(t, d.Enqueue(ctx, nil, row))

	claimed, err := d.ClaimPending(ctx, nil, 10, "pod-1", time.Minute)
	require.NoError(t, err)
	require.NotEmpty(t, claimed)

	var target *db.NotificationOutbox
	for i := range claimed {
		if claimed[i].DedupKey == row.DedupKey {
			target = &claimed[i]
			break
		}
	}
	require.NotNil(t, target, "enqueued row must be claimed")
	assert.Equal(t, db.NotificationStatusProcessing, target.Status)
	assert.Equal(t, 1, target.Attempts)
	require.NotNil(t, target.LockedBy)
	assert.Equal(t, "pod-1", *target.LockedBy)

	stored := fetchRow(t, g, target.ID)
	assert.Equal(t, db.NotificationStatusProcessing, stored.Status)
	assert.Equal(t, 1, stored.Attempts)
}

func TestClaimPending_DoesNotReclaimProcessing(t *testing.T) {
	ctx := context.Background()
	d, _ := newNotifDB(t)
	incID := seedIncident(t, d)

	row := newOutboxRow(incID, "admin@com.com")
	require.NoError(t, d.Enqueue(ctx, nil, row))

	first, err := d.ClaimPending(ctx, nil, 10, "pod-1", time.Minute)
	require.NoError(t, err)
	require.NotEmpty(t, first)

	// A second claim must not return the same row (already processing).
	second, err := d.ClaimPending(ctx, nil, 10, "pod-2", time.Minute)
	require.NoError(t, err)
	for i := range second {
		assert.NotEqual(t, row.DedupKey, second[i].DedupKey,
			"row already processing must not be reclaimed")
	}
}

func TestMarkSent_UpdatesStatusAndClearsLease(t *testing.T) {
	ctx := context.Background()
	d, g := newNotifDB(t)
	incID := seedIncident(t, d)

	row := newOutboxRow(incID, "creator@com.com")
	require.NoError(t, d.Enqueue(ctx, nil, row))
	claimed, err := d.ClaimPending(ctx, nil, 10, "pod-1", time.Minute)
	require.NoError(t, err)
	lease := findClaimed(t, claimed, row.DedupKey).Lease()

	require.NoError(t, d.MarkSent(ctx, nil, lease))

	stored := fetchRow(t, g, lease.ID)
	assert.Equal(t, db.NotificationStatusSent, stored.Status)
	assert.Nil(t, stored.LockedBy)
	assert.Nil(t, stored.LockedAt)
	assert.Nil(t, stored.LastError)
}

func TestMarkSent_NotFound(t *testing.T) {
	ctx := context.Background()
	d, _ := newNotifDB(t)
	err := d.MarkSent(ctx, nil, db.Lease{})
	require.ErrorIs(t, err, db.ErrNotificationNotFound)
}

func TestMarkFailed_RetryWhenAttemptsRemain(t *testing.T) {
	ctx := context.Background()
	d, g := newNotifDB(t)
	incID := seedIncident(t, d)

	row := newOutboxRow(incID, "creator@com.com")
	require.NoError(t, d.Enqueue(ctx, nil, row))
	claimed, err := d.ClaimPending(ctx, nil, 10, "pod-1", time.Minute)
	require.NoError(t, err)
	lease := findClaimed(t, claimed, row.DedupKey).Lease() // attempts is now 1

	retryAt := time.Now().UTC().Add(5 * time.Minute)
	backoff := func(_ int) time.Time { return retryAt }
	require.NoError(t, d.MarkFailed(ctx, nil, lease, "smtp timeout", 5, backoff))

	stored := fetchRow(t, g, lease.ID)
	assert.Equal(t, db.NotificationStatusPending, stored.Status)
	require.NotNil(t, stored.NextAttemptAt)
	require.NotNil(t, stored.LastError)
	assert.Equal(t, "smtp timeout", *stored.LastError)
	assert.Nil(t, stored.LockedBy)
}

func TestMarkFailed_FinalWhenAttemptsExhausted(t *testing.T) {
	ctx := context.Background()
	d, g := newNotifDB(t)
	incID := seedIncident(t, d)

	row := newOutboxRow(incID, "creator@com.com")
	require.NoError(t, d.Enqueue(ctx, nil, row))
	claimed, err := d.ClaimPending(ctx, nil, 10, "pod-1", time.Minute)
	require.NoError(t, err)
	lease := findClaimed(t, claimed, row.DedupKey).Lease() // attempts is now 1

	backoff := func(_ int) time.Time { return time.Now().UTC().Add(time.Minute) }
	// maxAttempts=1, current attempts=1 -> final failure.
	require.NoError(t, d.MarkFailed(ctx, nil, lease, "permanent", 1, backoff))

	stored := fetchRow(t, g, lease.ID)
	assert.Equal(t, db.NotificationStatusFailed, stored.Status)
	assert.Nil(t, stored.NextAttemptAt)
	require.NotNil(t, stored.LastError)
	assert.Equal(t, "permanent", *stored.LastError)
}

func TestFinalize_RejectsLeaseLostToRecovery(t *testing.T) {
	ctx := context.Background()
	d, g := newNotifDB(t)
	incID := seedIncident(t, d)

	row := newOutboxRow(incID, "creator@com.com")
	require.NoError(t, d.Enqueue(ctx, nil, row))
	claimed, err := d.ClaimPending(ctx, nil, 10, "pod-1", time.Minute)
	require.NoError(t, err)
	old := findClaimed(t, claimed, row.DedupKey).Lease()

	// The send outlives its lease: the row is recovered and handed to another pod.
	stale := time.Now().UTC().Add(-10 * time.Minute)
	updateOutbox(t, g, map[string]any{"locked_at": stale}, "id", old.ID)
	_, err = d.RecoverStaleProcessing(ctx, nil, time.Minute, 5)
	require.NoError(t, err)

	reclaimed, err := d.ClaimPending(ctx, nil, 10, "pod-2", time.Minute)
	require.NoError(t, err)
	fresh := findClaimed(t, reclaimed, row.DedupKey).Lease()
	require.Equal(t, old.ID, fresh.ID)
	require.Greater(t, fresh.Attempts, old.Attempts, "re-claim must bump the lease epoch")

	backoff := func(_ int) time.Time { return time.Now().UTC().Add(time.Minute) }
	require.ErrorIs(t, d.MarkFailed(ctx, nil, old, "smtp timeout", 5, backoff), db.ErrNotificationLeaseLost)
	require.ErrorIs(t, d.MarkSent(ctx, nil, old), db.ErrNotificationLeaseLost)
	require.ErrorIs(t, d.MarkFailedTerminal(ctx, nil, old, "permanent"), db.ErrNotificationLeaseLost)

	stored := fetchRow(t, g, fresh.ID)
	assert.Equal(t, db.NotificationStatusProcessing, stored.Status, "the new owner's row must stay untouched")
	require.NotNil(t, stored.LockedBy)
	assert.Equal(t, "pod-2", *stored.LockedBy)
	assert.Nil(t, stored.LastError)

	require.NoError(t, d.MarkSent(ctx, nil, fresh))
	stored = fetchRow(t, g, fresh.ID)
	assert.Equal(t, db.NotificationStatusSent, stored.Status)
	assert.Nil(t, stored.LockedBy)
}

func TestRecoverStaleProcessing_ReturnsToPending(t *testing.T) {
	ctx := context.Background()
	d, g := newNotifDB(t)
	incID := seedIncident(t, d)

	row := newOutboxRow(incID, "creator@com.com")
	require.NoError(t, d.Enqueue(ctx, nil, row))
	claimed, err := d.ClaimPending(ctx, nil, 10, "pod-1", time.Minute)
	require.NoError(t, err)
	id := findClaimed(t, claimed, row.DedupKey).ID

	// Simulate a crashed pod: push locked_at far into the past.
	stale := time.Now().UTC().Add(-10 * time.Minute)
	updateOutbox(t, g, map[string]any{"locked_at": stale}, "id", id)

	recovered, err := d.RecoverStaleProcessing(ctx, nil, time.Minute, 5)
	require.NoError(t, err)
	require.True(t, containsID(recovered, id), "stale row must be recovered")

	stored := fetchRow(t, g, id)
	assert.Equal(t, db.NotificationStatusPending, stored.Status)
	assert.Nil(t, stored.LockedBy)
	assert.Nil(t, stored.LockedAt)
	require.NotNil(t, stored.NextAttemptAt)
}

func TestRecoverStaleProcessing_FinalWhenAttemptsExhausted(t *testing.T) {
	ctx := context.Background()
	d, g := newNotifDB(t)
	incID := seedIncident(t, d)

	row := newOutboxRow(incID, "creator@com.com")
	require.NoError(t, d.Enqueue(ctx, nil, row))
	claimed, err := d.ClaimPending(ctx, nil, 10, "pod-1", time.Minute)
	require.NoError(t, err)
	id := findClaimed(t, claimed, row.DedupKey).ID // attempts is now 1

	stale := time.Now().UTC().Add(-10 * time.Minute)
	updateOutbox(t, g, map[string]any{"locked_at": stale}, "id", id)

	// maxAttempts=1 with attempts=1 -> recovery marks it failed.
	recovered, err := d.RecoverStaleProcessing(ctx, nil, time.Minute, 1)
	require.NoError(t, err)
	require.True(t, containsID(recovered, id))

	stored := fetchRow(t, g, id)
	assert.Equal(t, db.NotificationStatusFailed, stored.Status)
}

// findClaimed returns the claimed row for a dedup key, carrying the lease that its
// finalize calls must present.
func findClaimed(t *testing.T, rows []db.NotificationOutbox, dedupKey string) db.NotificationOutbox {
	t.Helper()
	for i := range rows {
		if rows[i].DedupKey == dedupKey {
			return rows[i]
		}
	}
	require.FailNow(t, "claimed row not found for dedup key: "+dedupKey)
	return db.NotificationOutbox{}
}

func containsID(rows []db.NotificationOutbox, id uint) bool {
	for i := range rows {
		if rows[i].ID == id {
			return true
		}
	}
	return false
}
