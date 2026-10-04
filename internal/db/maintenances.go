package db

import (
	entsql "entgo.io/ent/dialect/sql"

	"github.com/stackmon/otc-status-dashboard/ent/incident"
)

func (db *DB) GetMaintenances() ([]*Incident, error) {
	return db.getEventsByType(incident.TypeMaintenance, entsql.OrderAsc())
}
