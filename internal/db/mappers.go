package db

import (
	"context"
	"time"

	entsql "entgo.io/ent/dialect/sql"

	"github.com/stackmon/otc-status-dashboard/ent"
	"github.com/stackmon/otc-status-dashboard/ent/incidentstatus"
	"github.com/stackmon/otc-status-dashboard/internal/event"
)

// The facade keeps exposing the domain structs from models.go; Ent entities
// never leave this package. These mappers are the only place where both
// representations meet.

func optTimePtr(v time.Time) *time.Time {
	if v.IsZero() {
		return nil
	}
	return &v
}

func timePtr(v time.Time) *time.Time {
	return &v
}

func intPtr(v int) *int {
	return &v
}

func incidentFromEnt(e *ent.Incident) *Incident {
	inc := &Incident{
		ID:           uint(e.ID),
		Text:         &e.Text,
		Description:  e.Description,
		StartDate:    timePtr(e.StartDate),
		EndDate:      optTimePtr(e.EndDate),
		Impact:       intPtr(e.Impact),
		Status:       event.Status(e.Status),
		System:       e.System,
		Type:         e.Type.String(),
		CreatedAt:    optTimePtr(e.CreatedAt),
		ModifiedAt:   optTimePtr(e.ModifiedAt),
		DeletedAt:    optTimePtr(e.DeletedAt),
		CreatedBy:    e.CreatedBy,
		ContactEmail: e.ContactEmail,
		Version:      intPtr(e.Version),
	}

	if e.Edges.Components != nil {
		inc.Components = make([]Component, 0, len(e.Edges.Components))
		for _, c := range e.Edges.Components {
			inc.Components = append(inc.Components, componentFromEnt(c))
		}
	}

	return inc
}

func incidentStatusFromEnt(e *ent.IncidentStatus) IncidentStatus {
	return IncidentStatus{
		ID:         uint(e.ID),
		IncidentID: uint(e.IncidentID),
		Status:     event.Status(e.Status),
		Text:       e.Text,
		Timestamp:  e.Timestamp,
		CreatedAt:  optTimePtr(e.CreatedAt),
		ModifiedAt: optTimePtr(e.ModifiedAt),
		DeletedAt:  optTimePtr(e.DeletedAt),
		CreatedBy:  e.CreatedBy,
		ModifiedBy: e.ModifiedBy,
	}
}

func componentFromEnt(e *ent.Component) Component {
	c := Component{
		ID:         uint(e.ID),
		Name:       e.Name,
		CreatedAt:  optTimePtr(e.CreatedAt),
		ModifiedAt: optTimePtr(e.ModifiedAt),
		DeletedAt:  optTimePtr(e.DeletedAt),
	}

	if e.Edges.Attributes != nil {
		c.Attrs = make([]ComponentAttr, 0, len(e.Edges.Attributes))
		for _, a := range e.Edges.Attributes {
			c.Attrs = append(c.Attrs, componentAttrFromEnt(a))
		}
	}

	if e.Edges.Incidents != nil {
		c.Incidents = make([]*Incident, 0, len(e.Edges.Incidents))
		for _, i := range e.Edges.Incidents {
			c.Incidents = append(c.Incidents, incidentFromEnt(i))
		}
	}

	return c
}

func componentAttrFromEnt(e *ent.ComponentAttr) ComponentAttr {
	return ComponentAttr{
		ID:          uint(e.ID),
		ComponentID: uint(e.ComponentID),
		Name:        e.Name,
		Value:       e.Value,
	}
}

// incidentStatusChunkSize caps the IN predicate because PostgreSQL rejects
// queries with more than 65535 bound parameters.
const incidentStatusChunkSize = 1000

// statusesByIncident loads the update history for the given incidents. The Ent
// schema has no status edge (production carries no foreign key on
// incident_status), so callers attach the result themselves.
func (db *DB) statusesByIncident(ctx context.Context, ids []int) (map[int][]IncidentStatus, error) {
	grouped := make(map[int][]IncidentStatus, len(ids))
	if len(ids) == 0 {
		return grouped, nil
	}

	for start := 0; start < len(ids); start += incidentStatusChunkSize {
		end := min(start+incidentStatusChunkSize, len(ids))

		rows, err := db.e.IncidentStatus.Query().
			Where(incidentstatus.IncidentIDIn(ids[start:end]...)).
			Order(incidentstatus.ByID(entsql.OrderAsc())).
			All(ctx)
		if err != nil {
			return nil, err
		}

		for _, r := range rows {
			grouped[r.IncidentID] = append(grouped[r.IncidentID], incidentStatusFromEnt(r))
		}
	}

	return grouped, nil
}

func attachStatuses(incidents []*Incident, grouped map[int][]IncidentStatus) {
	for _, inc := range incidents {
		if s, ok := grouped[int(inc.ID)]; ok {
			inc.Statuses = s
			continue
		}
		inc.Statuses = []IncidentStatus{}
	}
}

func notificationOutboxFromEnt(e *ent.NotificationOutbox) NotificationOutbox {
	row := NotificationOutbox{
		ID:         uint(e.ID),
		Kind:       e.Kind,
		IncidentID: uint(e.IncidentID),
		Recipient:  e.Recipient,
		Payload:    e.Payload,
		ChangeID:   e.ChangeID,
		DedupKey:   e.DedupKey,
		Status:     e.Status,
		Attempts:   e.Attempts,
		CreatedAt:  e.CreatedAt,
		UpdatedAt:  e.UpdatedAt,
	}
	if !e.NextAttemptAt.IsZero() {
		row.NextAttemptAt = &e.NextAttemptAt
	}
	if e.LockedBy != "" {
		row.LockedBy = &e.LockedBy
	}
	if !e.LockedAt.IsZero() {
		row.LockedAt = &e.LockedAt
	}
	if e.LastError != "" {
		row.LastError = &e.LastError
	}
	return row
}

func incidentIDs(rows []*ent.Incident) []int {
	ids := make([]int, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	return ids
}
