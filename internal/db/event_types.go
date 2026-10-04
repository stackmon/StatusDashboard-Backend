package db

import (
	"context"

	entsql "entgo.io/ent/dialect/sql"

	"github.com/stackmon/otc-status-dashboard/ent"
	"github.com/stackmon/otc-status-dashboard/ent/component"
	"github.com/stackmon/otc-status-dashboard/ent/incident"
)

// getEventsByType lists events of a single type with their update history.
func (db *DB) getEventsByType(eventType incident.Type, order entsql.OrderTermOption) ([]*Incident, error) {
	ctx := context.Background()

	query := db.e.Incident.Query().
		Where(incident.TypeEQ(eventType)).
		WithComponents(func(q *ent.ComponentQuery) {
			q.Select(component.FieldID)
		}).
		Order(incident.ByID(order))

	rows, err := query.All(ctx)
	if err != nil {
		return nil, err
	}

	incidents := make([]*Incident, 0, len(rows))
	for _, row := range rows {
		incidents = append(incidents, incidentFromEnt(row))
	}

	grouped, err := db.statusesByIncident(ctx, incidentIDs(rows))
	if err != nil {
		return nil, err
	}
	attachStatuses(incidents, grouped)

	return incidents, nil
}
