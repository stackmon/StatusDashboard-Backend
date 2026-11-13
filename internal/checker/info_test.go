package checker

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"github.com/stackmon/otc-status-dashboard/internal/db"
	"github.com/stackmon/otc-status-dashboard/internal/event"
)

func infoRows(statuses ...event.Status) []db.IncidentStatus {
	rows := make([]db.IncidentStatus, 0, len(statuses))
	for _, status := range statuses {
		rows = append(rows, db.IncidentStatus{Status: status})
	}

	return rows
}

func TestCalculateCurrentInfoStatus(t *testing.T) {
	future := time.Now().UTC().Add(24 * time.Hour)
	past := time.Now().UTC().Add(-24 * time.Hour)
	recentPast := time.Now().UTC().Add(-1 * time.Hour)

	tests := []struct {
		name           string
		status         event.Status
		startDate      time.Time
		endDate        *time.Time
		rows           []db.IncidentStatus
		expectedStatus event.Status
	}{
		{
			name:           "Planned before start",
			status:         event.InfoPlanned,
			startDate:      future,
			rows:           infoRows(event.InfoPlanned),
			expectedStatus: event.InfoPlanned,
		},
		{
			name:           "Active without end date",
			status:         event.InfoActive,
			startDate:      past,
			rows:           infoRows(event.InfoPlanned, event.InfoActive),
			expectedStatus: event.InfoActive,
		},
		{
			name:           "Completed without end date stays completed",
			status:         event.InfoCompleted,
			startDate:      past,
			rows:           infoRows(event.InfoPlanned, event.InfoActive, event.InfoCompleted),
			expectedStatus: event.InfoCompleted,
		},
		{
			name:           "Completed before the planned end stays completed",
			status:         event.InfoCompleted,
			startDate:      past,
			endDate:        &future,
			rows:           infoRows(event.InfoPlanned, event.InfoActive, event.InfoCompleted),
			expectedStatus: event.InfoCompleted,
		},
		{
			name:           "Update after completion reopens the event",
			status:         event.InfoActive,
			startDate:      past,
			endDate:        &recentPast,
			rows:           infoRows(event.InfoPlanned, event.InfoActive, event.InfoCompleted, event.InfoActive),
			expectedStatus: event.InfoActive,
		},
		{
			name:           "Cancelled short-circuits",
			status:         event.InfoCancelled,
			startDate:      future,
			rows:           infoRows(event.InfoPlanned, event.InfoCancelled),
			expectedStatus: event.InfoCancelled,
		},
		{
			name:           "Passed end date completes an unmaintained event",
			status:         event.InfoActive,
			startDate:      past,
			endDate:        &recentPast,
			rows:           infoRows(event.InfoPlanned, event.InfoActive),
			expectedStatus: event.InfoCompleted,
		},
		{
			name:           "Empty history follows the clock",
			status:         event.InfoPlanned,
			startDate:      past,
			expectedStatus: event.InfoActive,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			info := &db.Incident{
				Status:    tc.status,
				StartDate: &tc.startDate,
				EndDate:   tc.endDate,
				Statuses:  tc.rows,
			}

			assert.Equal(t, tc.expectedStatus,
				calculateCurrentInfoStatus(calculateInfoStatusHistory(info), info))
		})
	}
}

// Description rows are stored in the same table and must not be taken as the
// status the event was moved to.
func TestCalculateInfoStatusHistoryIgnoresNonStatusRows(t *testing.T) {
	info := &db.Incident{Statuses: []db.IncidentStatus{
		{Status: event.InfoPlanned},
		{Status: event.InfoActive},
		{Status: event.Status("description")},
		{Status: event.InfoCompleted},
	}}

	sHistory := calculateInfoStatusHistory(info)

	assert.Equal(t, event.InfoCompleted, sHistory.latest)
	assert.True(t, sHistory.hasCompleted)
}

func TestFixInfoMissedStatusesCompletedWithoutEndDate(t *testing.T) {
	startDate := time.Now().UTC().Add(-1 * time.Hour)
	ch := &Checker{log: zap.NewNop()}
	info := &db.Incident{
		Status:    event.InfoCompleted,
		StartDate: &startDate,
		Statuses:  []db.IncidentStatus{},
	}

	ch.fixInfoMissedStatuses(event.InfoCompleted, calculateInfoStatusHistory(info), info)

	added := info.Statuses[len(info.Statuses)-1]
	assert.Equal(t, event.InfoCompleted, added.Status)
	assert.False(t, added.Timestamp.IsZero())
}
