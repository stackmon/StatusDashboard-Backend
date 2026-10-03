package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"

	// Registered for its side effect: sql.Open("pgx", ...) needs the pgx database/sql driver.
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/stackmon/otc-status-dashboard/ent"
	"github.com/stackmon/otc-status-dashboard/ent/component"
	"github.com/stackmon/otc-status-dashboard/ent/componentattr"
	"github.com/stackmon/otc-status-dashboard/ent/incident"
	"github.com/stackmon/otc-status-dashboard/ent/incidentstatus"
	"github.com/stackmon/otc-status-dashboard/ent/predicate"
	"github.com/stackmon/otc-status-dashboard/internal/conf"
	"github.com/stackmon/otc-status-dashboard/internal/event"
)

// Connection pool defaults.
const (
	PublicAccess      = false
	AuthorizedAccess  = true
	dbMaxOpenConns    = 25
	dbMaxIdleConns    = 10
	dbConnMaxLifetime = 5 * time.Minute
	dbConnMaxIdleTime = 30 * time.Second
)

// DB is the storage facade over the Ent client and its connection pool.
type DB struct {
	sql *sql.DB
	e   *ent.Client
}

func New(c *conf.Config) (*DB, error) {
	sqlDB, err := sql.Open("pgx", c.DB)
	if err != nil {
		return nil, err
	}

	sqlDB.SetMaxOpenConns(dbMaxOpenConns)
	sqlDB.SetMaxIdleConns(dbMaxIdleConns)
	sqlDB.SetConnMaxLifetime(dbConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(dbConnMaxIdleTime)

	e := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, sqlDB)))

	return &DB{sql: sqlDB, e: e}, nil
}

func (db *DB) Close() error {
	return db.sql.Close()
}

type IncidentsParams struct {
	Types        []string
	Status       *event.Status
	StartDate    *time.Time
	EndDate      *time.Time
	Impact       *int
	IsSystem     *bool
	ComponentIDs []int
	LastCount    int
	IsActive     *bool
	Limit        *int
	Page         *int
}

func applyEventsFilters(params *IncidentsParams, isAuth bool) ([]predicate.Incident, error) {
	var preds []predicate.Incident

	if params.Types != nil {
		types := make([]incident.Type, 0, len(params.Types))
		for _, t := range params.Types {
			types = append(types, incident.Type(t))
		}
		preds = append(preds, incident.TypeIn(types...))
	}

	if params.Impact != nil {
		preds = append(preds, incident.ImpactEQ(*params.Impact))
	}

	if params.IsSystem != nil {
		preds = append(preds, incident.SystemEQ(*params.IsSystem))
	}

	if len(params.ComponentIDs) > 0 {
		preds = append(preds, incident.HasComponentsWith(component.IDIn(params.ComponentIDs...)))
	}

	// it's a special case for active events
	if params.IsActive != nil {
		if !*params.IsActive {
			return nil, ErrDBIncidentFilterActiveFalse //nolint:wrapcheck
		}
		currentTime := time.Now().UTC()
		preds = append(preds, incident.Or(
			incident.EndDateIsNil(),
			incident.And(
				incident.StartDateLTE(currentTime),
				incident.EndDateGTE(currentTime),
				incident.StatusNotIn(
					string(event.IncidentResolved),
					string(event.MaintenanceCompleted),
					string(event.MaintenanceCancelled),
					string(event.MaintenancePendingReview),
					string(event.MaintenanceReviewed),
					string(event.InfoCompleted),
					string(event.InfoCancelled),
				),
			),
		))
	}

	if params.Status != nil {
		preds = append(preds, incident.StatusEQ(string(*params.Status)))
	}

	switch {
	case params.StartDate != nil && params.EndDate != nil:
		preds = append(preds,
			incident.StartDateGTE(*params.StartDate),
			incident.EndDateLTE(*params.EndDate))
	case params.StartDate != nil && params.EndDate == nil:
		preds = append(preds, incident.StartDateGTE(*params.StartDate))
	case params.EndDate != nil && params.StartDate == nil:
		preds = append(preds, incident.EndDateLTE(*params.EndDate))
	}

	if !isAuth {
		preds = append(preds, publicEventPredicates()...)
	}

	return preds, nil
}

// publicEventPredicates hides maintenance awaiting review and cancelled
// maintenance that never reached a public status.
func publicEventPredicates() []predicate.Incident {
	return []predicate.Incident{
		incident.Not(incident.And(
			incident.TypeEQ(incident.TypeMaintenance),
			incident.StatusIn(
				string(event.MaintenancePendingReview),
				string(event.MaintenanceReviewed),
			),
		)),
		incident.Not(incident.And(
			incident.TypeEQ(incident.TypeMaintenance),
			incident.StatusEQ(string(event.MaintenanceCancelled)),
			noPublicStatus(),
		)),
	}
}

// noPublicStatus matches events without any public maintenance update. Ent has no
// edge to incident_status (production carries no foreign key on it), so the
// correlated subquery stays raw.
func noPublicStatus() predicate.Incident {
	return func(s *entsql.Selector) {
		s.Where(entsql.P(func(b *entsql.Builder) {
			b.WriteString(`NOT EXISTS (SELECT 1 FROM "incident_status" WHERE ` +
				`"incident_status"."incident_id" = "incident"."id" AND "incident_status"."status" IN (`)
			b.Args(
				string(event.MaintenancePlanned),
				string(event.MaintenanceInProgress),
				string(event.MaintenanceModified),
				string(event.MaintenanceCompleted),
			)
			b.WriteString("))")
		}))
	}
}

// GetEventsWithCount retrieves events based on the provided parameters, with pagination and total count.
func (db *DB) GetEventsWithCount(isAuth bool, params ...*IncidentsParams) ([]*Incident, int64, error) {
	var param IncidentsParams
	if len(params) > 0 && params[0] != nil {
		param = *params[0]
	}

	ctx := context.Background()

	preds, err := applyEventsFilters(&param, isAuth)
	if err != nil {
		return nil, 0, err
	}

	// Get total count before applying limit and offset.
	count, err := db.e.Incident.Query().Where(preds...).Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	total := int64(count)

	query := db.e.Incident.Query().
		Where(preds...).
		WithComponents(func(q *ent.ComponentQuery) {
			q.Select(component.FieldID, component.FieldName)
			q.WithAttributes()
		}).
		Order(incident.ByStartDate(entsql.OrderDesc()))

	switch {
	case param.Limit != nil && *param.Limit > 0:
		query = query.Limit(*param.Limit)
		if param.Page != nil && *param.Page > 1 {
			query = query.Offset((*param.Page - 1) * *param.Limit)
		}
	case param.LastCount > 0:
		query = query.Limit(param.LastCount)
	}

	rows, err := query.All(ctx)
	if err != nil {
		return nil, 0, err
	}

	events := make([]*Incident, 0, len(rows))
	for _, row := range rows {
		events = append(events, incidentFromEnt(row))
	}

	grouped, err := db.statusesByIncident(ctx, incidentIDs(rows))
	if err != nil {
		return nil, 0, err
	}
	attachStatuses(events, grouped)

	return events, total, nil
}

// GetEvents retrieves events based on the provided parameters.
// This is a wrapper around GetEventsWithCount for backward compatibility.
func (db *DB) GetEvents(isAuth bool, params ...*IncidentsParams) ([]*Incident, error) {
	events, _, err := db.GetEventsWithCount(isAuth, params...)
	return events, err
}

// GetEventsInternal retrieves all events for internal use (no filtering by auth).
func (db *DB) GetEventsInternal(params ...*IncidentsParams) ([]*Incident, error) {
	return db.GetEvents(AuthorizedAccess, params...)
}

func (db *DB) GetIncident(id int) (*Incident, error) {
	ctx := context.Background()

	e, err := db.e.Incident.Query().
		Where(incident.IDEQ(id)).
		WithComponents(func(q *ent.ComponentQuery) {
			q.Select(component.FieldID, component.FieldName)
			q.WithAttributes()
		}).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrDBIncidentDSNotExist
		}
		return nil, err
	}

	inc := incidentFromEnt(e)
	grouped, err := db.statusesByIncident(ctx, []int{e.ID})
	if err != nil {
		return nil, err
	}
	attachStatuses([]*Incident{inc}, grouped)

	return inc, nil
}

// WithTx runs fn inside a single transaction on the shared connection pool.
// Callers use it to write a business change and enqueue its notification atomically.
func (db *DB) WithTx(ctx context.Context, fn func(tx *Tx) error) error {
	tx, err := db.begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.rollback() }()

	if err = fn(tx); err != nil {
		return err
	}
	return tx.commit()
}

// SaveIncidentTx creates an incident using the provided transaction.
func (db *DB) SaveIncidentTx(tx *Tx, inc *Incident) (uint, error) {
	if inc.Text == nil || *inc.Text == "" {
		return 0, ErrIncidentTextRequired
	}

	ctx := context.Background()
	c := db.clientFor(tx)

	now := time.Now().UTC()
	createdAt := now
	if inc.CreatedAt != nil {
		createdAt = *inc.CreatedAt
	}
	modifiedAt := now
	if inc.ModifiedAt != nil {
		modifiedAt = *inc.ModifiedAt
	}

	inc.CreatedAt = &createdAt
	inc.ModifiedAt = &modifiedAt

	create := c.Incident.Create().
		SetText(*inc.Text).
		SetStartDate(valueOr(inc.StartDate, now)).
		SetImpact(valueOr(inc.Impact, 0)).
		SetSystem(inc.System).
		SetType(incident.Type(inc.Type)).
		SetCreatedAt(createdAt).
		SetModifiedAt(modifiedAt)

	if inc.Description != nil {
		create.SetDescription(*inc.Description)
	}
	if inc.EndDate != nil {
		create.SetEndDate(*inc.EndDate)
	}
	if inc.Status != "" {
		create.SetStatus(string(inc.Status))
	}
	if inc.CreatedBy != nil {
		create.SetCreatedBy(*inc.CreatedBy)
	}
	if inc.ContactEmail != nil {
		create.SetContactEmail(*inc.ContactEmail)
	}
	if inc.Version != nil {
		create.SetVersion(*inc.Version)
	}

	componentIDs := make([]int, 0, len(inc.Components))
	for i := range inc.Components {
		if inc.Components[i].ID != 0 {
			componentIDs = append(componentIDs, int(inc.Components[i].ID))
		}
	}
	if len(componentIDs) > 0 {
		create.AddComponentIDs(componentIDs...)
	}

	created, err := create.Save(ctx)
	if err != nil {
		return 0, err
	}

	inc.ID = uint(created.ID)
	inc.Version = intPtr(created.Version)

	for i := range inc.Statuses {
		if inc.Statuses[i].ID != 0 {
			continue
		}
		id, errStatus := insertIncidentStatus(ctx, c, &inc.Statuses[i], inc.ID)
		if errStatus != nil {
			return 0, errStatus
		}
		inc.Statuses[i].ID = uint(id)
		inc.Statuses[i].IncidentID = inc.ID
	}

	return inc.ID, nil
}

func (db *DB) SaveIncident(inc *Incident) (uint, error) {
	return db.SaveIncidentTx(nil, inc)
}

// ModifyIncidentTx applies a modification (with maintenance optimistic locking and
// new status inserts) using the provided transaction.
func (db *DB) ModifyIncidentTx(tx *Tx, inc *Incident) error {
	return db.modifyIncident(context.Background(), db.clientFor(tx), inc)
}

func (db *DB) ModifyIncident(inc *Incident) error {
	return db.execWithTx(context.Background(), nil, func(client *ent.Client, _ entsql.ExecQuerier) error {
		return db.modifyIncident(context.Background(), client, inc)
	})
}

func (db *DB) modifyIncident(ctx context.Context, c *ent.Client, inc *Incident) error {
	if inc.Version == nil {
		return errors.New("version is required for event modification")
	}

	expectedVersion := *inc.Version
	newVersion := expectedVersion + 1
	inc.Version = &newVersion

	now := time.Now().UTC()

	update := c.Incident.Update().Where(incident.IDEQ(int(inc.ID)))
	if inc.Type == event.TypeMaintenance {
		update.Where(incident.VersionEQ(expectedVersion))
	}
	applyIncidentPatch(update, inc)
	update.SetVersion(newVersion).SetModifiedAt(now)

	affected, err := update.Save(ctx)
	if err != nil {
		return err
	}
	if inc.Type == event.TypeMaintenance && affected == 0 {
		return ErrVersionConflict
	}

	for i := range inc.Statuses {
		if inc.Statuses[i].ID != 0 {
			continue
		}
		id, errStatus := insertIncidentStatus(ctx, c, &inc.Statuses[i], inc.ID)
		if errStatus != nil {
			return errStatus
		}
		inc.Statuses[i].ID = uint(id)
		inc.Statuses[i].IncidentID = inc.ID
	}

	return nil
}

// applyIncidentPatch sets the non-zero fields only, so nil pointers and empty
// scalars are left untouched, mirroring the previous ORM behaviour.
func applyIncidentPatch(update *ent.IncidentUpdate, inc *Incident) {
	if inc.Text != nil {
		update.SetText(*inc.Text)
	}
	if inc.Description != nil {
		update.SetDescription(*inc.Description)
	}
	if inc.StartDate != nil {
		update.SetStartDate(*inc.StartDate)
	}
	if inc.EndDate != nil {
		update.SetEndDate(*inc.EndDate)
	}
	if inc.Impact != nil {
		update.SetImpact(*inc.Impact)
	}
	if inc.Status != "" {
		update.SetStatus(string(inc.Status))
	}
	if inc.System {
		update.SetSystem(true)
	}
	if inc.Type != "" {
		update.SetType(incident.Type(inc.Type))
	}
	if inc.CreatedAt != nil {
		update.SetCreatedAt(*inc.CreatedAt)
	}
	if inc.CreatedBy != nil {
		update.SetCreatedBy(*inc.CreatedBy)
	}
	if inc.ContactEmail != nil {
		update.SetContactEmail(*inc.ContactEmail)
	}
}

// AddComponentToIncident adds a component and a status update to an incident using optimistic locking.
func (db *DB) AddComponentToIncident(inc *Incident, comp *Component, status IncidentStatus) error {
	if inc.Version == nil {
		return errors.New("version is required for incident modification")
	}

	expectedVersion := *inc.Version
	newVersion := expectedVersion + 1
	ctx := context.Background()

	err := db.execWithTx(ctx, nil, func(client *ent.Client, _ entsql.ExecQuerier) error {
		affected, err := client.Incident.Update().
			Where(incident.IDEQ(int(inc.ID)), incident.VersionEQ(expectedVersion)).
			SetVersion(newVersion).
			Save(ctx)
		if err != nil {
			return err
		}
		if affected == 0 {
			return ErrVersionConflict
		}

		if comp.ID != 0 {
			if _, err = client.Incident.UpdateOneID(int(inc.ID)).AddComponentIDs(int(comp.ID)).Save(ctx); err != nil {
				return err
			}
			inc.Components = append(inc.Components, *comp)
		}

		if _, err = insertIncidentStatus(ctx, client, &status, inc.ID); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}

	inc.Version = &newVersion
	return nil
}

// ReOpenIncident the special function if you need to NULL your end_date.
func (db *DB) ReOpenIncident(inc *Incident) error {
	err := db.e.Incident.UpdateOneID(int(inc.ID)).
		ClearEndDate().
		Exec(context.Background())
	if ent.IsNotFound(err) {
		return nil
	}
	return err
}

// GetEventsByComponentID retrieves all public events associated with a specific component ID.
// Maintenance events in pending_review/reviewed status are excluded (require authentication).
// Not affected to getActiveEventsForComponent (v2.go) because IsActive filter already contains
// exceptions for "event.TypeMaintenance, event.MaintenancePendingReview, event.MaintenanceReviewed".
// Supports optional filtering parameters: isActive, Types, LastCount.
func (db *DB) GetEventsByComponentID(componentID uint, params ...*IncidentsParams) ([]*Incident, error) {
	var param IncidentsParams
	if params != nil && params[0] != nil {
		param = *params[0]
	}

	ctx := context.Background()

	preds := []predicate.Incident{
		incident.HasComponentsWith(component.IDEQ(int(componentID))),
	}
	preds = append(preds, publicEventPredicates()...)

	if param.IsActive != nil && *param.IsActive {
		currentTime := time.Now().UTC()
		preds = append(preds, incident.Or(
			incident.EndDateIsNil(),
			incident.And(
				incident.StartDateLTE(currentTime),
				incident.EndDateGTE(currentTime),
				incident.StatusNotIn(
					string(event.IncidentResolved),
					string(event.MaintenanceCompleted),
					string(event.MaintenanceCancelled),
					string(event.InfoCompleted),
					string(event.InfoCancelled),
				),
			),
		))
	}

	if len(param.Types) > 0 {
		types := make([]incident.Type, 0, len(param.Types))
		for _, t := range param.Types {
			types = append(types, incident.Type(t))
		}
		preds = append(preds, incident.TypeIn(types...))
	}

	query := db.e.Incident.Query().
		Where(preds...).
		WithComponents(func(q *ent.ComponentQuery) {
			q.Select(component.FieldID, component.FieldName)
			q.WithAttributes()
		})

	if param.LastCount != 0 {
		query = query.Order(incident.ByID(entsql.OrderDesc())).Limit(param.LastCount)
	} else {
		query = query.Order(incident.ByID(entsql.OrderAsc()))
	}

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

func (db *DB) GetIncidentsByComponentAttr(attr *ComponentAttr, params ...*IncidentsParams) ([]*Incident, error) {
	// Get all public incidents for components with this attribute.
	// Maintenance events in pending_review/reviewed status are excluded (require authentication).
	var param IncidentsParams
	if params != nil && params[0] != nil {
		param = *params[0]
	}

	ctx := context.Background()

	// The previous ORM joined the relation and the attribute tables directly, so an
	// incident matched by several components appeared once per match. The raw id
	// query keeps that shape and its ordering.
	query := incidentsByComponentAttrQuery
	args := []any{
		attr.Name, attr.Value,
		string(event.TypeMaintenance), string(event.MaintenancePendingReview), string(event.MaintenanceReviewed),
		string(event.TypeMaintenance), string(event.MaintenanceCancelled),
		string(event.MaintenancePlanned), string(event.MaintenanceInProgress),
		string(event.MaintenanceModified), string(event.MaintenanceCompleted),
	}
	if param.LastCount != 0 {
		query += " ORDER BY incident.id DESC LIMIT $12"
		args = append(args, param.LastCount)
	}

	ids, err := scanIntColumn(ctx, db.rawFor(nil), query, args...)
	if err != nil {
		return nil, err
	}

	return db.incidentsByIDs(ctx, ids)
}

func (db *DB) GetComponent(id int) (*Component, error) {
	e, err := db.e.Component.Query().
		Where(component.IDEQ(id)).
		WithAttributes().
		First(context.Background())
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrDBComponentDSNotExist
		}
		return nil, err
	}

	comp := componentFromEnt(e)
	return &comp, nil
}

func (db *DB) GetComponentsAsMap() (map[int]*Component, error) {
	rows, err := db.e.Component.Query().All(context.Background())
	if err != nil {
		return nil, err
	}

	compMap := make(map[int]*Component, len(rows))
	for _, row := range rows {
		comp := componentFromEnt(row)
		compMap[int(comp.ID)] = &comp
	}

	return compMap, nil
}

func (db *DB) GetComponentsWithValues() ([]Component, error) {
	rows, err := db.e.Component.Query().
		WithAttributes().
		All(context.Background())
	if err != nil {
		return nil, err
	}

	components := make([]Component, 0, len(rows))
	for _, row := range rows {
		components = append(components, componentFromEnt(row))
	}

	return components, nil
}

func (db *DB) GetComponentsWithIncidents() ([]Component, error) {
	ctx := context.Background()

	rows, err := db.e.Component.Query().
		WithAttributes().
		WithIncidents().
		All(ctx)
	if err != nil {
		return nil, err
	}

	var entIncidents []*ent.Incident
	for _, row := range rows {
		entIncidents = append(entIncidents, row.Edges.Incidents...)
	}

	grouped, err := db.statusesByIncident(ctx, incidentIDs(entIncidents))
	if err != nil {
		return nil, err
	}

	components := make([]Component, 0, len(rows))
	for _, row := range rows {
		comp := componentFromEnt(row)
		attachStatuses(comp.Incidents, grouped)
		components = append(components, comp)
	}

	return components, nil
}

// GetComponentFromNameAttrs returns the Component from its name and region attribute.
func (db *DB) GetComponentFromNameAttrs(name string, attr *ComponentAttr) (*Component, error) {
	e, err := db.e.Component.Query().
		Where(
			component.NameEQ(name),
			component.HasAttributesWith(componentattr.ValueEQ(attr.Value)),
		).
		WithAttributes().
		Order(component.ByID(entsql.OrderAsc())).
		First(context.Background())
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrDBComponentDSNotExist
		}
		return nil, err
	}

	comp := componentFromEnt(e)
	return &comp, nil
}

func (db *DB) SaveComponent(comp *Component) (uint, error) {
	ctx := context.Background()

	// Validate required region attribute
	hasRegion := false
	for _, attr := range comp.Attrs {
		if attr.Name == regionAttrName {
			hasRegion = true

			// Check if component with same name and region exists
			exists, err := db.e.Component.Query().
				Where(
					component.NameEQ(comp.Name),
					component.HasAttributesWith(
						componentattr.NameEQ(regionAttrName),
						componentattr.ValueEQ(attr.Value),
					),
				).
				Exist(ctx)
			if err != nil {
				return 0, err
			}
			if exists {
				return 0, ErrDBComponentExists
			}
			break
		}
	}

	if !hasRegion {
		return 0, fmt.Errorf("missing required region attribute")
	}

	now := time.Now().UTC()
	if comp.CreatedAt == nil {
		comp.CreatedAt = &now
	}
	if comp.ModifiedAt == nil {
		comp.ModifiedAt = &now
	}

	tx, err := db.e.Tx(ctx)
	if err != nil {
		return 0, err
	}

	created, err := tx.Component.Create().
		SetName(comp.Name).
		SetCreatedAt(*comp.CreatedAt).
		SetModifiedAt(*comp.ModifiedAt).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return 0, err
	}

	for i := range comp.Attrs {
		attr, attrErr := tx.ComponentAttr.Create().
			SetName(comp.Attrs[i].Name).
			SetValue(comp.Attrs[i].Value).
			SetComponentID(created.ID).
			Save(ctx)
		if attrErr != nil {
			_ = tx.Rollback()
			return 0, attrErr
		}
		comp.Attrs[i].ID = uint(attr.ID)
		comp.Attrs[i].ComponentID = uint(created.ID)
	}

	if err = tx.Commit(); err != nil {
		return 0, err
	}

	comp.ID = uint(created.ID)
	return comp.ID, nil
}

func (db *DB) MoveComponentFromOldToAnotherIncident(
	comp *Component, incOld, incNew *Incident, closeOld bool,
) (*Incident, error) {
	timeNow := time.Now().UTC()

	if comp.Name == "" {
		c, err := db.GetComponent(int(comp.ID))
		if err != nil {
			return nil, err
		}
		comp = c
	}

	incNew.Components = append(incNew.Components, *comp)
	text := fmt.Sprintf("%s moved from %s", comp.PrintAttrs(), incOld.Link())
	incNew.Statuses = append(incNew.Statuses, IncidentStatus{
		IncidentID: incNew.ID,
		Status:     event.OutDatedSystem,
		Text:       text,
		Timestamp:  timeNow,
	})

	text = fmt.Sprintf("%s moved to %s", comp.PrintAttrs(), incNew.Link())
	status := event.OutDatedSystem

	if closeOld {
		text = fmt.Sprintf("%s, Incident closed by system", text)
		status = event.IncidentResolved
		incOld.Status = event.IncidentResolved
	}

	incOld.Statuses = append(incOld.Statuses, IncidentStatus{
		IncidentID: incOld.ID,
		Status:     status,
		Text:       text,
		Timestamp:  timeNow,
	})
	if closeOld {
		incOld.EndDate = &timeNow
	}

	err := db.execWithTx(context.Background(), nil, func(client *ent.Client, _ entsql.ExecQuerier) error {
		if !closeOld && comp.ID != 0 {
			if errRemove := removeIncidentComponent(context.Background(), client, incOld.ID, comp.ID); errRemove != nil {
				return errRemove
			}
			dropIncidentComponent(incOld, comp.ID)
		}

		if errSave := saveIncidentFull(context.Background(), client, incNew); errSave != nil {
			return errSave
		}
		return saveIncidentFull(context.Background(), client, incOld)
	})
	if err != nil {
		return nil, err
	}

	return incNew, nil
}

func (db *DB) ExtractComponentsToNewIncident(
	comp []Component, incOld *Incident, impact int, text string, description *string,
) (*Incident, error) {
	if len(comp) == 0 {
		return nil, fmt.Errorf("no components to extract")
	}

	timeNow := time.Now().UTC()

	inc := &Incident{
		Text:        &text,
		Description: description,
		StartDate:   &timeNow,
		EndDate:     nil,
		Impact:      &impact,
		Statuses:    []IncidentStatus{},
		Status:      event.OutDatedSystem,
		System:      false,
		Type:        event.TypeIncident,
		Components:  comp,
	}

	id, err := db.SaveIncident(inc)
	if err != nil {
		return nil, err
	}

	for _, c := range comp {
		incText := fmt.Sprintf("%s moved from %s", c.PrintAttrs(), incOld.Link())
		inc.Statuses = append(inc.Statuses, IncidentStatus{
			IncidentID: id,
			Status:     event.OutDatedSystem,
			Text:       incText,
			Timestamp:  timeNow,
		})
	}

	for _, c := range comp {
		incText := fmt.Sprintf("%s moved to %s", c.PrintAttrs(), inc.Link())
		incOld.Statuses = append(incOld.Statuses, IncidentStatus{
			IncidentID: incOld.ID,
			Status:     event.OutDatedSystem,
			Text:       incText,
			Timestamp:  timeNow,
		})
	}

	// Use a transaction to save both incidents with their statuses and update associations
	err = db.execWithTx(context.Background(), nil, func(client *ent.Client, _ entsql.ExecQuerier) error {
		// Remove component from old incident
		for i := range comp {
			if comp[i].ID == 0 {
				continue
			}
			if errRemove := removeIncidentComponent(context.Background(), client, incOld.ID, comp[i].ID); errRemove != nil {
				return errRemove
			}
			dropIncidentComponent(incOld, comp[i].ID)
		}

		if errSave := saveIncidentFull(context.Background(), client, inc); errSave != nil {
			return errSave
		}
		return saveIncidentFull(context.Background(), client, incOld)
	})
	if err != nil {
		return nil, err
	}

	return inc, nil
}

func (db *DB) IncreaseIncidentImpact(inc *Incident, impact int) (*Incident, error) {
	timeNow := time.Now().UTC()
	text := fmt.Sprintf("impact changed from %d to %d", *inc.Impact, impact)
	inc.Statuses = append(inc.Statuses, IncidentStatus{
		IncidentID: inc.ID,
		Status:     event.OutDatedSystem,
		Text:       text,
		Timestamp:  timeNow,
		CreatedAt:  &timeNow,
		ModifiedAt: &timeNow,
	})
	inc.Impact = &impact
	inc.ModifiedAt = &timeNow

	// Only non-zero fields are written for the incident row, mirroring the
	// previous struct-based update. incident_status has no Ent edge, so the
	// appended status row is inserted directly in the same transaction.
	ctx := context.Background()
	tx, err := db.e.Tx(ctx)
	if err != nil {
		return nil, err
	}

	update := tx.Incident.UpdateOneID(int(inc.ID))
	if inc.Text != nil && *inc.Text != "" {
		update.SetText(*inc.Text)
	}
	if inc.Description != nil {
		update.SetDescription(*inc.Description)
	}
	if inc.StartDate != nil {
		update.SetStartDate(*inc.StartDate)
	}
	if inc.EndDate != nil {
		update.SetEndDate(*inc.EndDate)
	}
	update.SetImpact(*inc.Impact)
	if inc.Status != "" {
		update.SetStatus(string(inc.Status))
	}
	if inc.System {
		update.SetSystem(true)
	}
	if inc.Type != "" {
		update.SetType(incident.Type(inc.Type))
	}
	if inc.CreatedAt != nil {
		update.SetCreatedAt(*inc.CreatedAt)
	}
	update.SetModifiedAt(timeNow)
	if inc.CreatedBy != nil {
		update.SetCreatedBy(*inc.CreatedBy)
	}
	if inc.ContactEmail != nil {
		update.SetContactEmail(*inc.ContactEmail)
	}
	if inc.Version != nil {
		update.SetVersion(*inc.Version)
	}

	if err = update.Exec(ctx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}

	_, err = tx.IncidentStatus.Create().
		SetIncidentID(int(inc.ID)).
		SetStatus(string(event.OutDatedSystem)).
		SetText(text).
		SetTimestamp(timeNow).
		SetCreatedAt(timeNow).
		SetModifiedAt(timeNow).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}

	if err = tx.Commit(); err != nil {
		return nil, err
	}

	return inc, nil
}

func (db *DB) GetUniqueAttributeValues(attrName string) ([]string, error) {
	rows, err := db.e.ComponentAttr.Query().
		Where(componentattr.NameEQ(attrName)).
		Select(componentattr.FieldValue).
		Order(componentattr.ByValue(entsql.OrderAsc())).
		All(context.Background())
	if err != nil {
		return nil, err
	}

	values := make([]string, 0, len(rows))
	for i, row := range rows {
		if i > 0 && rows[i-1].Value == row.Value {
			continue
		}
		values = append(values, row.Value)
	}

	return values, nil
}

func (db *DB) GetEventUpdates(incidentID uint) ([]IncidentStatus, error) {
	rows, err := db.e.IncidentStatus.Query().
		Where(incidentstatus.IncidentID(int(incidentID))).
		Order(incidentstatus.ByID(entsql.OrderAsc())).
		All(context.Background())
	if err != nil {
		return nil, err
	}

	updates := make([]IncidentStatus, 0, len(rows))
	for _, row := range rows {
		updates = append(updates, incidentStatusFromEnt(row))
	}

	return updates, nil
}

// ModifyEventUpdateTx patches an event status update's text using the provided
// transaction and returns the updated row.
func (db *DB) ModifyEventUpdateTx(tx *Tx, update IncidentStatus) (IncidentStatus, error) {
	ctx := context.Background()
	c := db.clientFor(tx)
	now := time.Now().UTC()

	affected, err := c.IncidentStatus.Update().
		Where(
			incidentstatus.IDEQ(int(update.ID)),
			incidentstatus.IncidentIDEQ(int(update.IncidentID)),
		).
		SetText(update.Text).
		SetModifiedAt(now).
		Save(ctx)
	if err != nil {
		return IncidentStatus{}, err
	}
	if affected == 0 {
		return IncidentStatus{}, ErrDBEventUpdateDSNotExist
	}

	row, err := c.IncidentStatus.Query().
		Where(incidentstatus.IDEQ(int(update.ID))).
		Only(ctx)
	if err != nil {
		return IncidentStatus{}, err
	}

	return incidentStatusFromEnt(row), nil
}

func (db *DB) ModifyEventUpdate(update IncidentStatus) (IncidentStatus, error) {
	return db.ModifyEventUpdateTx(nil, update)
}
