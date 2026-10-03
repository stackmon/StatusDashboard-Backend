package db

import (
	"context"

	entsql "entgo.io/ent/dialect/sql"

	"github.com/stackmon/otc-status-dashboard/ent"
	"github.com/stackmon/otc-status-dashboard/ent/component"
	"github.com/stackmon/otc-status-dashboard/ent/incident"
)

// incidentsByComponentAttrQuery lists incident ids matched through a component
// attribute, applying the public visibility rules inline.
const incidentsByComponentAttrQuery = `
SELECT incident.id
FROM incident
JOIN incident_component_relation icr ON icr.incident_id = incident.id
JOIN component_attribute ca ON ca.component_id = icr.component_id
WHERE ca.name = $1 AND ca.value = $2
  AND NOT (incident.type = $3 AND incident.status IN ($4, $5))
  AND NOT (incident.type = $6 AND incident.status = $7 AND NOT EXISTS (
      SELECT 1 FROM incident_status
      WHERE incident_status.incident_id = incident.id
        AND incident_status.status IN ($8, $9, $10, $11)))`

func scanIntColumn(ctx context.Context, q entsql.ExecQuerier, query string, args ...any) ([]int, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var values []int
	for rows.Next() {
		var v int
		if err = rows.Scan(&v); err != nil {
			return nil, err
		}
		values = append(values, v)
	}
	return values, rows.Err()
}

// incidentsByIDs loads incidents in the given order, repeating entries when the id
// list repeats, with the trimmed component payload the read paths expose.
func (db *DB) incidentsByIDs(ctx context.Context, ids []int) ([]*Incident, error) {
	if len(ids) == 0 {
		return []*Incident{}, nil
	}

	rows, err := db.e.Incident.Query().
		Where(incident.IDIn(ids...)).
		WithComponents(func(q *ent.ComponentQuery) {
			q.Select(component.FieldID, component.FieldName)
			q.WithAttributes()
		}).
		All(ctx)
	if err != nil {
		return nil, err
	}

	byID := make(map[int]*Incident, len(rows))
	for _, row := range rows {
		byID[row.ID] = incidentFromEnt(row)
	}

	incidents := make([]*Incident, 0, len(ids))
	for _, id := range ids {
		if inc, ok := byID[id]; ok {
			incidents = append(incidents, inc)
		}
	}

	grouped, err := db.statusesByIncident(ctx, ids)
	if err != nil {
		return nil, err
	}
	attachStatuses(incidents, grouped)

	return incidents, nil
}
