package tests

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/stackmon/otc-status-dashboard/internal/conf"
	"github.com/stackmon/otc-status-dashboard/internal/db"
)

// newNotifDB returns the DB under test plus a raw gorm handle for seeding and
// verification against the real Postgres container.
func newNotifDB(t *testing.T) (*db.DB, *gorm.DB) {
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

	return d, g
}

// seedIncident inserts a minimal maintenance incident to satisfy the outbox FK
// and returns its id.
func seedIncident(t *testing.T, d *db.DB) uint {
	t.Helper()

	text := "notif-test maintenance"
	start := time.Now().UTC()
	impact := 0
	id, err := d.SaveIncident(&db.Incident{
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

func fetchRow(t *testing.T, g *gorm.DB, id uint) db.NotificationOutbox {
	t.Helper()
	var row db.NotificationOutbox
	require.NoError(t, g.First(&row, id).Error)
	return row
}

func TestEnqueue_Success(t *testing.T) {
	ctx := context.Background()
	d, g := newNotifDB(t)
	incID := seedIncident(t, d)

	row := newOutboxRow(incID, "creator@com.com")
	require.NoError(t, d.Enqueue(ctx, nil, row))

	var stored db.NotificationOutbox
	require.NoError(t, g.Where("dedup_key = ?", row.DedupKey).First(&stored).Error)
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
	id := findClaimedID(t, claimed, row.DedupKey)

	require.NoError(t, d.MarkSent(ctx, nil, id))

	stored := fetchRow(t, g, id)
	assert.Equal(t, db.NotificationStatusSent, stored.Status)
	assert.Nil(t, stored.LockedBy)
	assert.Nil(t, stored.LockedAt)
	assert.Nil(t, stored.LastError)
}

func TestMarkSent_NotFound(t *testing.T) {
	ctx := context.Background()
	d, _ := newNotifDB(t)
	err := d.MarkSent(ctx, nil, 0)
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
	id := findClaimedID(t, claimed, row.DedupKey) // attempts is now 1

	retryAt := time.Now().UTC().Add(5 * time.Minute)
	backoff := func(_ int) time.Time { return retryAt }
	require.NoError(t, d.MarkFailed(ctx, nil, id, "smtp timeout", 5, backoff))

	stored := fetchRow(t, g, id)
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
	id := findClaimedID(t, claimed, row.DedupKey) // attempts is now 1

	backoff := func(_ int) time.Time { return time.Now().UTC().Add(time.Minute) }
	// maxAttempts=1, current attempts=1 -> final failure.
	require.NoError(t, d.MarkFailed(ctx, nil, id, "permanent", 1, backoff))

	stored := fetchRow(t, g, id)
	assert.Equal(t, db.NotificationStatusFailed, stored.Status)
	assert.Nil(t, stored.NextAttemptAt)
	require.NotNil(t, stored.LastError)
	assert.Equal(t, "permanent", *stored.LastError)
}

func TestRecoverStaleProcessing_ReturnsToPending(t *testing.T) {
	ctx := context.Background()
	d, g := newNotifDB(t)
	incID := seedIncident(t, d)

	row := newOutboxRow(incID, "creator@com.com")
	require.NoError(t, d.Enqueue(ctx, nil, row))
	claimed, err := d.ClaimPending(ctx, nil, 10, "pod-1", time.Minute)
	require.NoError(t, err)
	id := findClaimedID(t, claimed, row.DedupKey)

	// Simulate a crashed pod: push locked_at far into the past.
	stale := time.Now().UTC().Add(-10 * time.Minute)
	require.NoError(t, g.Model(&db.NotificationOutbox{}).Where("id = ?", id).
		Update("locked_at", stale).Error)

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
	id := findClaimedID(t, claimed, row.DedupKey) // attempts is now 1

	stale := time.Now().UTC().Add(-10 * time.Minute)
	require.NoError(t, g.Model(&db.NotificationOutbox{}).Where("id = ?", id).
		Update("locked_at", stale).Error)

	// maxAttempts=1 with attempts=1 -> recovery marks it failed.
	recovered, err := d.RecoverStaleProcessing(ctx, nil, time.Minute, 1)
	require.NoError(t, err)
	require.True(t, containsID(recovered, id))

	stored := fetchRow(t, g, id)
	assert.Equal(t, db.NotificationStatusFailed, stored.Status)
}

func findClaimedID(t *testing.T, rows []db.NotificationOutbox, dedupKey string) uint {
	t.Helper()
	for i := range rows {
		if rows[i].DedupKey == dedupKey {
			return rows[i].ID
		}
	}
	require.FailNow(t, "claimed row not found for dedup key: "+dedupKey)
	return 0
}

func containsID(rows []db.NotificationOutbox, id uint) bool {
	for i := range rows {
		if rows[i].ID == id {
			return true
		}
	}
	return false
}
