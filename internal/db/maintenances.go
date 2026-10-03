package db

import (
	entsql "entgo.io/ent/dialect/sql"

	"github.com/stackmon/otc-status-dashboard/ent/incident"
)

func (db *DB) GetMaintenances(after uint) ([]*Incident, error) {
	return db.getEventsByType(incident.TypeMaintenance, after, entsql.OrderAsc())
}
