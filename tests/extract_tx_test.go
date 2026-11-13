package tests

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackmon/otc-status-dashboard/internal/db"
	"github.com/stackmon/otc-status-dashboard/internal/event"
)

// TestExtractComponentsRollsBackOnSecondPhaseFailure makes the last save of the
// extraction fail and checks that the incident created for the moved components,
// its statuses and the association change are rolled back with it.
func TestExtractComponentsRollsBackOnSecondPhaseFailure(t *testing.T) {
	ctx := context.Background()
	d, g := newNotifDB(t)

	comp := &db.Component{
		Name: "extract probe " + uuid.NewString(),
		Attrs: []db.ComponentAttr{
			{Name: "region", Value: "EU-DE"},
			{Name: "category", Value: "Compute"},
			{Name: "type", Value: "ecs"},
		},
	}
	_, err := d.SaveComponent(ctx, comp)
	require.NoError(t, err)

	start := time.Now().UTC()
	impact := 2
	oldText := "source incident " + uuid.NewString()

	oldID, err := d.SaveIncident(ctx, &db.Incident{
		Text:       &oldText,
		StartDate:  &start,
		Impact:     &impact,
		Status:     event.OutDatedSystem,
		Type:       event.TypeIncident,
		Components: []db.Component{*comp},
	})
	require.NoError(t, err)

	incOld, err := d.GetIncident(ctx, int(oldID))
	require.NoError(t, err)
	require.Len(t, incOld.Components, 1)

	// An empty text fails saveIncidentFull after the new incident, its statuses
	// and the association removal were written.
	emptyText := ""
	incOld.Text = &emptyText

	newText := "extracted incident " + uuid.NewString()
	description := "extract transaction probe"
	statusesBefore := tableCount(t, g, "incident_status", "TRUE")

	_, err = d.ExtractComponentsToNewIncident(
		ctx, []db.Component{*comp}, incOld, impact, newText, &description,
	)
	require.ErrorIs(t, err, db.ErrIncidentTextRequired)

	assert.Equal(t, int64(0), tableCount(t, g, "incident", "text = $1", newText))
	assert.Equal(t, statusesBefore, tableCount(t, g, "incident_status", "TRUE"))
	assert.Equal(t, int64(1), tableCount(t, g, "incident_component_relation",
		"incident_id = $1 AND component_id = $2", oldID, comp.ID))
}
