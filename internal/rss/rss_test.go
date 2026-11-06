package rss

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackmon/otc-status-dashboard/internal/db"
	"github.com/stackmon/otc-status-dashboard/internal/event"
)

func testTime() time.Time {
	return time.Date(2026, 11, 5, 10, 30, 0, 0, time.UTC)
}

func componentWithRegion(name, region string) db.Component {
	return db.Component{
		Name: name,
		Attrs: []db.ComponentAttr{
			{Name: "region", Value: region},
			{Name: "type", Value: "compute"},
		},
	}
}

func baseIncident(components []db.Component) *db.Incident {
	impact := 2
	text := "ECS outage"
	start := testTime()

	return &db.Incident{
		ID:         42,
		Text:       &text,
		Type:       event.TypeIncident,
		Impact:     &impact,
		StartDate:  &start,
		Components: components,
	}
}

// Incident events without components must not panic and must produce a
// generic item instead.
func TestCreateIncidentFeedItemsNoComponents(t *testing.T) {
	incident := baseIncident(nil)
	status := "fixing"
	incident.Statuses = []db.IncidentStatus{
		{Status: event.IncidentDetected, Text: "detected", Timestamp: testTime()},
		{Status: event.Status(status), Text: "we are on it", Timestamp: testTime().Add(time.Hour)},
	}

	items := createIncidentFeedItems(incident, "https://status.example.com")
	require.Len(t, items, 2, "main item plus one update (detected is skipped)")

	main := items[0]
	assert.Equal(t, "Status change to Major incident", main.Title)
	assert.Equal(t,
		"A Major incident was detected at 2026-11-05 10:30:00 UTC.",
		main.Description)
	assert.NotContains(t, main.Description, " for .", "description must not dangle on 'for'")
	assert.Equal(t, "https://status.example.com/incidents/42", main.Link.Href)

	upd := items[1]
	assert.Equal(t, "Update published for: ECS outage", upd.Title)
	assert.Equal(t,
		"An update was provided at 2026-11-05 11:30:00 UTC: fixing - we are on it",
		upd.Description)
}

func TestCreateIncidentFeedItemsSingleComponent(t *testing.T) {
	incident := baseIncident([]db.Component{componentWithRegion("ECS", "EU-DE")})
	incident.Statuses = []db.IncidentStatus{
		{Status: event.IncidentDetected, Text: "detected", Timestamp: testTime()},
		{Status: event.IncidentFixing, Text: "we are on it", Timestamp: testTime().Add(time.Hour)},
	}

	items := createIncidentFeedItems(incident, "https://status.example.com")
	require.Len(t, items, 2)

	assert.Equal(t, "ECS status changed to Major incident", items[0].Title)
	assert.Equal(t,
		"A Major incident was detected at 2026-11-05 10:30:00 UTC for ECS in EU-DE.",
		items[0].Description)
	assert.Equal(t,
		"An update was provided at 2026-11-05 11:30:00 UTC for ECS in EU-DE: fixing - we are on it",
		items[1].Description)
}

func TestCreateIncidentFeedItemsMultipleComponents(t *testing.T) {
	incident := baseIncident([]db.Component{
		componentWithRegion("ECS", "EU-DE"),
		componentWithRegion("EVS", "EU-NL"),
	})
	incident.Statuses = []db.IncidentStatus{
		{Status: event.IncidentDetected, Text: "detected", Timestamp: testTime()},
		{Status: event.IncidentFixing, Text: "we are on it", Timestamp: testTime().Add(time.Hour)},
	}

	items := createIncidentFeedItems(incident, "https://status.example.com")
	require.Len(t, items, 2)

	assert.Equal(t, "Status change of multiple services to Major incident", items[0].Title)
	assert.Equal(t,
		"A Major incident was detected at 2026-11-05 10:30:00 UTC for ECS in EU-DE, EVS in EU-NL.",
		items[0].Description)
	assert.Equal(t,
		"An update was provided at 2026-11-05 11:30:00 UTC for ECS (EU-DE), EVS (EU-NL): fixing - we are on it",
		items[1].Description)
}

// Maintenance events with no components must not panic either.
func TestCreateMaintenanceFeedItemsNoComponents(t *testing.T) {
	incident := baseIncident(nil)
	incident.Type = event.TypeMaintenance
	end := testTime().Add(2 * time.Hour)
	incident.EndDate = &end
	incident.Statuses = []db.IncidentStatus{
		{Status: event.MaintenancePlanned, Text: "planned", Timestamp: testTime()},
	}

	assert.NotPanics(t, func() {
		items := createFeedItems(incident, "https://status.example.com")
		require.NotEmpty(t, items)
	})
}

// createFeedItems must route by type without touching Components[0].
func TestCreateFeedItemsDispatchNoPanicOnEmptyComponents(t *testing.T) {
	for _, typ := range []string{event.TypeIncident, event.TypeMaintenance, event.TypeInformation} {
		t.Run(typ, func(t *testing.T) {
			incident := baseIncident([]db.Component{})
			incident.Type = typ
			end := testTime().Add(2 * time.Hour)
			incident.EndDate = &end
			incident.Statuses = []db.IncidentStatus{
				{Status: event.IncidentDetected, Text: "detected", Timestamp: testTime()},
			}

			assert.NotPanics(t, func() {
				createFeedItems(incident, "https://status.example.com")
			})
		})
	}
}
