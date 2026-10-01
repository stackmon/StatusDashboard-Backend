package tests

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/stackmon/otc-status-dashboard/internal/db"
	"github.com/stackmon/otc-status-dashboard/internal/event"
)

func TestWithTx_CommitsIncidentAndOutboxAtomically(t *testing.T) {
	ctx := context.Background()
	d, g := newNotifDB(t)

	var incID uint
	err := d.WithTx(ctx, func(tx *gorm.DB) error {
		id, e := d.SaveIncidentTx(tx, newMaintenanceIncident())
		if e != nil {
			return e
		}
		incID = id
		return d.Enqueue(ctx, tx, newOutboxRow(id, "creator@com.com"))
	})
	require.NoError(t, err)

	var incCount, outCount int64
	require.NoError(t, g.Model(&db.Incident{}).Where("id = ?", incID).Count(&incCount).Error)
	require.NoError(t, g.Model(&db.NotificationOutbox{}).Where("incident_id = ?", incID).Count(&outCount).Error)
	assert.Equal(t, int64(1), incCount)
	assert.Equal(t, int64(1), outCount)
}

func TestWithTx_RollsBackBothOnError(t *testing.T) {
	ctx := context.Background()
	d, g := newNotifDB(t)
	sentinel := errors.New("boom")

	var incID uint
	var dedup string
	err := d.WithTx(ctx, func(tx *gorm.DB) error {
		id, e := d.SaveIncidentTx(tx, newMaintenanceIncident())
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

	var incCount, outCount int64
	require.NoError(t, g.Model(&db.Incident{}).Where("id = ?", incID).Count(&incCount).Error)
	require.NoError(t, g.Model(&db.NotificationOutbox{}).Where("dedup_key = ?", dedup).Count(&outCount).Error)
	assert.Equal(t, int64(0), incCount, "incident rolled back")
	assert.Equal(t, int64(0), outCount, "no orphan email task")
}

func TestModifyIncidentTx_SharedTxWithEnqueue(t *testing.T) {
	ctx := context.Background()
	d, g := newNotifDB(t)

	incID := seedIncident(t, d)
	inc, err := d.GetIncident(int(incID))
	require.NoError(t, err)
	inc.Status = event.MaintenanceReviewed

	row := newOutboxRow(incID, "creator@com.com")
	err = d.WithTx(ctx, func(tx *gorm.DB) error {
		if e := d.ModifyIncidentTx(tx, inc); e != nil {
			return e
		}
		return d.Enqueue(ctx, tx, row)
	})
	require.NoError(t, err)

	got, err := d.GetIncident(int(incID))
	require.NoError(t, err)
	assert.Equal(t, event.MaintenanceReviewed, got.Status)

	var outCount int64
	require.NoError(t, g.Model(&db.NotificationOutbox{}).Where("dedup_key = ?", row.DedupKey).Count(&outCount).Error)
	assert.Equal(t, int64(1), outCount)
}

func TestModifyEventUpdateTx_UpdatesText(t *testing.T) {
	d, g := newNotifDB(t)

	incID := seedIncident(t, d)
	// Seed one status row for the incident.
	status := db.IncidentStatus{IncidentID: incID, Status: event.MaintenancePendingReview, Text: "original"}
	require.NoError(t, g.Create(&status).Error)

	updated, err := d.ModifyEventUpdateTx(g, db.IncidentStatus{
		ID: status.ID, IncidentID: incID, Text: "patched",
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
