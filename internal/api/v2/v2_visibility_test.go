package v2

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/stackmon/otc-status-dashboard/internal/db"
	"github.com/stackmon/otc-status-dashboard/internal/event"
)

func TestMapEventUpdates_FiltersInternalStatuses(t *testing.T) {
	testTime := time.Now().UTC()

	statuses := []db.IncidentStatus{
		{ID: 1, Status: "pending_review", Text: "Pending", Timestamp: testTime},
		{ID: 2, Status: "reviewed", Text: "Reviewed", Timestamp: testTime},
		{ID: 3, Status: "planned", Text: "Planned", Timestamp: testTime},
		{ID: 4, Status: "in_progress", Text: "In progress", Timestamp: testTime},
	}

	t.Run("authenticated sees all statuses", func(t *testing.T) {
		updates, _ := mapEventUpdates(statuses, true, event.MaintenancePlanned, nil, event.TypeMaintenance)
		assert.Len(t, updates, 4)
		assert.Equal(t, "pending_review", string(updates[0].Status))
		assert.Equal(t, "reviewed", string(updates[1].Status))
	})

	t.Run("unauthenticated sees only public statuses", func(t *testing.T) {
		updates, _ := mapEventUpdates(statuses, false, event.MaintenancePlanned, nil, event.TypeMaintenance)
		assert.Len(t, updates, 2)
		assert.Equal(t, "planned", string(updates[0].Status))
		assert.Equal(t, "in_progress", string(updates[1].Status))
	})

	t.Run("IDs are sequential after filtering", func(t *testing.T) {
		updates, _ := mapEventUpdates(statuses, false, event.MaintenancePlanned, nil, event.TypeMaintenance)
		for i, u := range updates {
			assert.Equal(t, i, u.ID)
		}
	})
}

func TestMapEventUpdates_DescriptionRows(t *testing.T) {
	testTime := time.Now().UTC()

	statuses := []db.IncidentStatus{
		{ID: 1, Status: "planned", Text: "Planned", Timestamp: testTime},
		{ID: 2, Status: "description", Text: "First description", Timestamp: testTime},
		{ID: 3, Status: "in_progress", Text: "In progress", Timestamp: testTime},
		{ID: 4, Status: "description", Text: "Latest description", Timestamp: testTime},
	}

	t.Run("description rows are removed and latest text is returned", func(t *testing.T) {
		updates, description := mapEventUpdates(statuses, true, event.MaintenancePlanned, nil, event.TypeMaintenance)
		assert.Len(t, updates, 2)
		assert.Equal(t, "planned", string(updates[0].Status))
		assert.Equal(t, "in_progress", string(updates[1].Status))
		assert.Equal(t, "Latest description", description)
	})

	t.Run("IDs are sequential after description removal", func(t *testing.T) {
		updates, _ := mapEventUpdates(statuses, false, event.MaintenancePlanned, nil, event.TypeMaintenance)
		for i, u := range updates {
			assert.Equal(t, i, u.ID)
		}
	})

	t.Run("no description rows returns empty description", func(t *testing.T) {
		_, description := mapEventUpdates(statuses[:1], true, event.MaintenancePlanned, nil, event.TypeMaintenance)
		assert.Empty(t, description)
	})
}

func TestMapEventUpdates_NormalizesStatuses(t *testing.T) {
	testTime := time.Now().UTC()
	endDate := testTime.Add(time.Hour)

	statuses := []db.IncidentStatus{
		{ID: 1, Status: "analyzing", Text: "Analysing", Timestamp: testTime},
		{ID: 2, Status: "in progress", Text: "In progress", Timestamp: testTime},
		{ID: 3, Status: "scheduled", Text: "Scheduled", Timestamp: testTime},
	}

	updates, _ := mapEventUpdates(statuses, true, event.MaintenancePlanned, &endDate, event.TypeMaintenance)
	assert.Equal(t, "analysing", string(updates[0].Status))
	assert.Equal(t, "in_progress", string(updates[1].Status))
	assert.Equal(t, "planned", string(updates[2].Status))

	systemStatuses := []db.IncidentStatus{
		{ID: 1, Status: event.OutDatedSystem, Text: "Moved", Timestamp: testTime},
	}
	systemUpdates, _ := mapEventUpdates(systemStatuses, true, event.MaintenancePlanned, &endDate, event.TypeMaintenance)
	assert.Equal(t, event.MaintenanceCompleted, systemUpdates[0].Status)
}

func TestEventStatus(t *testing.T) {
	testTime := time.Now().UTC()
	endDate := testTime.Add(time.Hour)
	past := testTime.Add(-time.Hour)

	t.Run("changed on a closed incident collapses to the previous status", func(t *testing.T) {
		inc := &db.Incident{
			Type:    event.TypeIncident,
			Status:  event.IncidentChanged,
			EndDate: &endDate,
			Statuses: []db.IncidentStatus{
				{Status: event.IncidentDetected, Timestamp: past},
				{Status: event.IncidentResolved, Timestamp: past},
			},
		}
		assert.Equal(t, event.IncidentResolved, eventStatus(inc))
	})

	t.Run("impact changed on an open incident collapses to the previous status", func(t *testing.T) {
		inc := &db.Incident{
			Type:   event.TypeIncident,
			Status: event.IncidentImpactChanged,
			Statuses: []db.IncidentStatus{
				{Status: event.IncidentDetected, Timestamp: past},
				{Status: event.IncidentAnalysing, Timestamp: past},
			},
		}
		assert.Equal(t, event.IncidentAnalysing, eventStatus(inc))
	})

	t.Run("a plain status is returned normalized", func(t *testing.T) {
		inc := &db.Incident{
			Type:     event.TypeIncident,
			Status:   "analyzing",
			Statuses: []db.IncidentStatus{{Status: "analyzing", Timestamp: past}},
		}
		assert.Equal(t, event.IncidentAnalysing, eventStatus(inc))
	})

	t.Run("SYSTEM without an end date is kept verbatim", func(t *testing.T) {
		inc := &db.Incident{
			Type:     event.TypeIncident,
			Status:   event.OutDatedSystem,
			Statuses: []db.IncidentStatus{{Status: event.OutDatedSystem, Timestamp: past}},
		}
		assert.Equal(t, event.OutDatedSystem, eventStatus(inc))
	})
}

func TestEventStatus_MatchesLastUpdate(t *testing.T) {
	testTime := time.Now().UTC()
	endDate := testTime.Add(time.Hour)

	statuses := []db.IncidentStatus{
		{Status: event.IncidentDetected, Text: "detected", Timestamp: testTime},
		{Status: event.IncidentResolved, Text: "resolved", Timestamp: testTime},
		{Status: event.IncidentChanged, Text: "changed", Timestamp: testTime},
	}
	inc := &db.Incident{
		Type:     event.TypeIncident,
		Status:   event.IncidentChanged,
		EndDate:  &endDate,
		Statuses: statuses,
	}

	updates, _ := mapEventUpdates(statuses, true, inc.Status, inc.EndDate, inc.Type)
	assert.Equal(t, eventStatus(inc), updates[len(updates)-1].Status)
}

func TestEventStatus_DoesNotDependOnVisibility(t *testing.T) {
	testTime := time.Now().UTC()

	statuses := []db.IncidentStatus{
		{Status: event.MaintenancePlanned, Text: "planned", Timestamp: testTime},
		{Status: event.MaintenanceReviewed, Text: "reviewed", Timestamp: testTime},
	}
	inc := &db.Incident{Type: event.TypeMaintenance, Status: event.MaintenanceReviewed, Statuses: statuses}

	assert.Equal(t, event.MaintenanceReviewed, eventStatus(inc))

	// The unauthenticated view filters the trailing internal row, so the last
	// visible update can differ from the view-independent event status.
	public, _ := mapEventUpdates(statuses, false, inc.Status, inc.EndDate, inc.Type)
	assert.Equal(t, event.MaintenancePlanned, public[len(public)-1].Status)
}

func TestMapEventUpdates_ChangedStatusesKeepPrevious(t *testing.T) {
	testTime := time.Now().UTC()

	statuses := []db.IncidentStatus{
		{ID: 1, Status: "analysing", Text: "Analysing", Timestamp: testTime},
		{ID: 2, Status: "impact changed", Text: "Impact changed", Timestamp: testTime},
		{ID: 3, Status: "changed", Text: "Changed", Timestamp: testTime},
	}

	updates, _ := mapEventUpdates(statuses, true, event.IncidentAnalysing, nil, event.TypeIncident)
	assert.Len(t, updates, 3)
	assert.Equal(t, "analysing", string(updates[0].Status))
	assert.Equal(t, "analysing", string(updates[1].Status))
	assert.Equal(t, "analysing", string(updates[2].Status))
}

func TestMapEventUpdates_LeadingChangedUsesEventStatus(t *testing.T) {
	testTime := time.Now().UTC()

	statuses := []db.IncidentStatus{
		{ID: 1, Status: "changed", Text: "Changed", Timestamp: testTime},
	}

	updates, _ := mapEventUpdates(statuses, true, event.IncidentResolved, nil, event.TypeIncident)
	assert.Len(t, updates, 1)
	assert.Equal(t, event.IncidentResolved, updates[0].Status)
}

func TestNormalizeStatus(t *testing.T) {
	endDate := time.Now().UTC()

	tests := []struct {
		name      string
		raw       event.Status
		endDate   *time.Time
		eventType string
		want      event.Status
	}{
		{name: "analyzing to analysing", raw: "analyzing", want: event.IncidentAnalysing},
		{name: "in progress to in_progress", raw: "in progress", want: event.MaintenanceInProgress},
		{name: "scheduled to planned", raw: "scheduled", want: event.MaintenancePlanned},
		{name: "SYSTEM incident with end date", raw: event.OutDatedSystem, endDate: &endDate, eventType: event.TypeIncident, want: event.IncidentResolved},
		{name: "SYSTEM maintenance with end date", raw: event.OutDatedSystem, endDate: &endDate, eventType: event.TypeMaintenance, want: event.MaintenanceCompleted},
		{name: "SYSTEM info with end date", raw: event.OutDatedSystem, endDate: &endDate, eventType: event.TypeInformation, want: event.InfoCompleted},
		{name: "SYSTEM without end date passes through", raw: event.OutDatedSystem, want: event.OutDatedSystem},
		{name: "changed passes through", raw: event.IncidentChanged, want: event.IncidentChanged},
		{name: "impact changed passes through", raw: event.IncidentImpactChanged, want: event.IncidentImpactChanged},
		{name: "canonical status passes through", raw: event.IncidentResolved, want: event.IncidentResolved},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeStatus(tt.raw, tt.endDate, tt.eventType)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsCancelledWithoutPublicStatus(t *testing.T) {
	testTime := time.Now().UTC()

	tests := []struct {
		name     string
		incident *db.Incident
		want     bool
	}{
		{
			name: "maintenance cancelled without public status",
			incident: &db.Incident{
				Type:   "maintenance",
				Status: "cancelled",
				Statuses: []db.IncidentStatus{
					{Status: "pending_review", Timestamp: testTime},
					{Status: "cancelled", Timestamp: testTime},
				},
			},
			want: true,
		},
		{
			name: "maintenance cancelled after planned",
			incident: &db.Incident{
				Type:   "maintenance",
				Status: "cancelled",
				Statuses: []db.IncidentStatus{
					{Status: "pending_review", Timestamp: testTime},
					{Status: "planned", Timestamp: testTime},
					{Status: "cancelled", Timestamp: testTime},
				},
			},
			want: false,
		},
		{
			name: "maintenance cancelled after in_progress",
			incident: &db.Incident{
				Type:   "maintenance",
				Status: "cancelled",
				Statuses: []db.IncidentStatus{
					{Status: "planned", Timestamp: testTime},
					{Status: "in_progress", Timestamp: testTime},
					{Status: "cancelled", Timestamp: testTime},
				},
			},
			want: false,
		},
		{
			name: "info cancelled without active status",
			incident: &db.Incident{
				Type:   "info",
				Status: "cancelled",
				Statuses: []db.IncidentStatus{
					{Status: "planned", Timestamp: testTime},
					{Status: "cancelled", Timestamp: testTime},
				},
			},
			want: false,
		},
		{
			name: "info cancelled after active",
			incident: &db.Incident{
				Type:   "info",
				Status: "cancelled",
				Statuses: []db.IncidentStatus{
					{Status: "planned", Timestamp: testTime},
					{Status: "active", Timestamp: testTime},
					{Status: "cancelled", Timestamp: testTime},
				},
			},
			want: false,
		},
		{
			name: "incident type is never hidden",
			incident: &db.Incident{
				Type:   "incident",
				Status: "resolved",
				Statuses: []db.IncidentStatus{
					{Status: "detected", Timestamp: testTime},
					{Status: "resolved", Timestamp: testTime},
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isCancelledWithoutPublicStatus(tt.incident)
			assert.Equal(t, tt.want, got)
		})
	}
}
