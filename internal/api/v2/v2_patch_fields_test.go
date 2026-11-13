package v2

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackmon/otc-status-dashboard/internal/db"
	"github.com/stackmon/otc-status-dashboard/internal/event"
)

func TestUpdateFieldsEndDate(t *testing.T) {
	updateDate := time.Date(2026, 11, 13, 13, 10, 33, 0, time.UTC)
	plannedEnd := updateDate.Add(48 * time.Hour)
	laterEnd := plannedEnd.Add(24 * time.Hour)
	impact := 0

	tests := []struct {
		name      string
		storedEnd *time.Time
		income    PatchIncidentData
		expected  *time.Time
	}{
		{
			name: "completed info event without any end date ends at the update date",
			income: PatchIncidentData{
				Status:     event.InfoCompleted,
				UpdateDate: updateDate,
			},
			expected: &updateDate,
		},
		{
			name:      "stored end date is kept when the patch omits one",
			storedEnd: &plannedEnd,
			income: PatchIncidentData{
				Status:     event.InfoCompleted,
				UpdateDate: updateDate,
			},
			expected: &plannedEnd,
		},
		{
			name:      "explicit end date wins over the stored one",
			storedEnd: &plannedEnd,
			income: PatchIncidentData{
				Status:     event.InfoCompleted,
				UpdateDate: updateDate,
				EndDate:    &laterEnd,
			},
			expected: &laterEnd,
		},
		{
			name: "active info event keeps an open end",
			income: PatchIncidentData{
				Status:     event.InfoActive,
				UpdateDate: updateDate,
			},
			expected: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stored := &db.Incident{Type: event.TypeInformation, Impact: &impact, EndDate: tc.storedEnd}

			updateFields(&tc.income, stored)

			require.Equal(t, tc.expected == nil, stored.EndDate == nil)
			if tc.expected != nil {
				assert.True(t, tc.expected.Equal(*stored.EndDate))
			}
		})
	}
}
