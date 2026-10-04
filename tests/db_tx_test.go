package tests

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackmon/otc-status-dashboard/internal/db"
	"github.com/stackmon/otc-status-dashboard/internal/event"
)

func TestWithTx_CommitsIncidentAndOutboxAtomically(t *testing.T) {
	ctx := context.Background()
	d, g := newNotifDB(t)

	var incID uint
	err := d.WithTx(ctx, func(tx *db.Tx) error {
		id, e := d.SaveIncidentTx(ctx, tx, newMaintenanceIncident())
		if e != nil {
			return e
		}
		incID = id
		return d.Enqueue(ctx, tx, newOutboxRow(id, "creator@com.com"))
	})
	require.NoError(t, err)

	incCount := tableCount(t, g, "incident", "id = $1", incID)
	outCount := tableCount(t, g, "notification_outbox", "incident_id = $1", incID)
	assert.Equal(t, int64(1), incCount)
	assert.Equal(t, int64(1), outCount)
}

func TestWithTx_RollsBackBothOnError(t *testing.T) {
	ctx := context.Background()
	d, g := newNotifDB(t)
	sentinel := errors.New("boom")

	var incID uint
	var dedup string
	err := d.WithTx(ctx, func(tx *db.Tx) error {
		id, e := d.SaveIncidentTx(ctx, tx, newMaintenanceIncident())
		if e != nil {
			return e
		}
		incID = id
		row := newOutboxRow(id, "creator@com.com")
		dedup = row.DedupKey
		if e = d.Enqueue(ctx, tx, row); e != nil {
			return e
		}
		return sentinel // force rollback after both writes
	})
	require.ErrorIs(t, err, sentinel)

	incCount := tableCount(t, g, "incident", "id = $1", incID)
	outCount := tableCount(t, g, "notification_outbox", "dedup_key = $1", dedup)
	assert.Equal(t, int64(0), incCount, "incident rolled back")
	assert.Equal(t, int64(0), outCount, "no orphan email task")
}

func TestModifyIncidentTx_SharedTxWithEnqueue(t *testing.T) {
	ctx := context.Background()
	d, g := newNotifDB(t)

	incID := seedIncident(t, d)
	inc, err := d.GetIncident(ctx, int(incID))
	require.NoError(t, err)
	inc.Status = event.MaintenanceReviewed

	row := newOutboxRow(incID, "creator@com.com")
	err = d.WithTx(ctx, func(tx *db.Tx) error {
		if e := d.ModifyIncidentTx(ctx, tx, inc); e != nil {
			return e
		}
		return d.Enqueue(ctx, tx, row)
	})
	require.NoError(t, err)

	got, err := d.GetIncident(ctx, int(incID))
	require.NoError(t, err)
	assert.Equal(t, event.MaintenanceReviewed, got.Status)

	outCount := tableCount(t, g, "notification_outbox", "dedup_key = $1", row.DedupKey)
	assert.Equal(t, int64(1), outCount)
}

func TestModifyEventUpdateTx_UpdatesText(t *testing.T) {
	d, g := newNotifDB(t)

	incID := seedIncident(t, d)
	// Seed one status row for the incident.
	statusID := insertIncidentStatus(t, g, incID, string(event.MaintenancePendingReview), "original")

	var updated db.IncidentStatus
	err := d.WithTx(context.Background(), func(tx *db.Tx) error {
		u, e := d.ModifyEventUpdateTx(context.Background(), tx, db.IncidentStatus{
			ID: statusID, IncidentID: incID, Text: "patched",
		})
		if e != nil {
			return e
		}
		updated = u
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, "patched", updated.Text)
}

// newMaintenanceIncident builds a minimal maintenance incident for tx tests.
func newMaintenanceIncident() *db.Incident {
	text := "tx maintenance"
	start := time.Now().UTC()
	impact := 0
	return &db.Incident{
		Text:      &text,
		StartDate: &start,
		Impact:    &impact,
		System:    false,
		Type:      "maintenance",
	}
}
