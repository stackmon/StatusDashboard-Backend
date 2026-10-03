package db

import (
	"context"
	"time"

	"github.com/stackmon/otc-status-dashboard/ent"
	"github.com/stackmon/otc-status-dashboard/ent/incident"
)

func valueOr[T any](p *T, fallback T) T {
	if p == nil {
		return fallback
	}
	return *p
}

// insertIncidentStatus appends one update row. incident_status has no Ent edge to
// incident, so the row is inserted directly and the parent id is supplied by the
// caller when the update does not carry one.
func insertIncidentStatus(
	ctx context.Context, c *ent.Client, s *IncidentStatus, fallbackIncidentID uint,
) (int, error) {
	now := time.Now().UTC()
	createdAt := now
	if s.CreatedAt != nil {
		createdAt = *s.CreatedAt
	}
	modifiedAt := now
	if s.ModifiedAt != nil {
		modifiedAt = *s.ModifiedAt
	}
	incidentID := s.IncidentID
	if incidentID == 0 {
		incidentID = fallbackIncidentID
	}

	create := c.IncidentStatus.Create().
		SetIncidentID(int(incidentID)).
		SetStatus(string(s.Status)).
		SetText(s.Text).
		SetTimestamp(s.Timestamp).
		SetCreatedAt(createdAt).
		SetModifiedAt(modifiedAt)
	if s.CreatedBy != nil {
		create.SetCreatedBy(*s.CreatedBy)
	}
	if s.ModifiedBy != nil {
		create.SetModifiedBy(*s.ModifiedBy)
	}

	row, err := create.Save(ctx)
	if err != nil {
		return 0, err
	}

	s.CreatedAt = &createdAt
	s.ModifiedAt = &modifiedAt

	return row.ID, nil
}

// dropIncidentComponent removes a component from the in-memory association list so
// a following full save does not re-insert the join row that was just deleted.
func dropIncidentComponent(inc *Incident, componentID uint) {
	kept := inc.Components[:0]
	for i := range inc.Components {
		if inc.Components[i].ID == componentID {
			continue
		}
		kept = append(kept, inc.Components[i])
	}
	inc.Components = kept
}

// saveIncidentFull writes every incident column and reconciles the component
// associations. nil optional values clear the column instead of being skipped.
func saveIncidentFull(ctx context.Context, c *ent.Client, inc *Incident) error {
	if inc.StartDate == nil {
		return ErrIncidentStartDateRequired
	}

	update := c.Incident.UpdateOneID(int(inc.ID)).
		SetText(valueOr(inc.Text, "")).
		SetStartDate(*inc.StartDate).
		SetImpact(valueOr(inc.Impact, 0)).
		SetSystem(inc.System).
		SetType(incident.Type(inc.Type)).
		SetStatus(string(inc.Status)).
		SetVersion(valueOr(inc.Version, 1))

	applyIncidentOptionalColumns(update, inc)

	if _, err := update.Save(ctx); err != nil {
		return err
	}
	if err := reconcileIncidentComponents(ctx, c, inc); err != nil {
		return err
	}
	return insertNewIncidentStatuses(ctx, c, inc)
}

func applyIncidentOptionalColumns(update *ent.IncidentUpdateOne, inc *Incident) {
	if inc.Description != nil {
		update.SetDescription(*inc.Description)
	} else {
		update.ClearDescription()
	}
	if inc.EndDate != nil {
		update.SetEndDate(*inc.EndDate)
	} else {
		update.ClearEndDate()
	}
	if inc.CreatedAt != nil {
		update.SetCreatedAt(*inc.CreatedAt)
	} else {
		update.ClearCreatedAt()
	}
	if inc.ModifiedAt != nil {
		update.SetModifiedAt(*inc.ModifiedAt)
	} else {
		update.ClearModifiedAt()
	}
	if inc.DeletedAt != nil {
		update.SetDeletedAt(*inc.DeletedAt)
	} else {
		update.ClearDeletedAt()
	}
	if inc.CreatedBy != nil {
		update.SetCreatedBy(*inc.CreatedBy)
	} else {
		update.ClearCreatedBy()
	}
	if inc.ContactEmail != nil {
		update.SetContactEmail(*inc.ContactEmail)
	} else {
		update.ClearContactEmail()
	}
}

// reconcileIncidentComponents makes the stored component edge match inc.Components
// exactly; every incident read loads the edge unfiltered, so inc.Components is the
// full authoritative set.
func reconcileIncidentComponents(ctx context.Context, c *ent.Client, inc *Incident) error {
	want := make(map[int]struct{}, len(inc.Components))
	for i := range inc.Components {
		if inc.Components[i].ID != 0 {
			want[int(inc.Components[i].ID)] = struct{}{}
		}
	}

	currentIDs, err := c.Incident.Query().
		Where(incident.IDEQ(int(inc.ID))).
		QueryComponents().
		IDs(ctx)
	if err != nil {
		return err
	}

	current := make(map[int]struct{}, len(currentIDs))
	var extra []int
	for _, id := range currentIDs {
		current[id] = struct{}{}
		if _, ok := want[id]; !ok {
			extra = append(extra, id)
		}
	}

	var missing []int
	for i := range inc.Components {
		id := int(inc.Components[i].ID)
		if id == 0 {
			continue
		}
		if _, ok := current[id]; !ok {
			missing = append(missing, id)
		}
	}

	if len(extra) == 0 && len(missing) == 0 {
		return nil
	}

	update := c.Incident.UpdateOneID(int(inc.ID))
	if len(extra) > 0 {
		update.RemoveComponentIDs(extra...)
	}
	if len(missing) > 0 {
		update.AddComponentIDs(missing...)
	}
	_, err = update.Save(ctx)
	return err
}

func insertNewIncidentStatuses(ctx context.Context, c *ent.Client, inc *Incident) error {
	for i := range inc.Statuses {
		if inc.Statuses[i].ID != 0 {
			continue
		}
		id, err := insertIncidentStatus(ctx, c, &inc.Statuses[i], inc.ID)
		if err != nil {
			return err
		}
		inc.Statuses[i].ID = uint(id)
		inc.Statuses[i].IncidentID = inc.ID
	}
	return nil
}

// removeIncidentComponent deletes only the join row; the component itself stays.
func removeIncidentComponent(ctx context.Context, c *ent.Client, incidentID, componentID uint) error {
	_, err := c.Incident.UpdateOneID(int(incidentID)).
		RemoveComponentIDs(int(componentID)).
		Save(ctx)
	return err
}
