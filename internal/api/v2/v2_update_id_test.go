package v2

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackmon/otc-status-dashboard/internal/db"
	"github.com/stackmon/otc-status-dashboard/internal/event"
)

func TestMapEventUpdatesKeepsPersistentIDs(t *testing.T) {
	statuses := []db.IncidentStatus{
		{ID: 7, Status: statusDescription, Text: "current description"},
		{ID: 9, Status: event.IncidentObserving, Text: "second update"},
		{ID: 8, Status: event.IncidentDetected, Text: "first update"},
	}

	updates, description := mapEventUpdates(
		statuses, true, event.IncidentDetected, nil, event.TypeIncident,
	)

	require.Len(t, updates, 2)
	assert.Equal(t, "current description", description)
	assert.Equal(t, 9, updates[0].ID)
	assert.Equal(t, 8, updates[1].ID)
	assert.NotContains(t, []int{updates[0].ID, updates[1].ID}, 7)
}

func TestSelectEventUpdate(t *testing.T) {
	updates := []db.IncidentStatus{
		{ID: 41, Status: statusDescription, Text: "description"},
		{ID: 43, Status: event.IncidentObserving, Text: "second update"},
		{ID: 42, Status: event.IncidentDetected, Text: "first update"},
	}

	t.Run("addresses a row by its ID regardless of order", func(t *testing.T) {
		selected, ok := selectEventUpdate(updates, 43)

		require.True(t, ok)
		assert.Equal(t, uint(43), selected.ID)
		assert.Equal(t, "second update", selected.Text)
	})

	t.Run("description row is not addressable", func(t *testing.T) {
		_, ok := selectEventUpdate(updates, 41)

		assert.False(t, ok)
	})

	t.Run("unknown ID", func(t *testing.T) {
		_, ok := selectEventUpdate(updates, 99)

		assert.False(t, ok)
	})
}
