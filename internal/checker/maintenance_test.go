package checker

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"github.com/stackmon/otc-status-dashboard/internal/db"
	"github.com/stackmon/otc-status-dashboard/internal/event"
)

func TestCalculateCurrentMntStatus(t *testing.T) {
	future := time.Now().UTC().Add(24 * time.Hour)
	farFuture := future.Add(48 * time.Hour)
	past := time.Now().UTC().Add(-24 * time.Hour)
	recentPast := time.Now().UTC().Add(-1 * time.Hour)

	tests := []struct {
		name           string
		status         event.Status
		startDate      time.Time
		endDate        time.Time
		history        *MntStatusHistory
		expectedStatus event.Status
	}{
		{
			name:           "Reviewed transitions to planned",
			status:         event.MaintenanceReviewed,
			startDate:      future,
			endDate:        farFuture,
			history:        &MntStatusHistory{hasReviewed: true},
			expectedStatus: event.MaintenancePlanned,
		},
		{
			name:           "Cancelled overrides reviewed",
			status:         event.MaintenanceReviewed,
			startDate:      future,
			endDate:        farFuture,
			history:        &MntStatusHistory{hasReviewed: true, hasCancelled: true},
			expectedStatus: event.MaintenanceCancelled,
		},
		{
			name:           "Planned with future start stays planned",
			status:         event.MaintenancePlanned,
			startDate:      future,
			endDate:        farFuture,
			history:        &MntStatusHistory{hasPlanned: true},
			expectedStatus: event.MaintenancePlanned,
		},
		{
			name:           "Planned with past start becomes in_progress",
			status:         event.MaintenancePlanned,
			startDate:      past,
			endDate:        future,
			history:        &MntStatusHistory{hasPlanned: true},
			expectedStatus: event.MaintenanceInProgress,
		},
		{
			name:           "Planned with past end becomes completed",
			status:         event.MaintenancePlanned,
			startDate:      past,
			endDate:        recentPast,
			history:        &MntStatusHistory{hasPlanned: true},
			expectedStatus: event.MaintenanceCompleted,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mn := &db.Incident{
				Status:    tc.status,
				StartDate: &tc.startDate,
				EndDate:   &tc.endDate,
			}

			result := calculateCurrentMntStatus(tc.history, mn)
			assert.Equal(t, tc.expectedStatus, result)
		})
	}
}

func TestCalculateCurrentMntStatusEmptyEndDate(t *testing.T) {
	future := time.Now().UTC().Add(24 * time.Hour)
	past := time.Now().UTC().Add(-1 * time.Hour)

	tests := []struct {
		name           string
		startDate      time.Time
		expectedStatus event.Status
	}{
		{
			name:           "Empty end date with past start stays in_progress",
			startDate:      past,
			expectedStatus: event.MaintenanceInProgress,
		},
		{
			name:           "Empty end date with future start stays planned",
			startDate:      future,
			expectedStatus: event.MaintenancePlanned,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mn := &db.Incident{
				Status:    event.MaintenanceInProgress,
				StartDate: &tc.startDate,
			}

			assert.Equal(t, tc.expectedStatus, calculateCurrentMntStatus(&MntStatusHistory{}, mn))
		})
	}
}

// TestEvaluateAndFixMntStatusEmptyEndDate covers the whole evaluation path for
// a maintenance without an end date, which used to panic on a nil dereference.
func TestEvaluateAndFixMntStatusEmptyEndDate(t *testing.T) {
	startDate := time.Now().UTC().Add(-1 * time.Hour)
	ch := &Checker{log: zap.NewNop()}
	mnt := &db.Incident{
		Status:    event.MaintenanceInProgress,
		StartDate: &startDate,
	}

	assert.Equal(t, event.MaintenanceInProgress, ch.evaluateAndFixMntStatus(mnt))
}

// TestFixMntMissedStatuses_Cancelled verifies that fixMntMissedStatuses does
// NOT fabricate a "planned" audit entry when a maintenance is cancelled before
// it ever entered the regular workflow (i.e. cancelled from pending_review),
// but DOES add the "planned" entry when the event progressed past reviewed.
func TestFixMntMissedStatuses_Cancelled(t *testing.T) {
	startDate := time.Now().UTC().Add(24 * time.Hour)

	tests := []struct {
		name           string
		history        *MntStatusHistory
		wantNewEntries int
	}{
		{
			name:           "cancelled from pending_review adds NO planned entry",
			history:        &MntStatusHistory{hasCancelled: true},
			wantNewEntries: 0,
		},
		{
			name:           "cancelled after reviewed adds planned entry",
			history:        &MntStatusHistory{hasReviewed: true, hasCancelled: true},
			wantNewEntries: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ch := &Checker{log: zap.NewNop()}
			mnt := &db.Incident{
				StartDate: &startDate,
				Statuses:  []db.IncidentStatus{},
			}

			initialLen := len(mnt.Statuses)
			ch.fixMntMissedStatuses(event.MaintenanceCancelled, tc.history, mnt)

			added := len(mnt.Statuses) - initialLen
			assert.Equal(t, tc.wantNewEntries, added,
				"unexpected number of new status entries for case: %s", tc.name)
		})
	}
}
