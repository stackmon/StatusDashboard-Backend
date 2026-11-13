package db

import (
	"context"
	"time"
)

// componentDowntimeQuery aggregates impact-3 downtime per component and
// calendar month inside [start, end]. The month series is clipped to the window
// so an incident that crosses a boundary only contributes its in-window part.
const componentDowntimeQuery = `
WITH months AS (
    SELECT generate_series(
        date_trunc('month', $1::timestamp),
        date_trunc('month', $2::timestamp),
        interval '1 month'
    ) AS month_start
)
SELECT
    icr.component_id,
    TO_CHAR(m.month_start, 'YYYY-MM') AS month_key,
    SUM(
        EXTRACT(EPOCH FROM (
            LEAST(i.end_date, m.month_start + interval '1 month', $2::timestamp)
            - GREATEST(i.start_date, m.month_start, $1::timestamp)
        )) / 3600.0
    )::double precision AS hours
FROM incident i
JOIN incident_component_relation icr ON icr.incident_id = i.id
CROSS JOIN months m
WHERE i.impact = 3
  AND i.end_date IS NOT NULL
  AND i.start_date < $2::timestamp
  AND i.end_date > $1::timestamp
  AND i.start_date < m.month_start + interval '1 month'
  AND i.end_date > m.month_start
GROUP BY icr.component_id, m.month_start
ORDER BY icr.component_id, m.month_start`

// ComponentDowntime is the outage downtime one component accumulated in a
// calendar month of the requested availability window.
type ComponentDowntime struct {
	ComponentID uint
	Month       time.Time
	Hours       float64
}

// GetComponentDowntime aggregates outage downtime by component and month
// without loading the incident rows into the application.
func (db *DB) GetComponentDowntime(
	ctx context.Context, start, end time.Time,
) ([]ComponentDowntime, error) {
	rows, err := db.sql.QueryContext(ctx, componentDowntimeQuery, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	downtime := make([]ComponentDowntime, 0)
	for rows.Next() {
		var (
			componentID int
			monthKey    string
			hours       float64
		)
		if err = rows.Scan(&componentID, &monthKey, &hours); err != nil {
			return nil, err
		}
		month, parseErr := time.Parse("2006-01", monthKey)
		if parseErr != nil {
			return nil, parseErr
		}
		downtime = append(downtime, ComponentDowntime{
			ComponentID: uint(componentID),
			Month:       month,
			Hours:       hours,
		})
	}

	return downtime, rows.Err()
}
