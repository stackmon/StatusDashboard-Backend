package db

import (
	"context"

	entsql "entgo.io/ent/dialect/sql"

	"github.com/stackmon/otc-status-dashboard/ent/incident"
)

func (db *DB) GetMaintenances(ctx context.Context) ([]*Incident, error) {
	return db.getEventsByType(ctx, incident.TypeMaintenance, entsql.OrderAsc())
}
