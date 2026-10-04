package db

import (
	entsql "entgo.io/ent/dialect/sql"

	"github.com/stackmon/otc-status-dashboard/ent/incident"
)

func (db *DB) GetInfoEvents() ([]*Incident, error) {
	return db.getEventsByType(incident.TypeInfo, entsql.OrderDesc())
}
