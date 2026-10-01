package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"moul.io/zapgorm2"

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

type DB struct {
	g *gorm.DB
}

func New(c *conf.Config) (*DB, error) {
	psql := postgres.New(postgres.Config{
		DSN: c.DB,
	})

	gConf := &gorm.Config{
		NowFunc: func() time.Time {
			return time.Now().UTC()
		},
	}

	if c.LogLevel != conf.DevelopMode {
		logger := zapgorm2.New(zap.L())
		gConf.Logger = logger
	}

	g, err := gorm.Open(psql, gConf)
	if err != nil {
		return nil, err
	}

	sqlDB, err := g.DB()
	if err != nil {
		return nil, fmt.Errorf("getting underlying sql.DB: %w", err)
	}

	sqlDB.SetMaxOpenConns(dbMaxOpenConns)
	sqlDB.SetMaxIdleConns(dbMaxIdleConns)
	sqlDB.SetConnMaxLifetime(dbConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(dbConnMaxIdleTime)

	return &DB{g: g}, nil
}

func (db *DB) Close() error {
	sqlDB, err := db.g.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
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

func applyEventsFilters(base *gorm.DB, params *IncidentsParams, isAuth bool) (*gorm.DB, error) {
	if params.Types != nil {
		base = base.Where("incident.type IN (?)", params.Types)
	}

	if params.Impact != nil {
		base = base.Where("incident.impact = ?", *params.Impact)
	}

	if params.IsSystem != nil {
		base = base.Where("incident.system = ?", *params.IsSystem)
	}

	if len(params.ComponentIDs) > 0 {
		base = base.Joins("JOIN incident_component_relation icr ON icr.incident_id = incident.id").
			Where("icr.component_id IN (?)", params.ComponentIDs).Group("incident.id")
	}

	// it's a special case for active events
	if params.IsActive != nil {
		if !*params.IsActive {
			return nil, ErrDBIncidentFilterActiveFalse //nolint:wrapcheck
		}
		currentTime := time.Now().UTC()
		base = base.Where("(incident.end_date IS NULL) OR "+
			"(incident.start_date <= ? AND "+
			"incident.end_date >= ? AND "+
			"incident.status NOT IN (?))",
			currentTime,
			currentTime,
			[]event.Status{event.IncidentResolved,
				event.MaintenanceCompleted,
				event.MaintenanceCancelled,
				event.MaintenancePendingReview,
				event.MaintenanceReviewed,
				event.InfoCompleted,
				event.InfoCancelled})
	}

	if params.Status != nil {
		base = base.Where("incident.status = ?", params.Status)
	}

	switch {
	case params.StartDate != nil && params.EndDate != nil:
		base = base.Where("incident.start_date >= ? AND incident.end_date <= ?", *params.StartDate, *params.EndDate)
	case params.StartDate != nil && params.EndDate == nil:
		base = base.Where("incident.start_date >= ?", *params.StartDate)
	case params.EndDate != nil && params.StartDate == nil:
		base = base.Where("incident.end_date <= ?", *params.EndDate)
	}

	if !isAuth {
		base = base.Where(
			"NOT (incident.type = ? AND incident.status IN (?, ?))",
			event.TypeMaintenance, event.MaintenancePendingReview, event.MaintenanceReviewed,
		)
		// Hide cancelled maintenance events that never reached a public status (planned or later).
		base = base.Where(
			"NOT (incident.type = ? AND incident.status = ? AND "+
				"NOT EXISTS (SELECT 1 FROM incident_status WHERE incident_status.incident_id = incident.id "+
				"AND incident_status.status IN (?, ?, ?, ?)))",
			event.TypeMaintenance, event.MaintenanceCancelled,
			event.MaintenancePlanned, event.MaintenanceInProgress, event.MaintenanceModified, event.MaintenanceCompleted,
		)
	}

	return base, nil
}

func (db *DB) fetchPaginatedEvents(filteredBase *gorm.DB, param *IncidentsParams) ([]*Incident, error) {
	var events []*Incident

	subQuery := filteredBase.
		Select("incident.id").
		Order("incident.start_date DESC").
		Limit(*param.Limit)

	if param.Page != nil && *param.Page > 1 {
		subQuery = subQuery.Offset((*param.Page - 1) * *param.Limit)
	}

	r := db.g.Model(&Incident{}).
		Joins("JOIN (?) AS filtered_ids ON filtered_ids.id = incident.id", subQuery).
		Preload("Statuses").
		Preload("Components", func(db *gorm.DB) *gorm.DB { return db.Select("ID, Name") }).
		Preload("Components.Attrs").
		Order("incident.start_date DESC")

	if err := r.Find(&events).Error; err != nil {
		return nil, err
	}
	return events, nil
}

func (db *DB) fetchUnpaginatedEvents(filteredBase *gorm.DB, param *IncidentsParams) ([]*Incident, error) {
	var events []*Incident

	r := filteredBase.Order("incident.start_date DESC")
	if param.LastCount > 0 {
		r = r.Limit(param.LastCount)
	}
	if err := r.Preload("Statuses").
		Preload("Components", func(db *gorm.DB) *gorm.DB { return db.Select("ID, Name") }).
		Preload("Components.Attrs").
		Find(&events).Error; err != nil {
		return nil, err
	}
	return events, nil
}

// GetEventsWithCount retrieves events based on the provided parameters, with pagination and total count.
func (db *DB) GetEventsWithCount(isAuth bool, params ...*IncidentsParams) ([]*Incident, int64, error) {
	var param IncidentsParams
	var total int64
	var events []*Incident
	if len(params) > 0 && params[0] != nil {
		param = *params[0]
	}

	// Base query for filtering
	base := db.g.Model(&Incident{})

	filteredBase, err := applyEventsFilters(base, &param, isAuth)
	if err != nil {
		return nil, 0, err
	}

	// Get total count before applying limit and offset.
	if err = filteredBase.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if param.Limit != nil && *param.Limit > 0 {
		events, err = db.fetchPaginatedEvents(filteredBase, &param)
	} else {
		events, err = db.fetchUnpaginatedEvents(filteredBase, &param)
	}

	if err != nil {
		return nil, 0, err
	}
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
	inc := Incident{ID: uint(id)}

	r := db.g.Model(&Incident{}).
		Where(inc).
		Preload("Statuses", func(db *gorm.DB) *gorm.DB {
			return db.Order("id ASC")
		}).
		Preload("Components", func(db *gorm.DB) *gorm.DB {
			return db.Select("ID, Name")
		}).
		Preload("Components.Attrs").
		First(&inc)

	if r.Error != nil {
		if errors.Is(r.Error, gorm.ErrRecordNotFound) {
			return nil, ErrDBIncidentDSNotExist
		}
		return nil, r.Error
	}

	return &inc, nil
}

// WithTx runs fn inside a single transaction on the shared connection pool.
// Callers use it to write a business change and enqueue its notification atomically.
func (db *DB) WithTx(ctx context.Context, fn func(tx *gorm.DB) error) error {
	return db.g.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(tx)
	})
}

// SaveIncidentTx creates an incident using the provided transaction.
func (db *DB) SaveIncidentTx(tx *gorm.DB, inc *Incident) (uint, error) {
	if err := tx.Create(inc).Error; err != nil {
		return 0, err
	}
	return inc.ID, nil
}

func (db *DB) SaveIncident(inc *Incident) (uint, error) {
	return db.SaveIncidentTx(db.g, inc)
}

// ModifyIncidentTx applies a modification (with maintenance optimistic locking and
// new status inserts) using the provided transaction.
func (db *DB) ModifyIncidentTx(tx *gorm.DB, inc *Incident) error {
	if inc.Version == nil {
		return errors.New("version is required for event modification")
	}

	expectedVersion := *inc.Version
	newVersion := expectedVersion + 1
	inc.Version = &newVersion

	query := tx.Model(&Incident{}).Where("id = ?", inc.ID)

	if inc.Type == event.TypeMaintenance {
		query = query.Where("version = ?", expectedVersion)
	}

	r := query.Omit("Statuses", "Components").Updates(inc)

	if r.Error != nil {
		return r.Error
	}

	if inc.Type == event.TypeMaintenance && r.RowsAffected == 0 {
		return ErrVersionConflict
	}

	for i := range inc.Statuses {
		if inc.Statuses[i].ID != 0 {
			continue
		}
		if inc.Statuses[i].IncidentID == 0 {
			inc.Statuses[i].IncidentID = inc.ID
		}
		if err := tx.Create(&inc.Statuses[i]).Error; err != nil {
			return err
		}
	}

	return nil
}

func (db *DB) ModifyIncident(inc *Incident) error {
	return db.g.Transaction(func(tx *gorm.DB) error {
		return db.ModifyIncidentTx(tx, inc)
	})
}

// AddComponentToIncident adds a component and a status update to an incident using optimistic locking.
func (db *DB) AddComponentToIncident(inc *Incident, comp *Component, status IncidentStatus) error {
	if inc.Version == nil {
		return errors.New("version is required for incident modification")
	}

	expectedVersion := *inc.Version
	newVersion := expectedVersion + 1

	err := db.g.Transaction(func(tx *gorm.DB) error {
		// Update version with optimistic lock
		r := tx.Model(&Incident{}).
			Where("id = ? AND version = ?", inc.ID, expectedVersion).
			Updates(map[string]interface{}{
				"version": newVersion,
			})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected == 0 {
			return ErrVersionConflict
		}

		// Add component to incident via association
		if err := tx.Model(inc).Association("Components").Append(comp); err != nil {
			return err
		}

		// Create status update
		if status.IncidentID == 0 {
			status.IncidentID = inc.ID
		}
		if err := tx.Create(&status).Error; err != nil {
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
	r := db.g.Model(&Incident{}).Where("id = ?", inc.ID).Updates(map[string]interface{}{
		"end_date": nil,
	})
	if r.Error != nil {
		return r.Error
	}

	return nil
}

// GetEventsByComponentID retrieves all public events associated with a specific component ID.
// Maintenance events in pending_review/reviewed status are excluded (require authentication).
// Not affected to getActiveEventsForComponent (v2.go) because IsActive filter already contains
// exceptions for "event.TypeMaintenance, event.MaintenancePendingReview, event.MaintenanceReviewed".
// Supports optional filtering parameters: isActive, Types, LastCount.
func (db *DB) GetEventsByComponentID(componentID uint, params ...*IncidentsParams) ([]*Incident, error) {
	// Get all incidents for this component
	var incidents []*Incident
	var param IncidentsParams
	if params != nil && params[0] != nil {
		param = *params[0]
	}

	r := db.g.Model(&Incident{}).
		Joins("JOIN incident_component_relation icr ON icr.incident_id = incident.id").
		Where("icr.component_id = ?", componentID).
		Where("NOT (incident.type = ? AND incident.status IN (?, ?))",
			event.TypeMaintenance, event.MaintenancePendingReview, event.MaintenanceReviewed).
		Where("NOT (incident.type = ? AND incident.status = ? AND "+
			"NOT EXISTS (SELECT 1 FROM incident_status WHERE incident_status.incident_id = incident.id "+
			"AND incident_status.status IN (?, ?, ?, ?)))",
			event.TypeMaintenance, event.MaintenanceCancelled,
			event.MaintenancePlanned, event.MaintenanceInProgress, event.MaintenanceModified, event.MaintenanceCompleted).
		Preload("Statuses").
		Preload("Components", func(db *gorm.DB) *gorm.DB {
			return db.Select("ID, Name")
		}).
		Preload("Components.Attrs")

	if param.LastCount != 0 {
		r.Order("incident.id desc").Limit(param.LastCount)
	}

	if param.IsActive != nil && *param.IsActive {
		currentTime := time.Now().UTC()
		r.Where("(incident.end_date IS NULL) OR "+
			"(incident.start_date <= ? AND "+
			"incident.end_date >= ? AND "+
			"incident.status NOT IN (?))",
			currentTime,
			currentTime,
			[]event.Status{event.IncidentResolved,
				event.MaintenanceCompleted,
				event.MaintenanceCancelled,
				event.InfoCompleted,
				event.InfoCancelled})
	}

	if len(param.Types) > 0 {
		r.Where("incident.type IN (?)", param.Types)
	}

	r.Find(&incidents)
	if r.Error != nil {
		return nil, r.Error
	}
	return incidents, nil
}

func (db *DB) GetIncidentsByComponentAttr(attr *ComponentAttr, params ...*IncidentsParams) ([]*Incident, error) {
	// Get all public incidents for components with this attribute.
	// Maintenance events in pending_review/reviewed status are excluded (require authentication).
	var incidents []*Incident
	var param IncidentsParams
	if params != nil && params[0] != nil {
		param = *params[0]
	}

	r := db.g.Model(&Incident{}).
		Joins("JOIN incident_component_relation icr ON icr.incident_id = incident.id").
		Joins("JOIN component_attribute ca ON ca.component_id = icr.component_id").
		Where("ca.name = ? AND ca.value = ?", attr.Name, attr.Value).
		Where("NOT (incident.type = ? AND incident.status IN (?, ?))",
			event.TypeMaintenance, event.MaintenancePendingReview, event.MaintenanceReviewed).
		Where("NOT (incident.type = ? AND incident.status = ? AND "+
			"NOT EXISTS (SELECT 1 FROM incident_status WHERE incident_status.incident_id = incident.id "+
			"AND incident_status.status IN (?, ?, ?, ?)))",
			event.TypeMaintenance, event.MaintenanceCancelled,
			event.MaintenancePlanned, event.MaintenanceInProgress, event.MaintenanceModified, event.MaintenanceCompleted).
		Preload("Statuses").
		Preload("Components", func(db *gorm.DB) *gorm.DB {
			return db.Select("ID, Name")
		}).
		Preload("Components.Attrs")

	if param.LastCount != 0 {
		r.Order("incident.id desc").Limit(param.LastCount)
	}

	r.Find(&incidents)
	if r.Error != nil {
		return nil, r.Error
	}

	return incidents, nil
}

func (db *DB) GetOpenedIncidentsWithComponent(name string, attrs []ComponentAttr) (*Incident, error) {
	comp := &Component{Name: name, Attrs: attrs}
	r := db.g.Model(&Component{}).Preload("Attrs").Find(comp)
	if r.Error != nil {
		if errors.Is(r.Error, gorm.ErrRecordNotFound) {
			return nil, ErrDBComponentDSNotExist
		}
		return nil, r.Error
	}

	var incident Incident
	r = db.g.Model(&Incident{}).
		Preload("Statuses").
		Preload("Components", func(db *gorm.DB) *gorm.DB {
			return db.Select("ID")
		}).
		// Where("component_id = ?", comp.ID).
		First(&incident)

	if r.Error != nil {
		return nil, r.Error
	}

	return &incident, nil
}

func (db *DB) GetComponent(id int) (*Component, error) {
	comp := &Component{ID: uint(id)}
	r := db.g.Model(&Component{}).Preload("Attrs").First(comp)

	if r.Error != nil {
		if errors.Is(r.Error, gorm.ErrRecordNotFound) {
			return nil, ErrDBComponentDSNotExist
		}
		return nil, r.Error
	}

	return comp, nil
}

func (db *DB) GetComponentsAsMap() (map[int]*Component, error) {
	var components []Component
	r := db.g.Model(&Component{}).Find(&components)

	if r.Error != nil {
		return nil, r.Error
	}

	var compMap = make(map[int]*Component)
	for _, comp := range components {
		compMap[int(comp.ID)] = &comp
	}

	return compMap, nil
}

func (db *DB) GetComponentsWithValues() ([]Component, error) {
	var components []Component
	r := db.g.Model(&Component{}).Preload("Attrs").Find(&components)

	if r.Error != nil {
		return nil, r.Error
	}

	return components, nil
}

func (db *DB) GetComponentsWithIncidents() ([]Component, error) {
	var components []Component
	r := db.g.Model(&Component{}).Preload("Attrs").Preload("Incidents").Preload("Incidents.Statuses").Find(&components)

	if r.Error != nil {
		return nil, r.Error
	}

	return components, nil
}

// GetComponentFromNameAttrs returns the Component from its name and region attribute.
func (db *DB) GetComponentFromNameAttrs(name string, attr *ComponentAttr) (*Component, error) {
	comp := Component{}
	//nolint:lll
	// You can reproduce this raw request
	// select * from component join component_attribute ca on component.id=ca.component_id
	// where component.id =
	// (select component.id from component join component_attribute ca on component.id = ca.component_id and ca.value='EU-DE' and component.name='Cloud Container Engine');
	subQuery := db.g.Model(&Component{}).
		Select("component.id").
		Joins("JOIN component_attribute ca ON ca.component_id = component.id").
		Where("ca.value = ?", attr.Value).
		Where("component.name = ?", name)
	r := db.g.Model(&Component{}).Where("name = ?", name).
		Where("id = (?)", subQuery).
		Preload("Attrs").
		First(&comp)

	if r.Error != nil {
		if errors.Is(r.Error, gorm.ErrRecordNotFound) {
			return nil, ErrDBComponentDSNotExist
		}
		return nil, r.Error
	}

	return &comp, nil
}

func (db *DB) SaveComponent(comp *Component) (uint, error) {
	// Validate required region attribute
	hasRegion := false
	for _, attr := range comp.Attrs {
		if attr.Name == "region" {
			hasRegion = true

			// Check if component with same name and region exists
			var exists Component
			if err := db.g.Joins("JOIN component_attribute ca ON ca.component_id = component.id").
				Where("component.name = ? AND ca.name = 'region' AND ca.value = ?",
					comp.Name, attr.Value).First(&exists).Error; err == nil {
				return 0, ErrDBComponentExists
			}
			break
		}
	}

	if !hasRegion {
		return 0, fmt.Errorf("missing required region attribute")
	}

	// Create the component
	if err := db.g.Create(comp).Error; err != nil {
		return 0, err
	}

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

	err := db.g.Transaction(func(tx *gorm.DB) error {
		if !closeOld {
			if err := tx.Model(incOld).Association("Components").Delete(comp); err != nil {
				return err
			}
		}

		if r := tx.Save(incNew); r.Error != nil {
			return r.Error
		}
		if r := tx.Save(incOld); r.Error != nil {
			return r.Error
		}

		return nil
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
	err = db.g.Transaction(func(tx *gorm.DB) error {
		// Remove component from old incident
		for _, c := range comp {
			if errDel := tx.Model(incOld).Association("Components").Delete(c); err != nil {
				return errDel
			}
		}

		// Save both incidents with their new statuses (Save() saves associated records)
		if r := tx.Save(inc); r.Error != nil {
			return r.Error
		}
		if r := tx.Save(incOld); r.Error != nil {
			return r.Error
		}

		return nil
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
	})
	inc.Impact = &impact

	if r := db.g.Updates(inc); r.Error != nil {
		return nil, r.Error
	}

	return inc, nil
}

func (db *DB) GetUniqueAttributeValues(attrName string) ([]string, error) {
	var values []string
	r := db.g.Model(&ComponentAttr{}).
		Select("DISTINCT value").
		Where("name = ?", attrName).
		Order("value ASC").
		Pluck("value", &values)

	if r.Error != nil {
		return nil, r.Error
	}

	return values, nil
}

func (db *DB) GetEventUpdates(incidentID uint) ([]IncidentStatus, error) {
	var updates []IncidentStatus
	r := db.g.Model(&IncidentStatus{}).
		Where("incident_id = ?", incidentID).
		Order("id ASC").
		Find(&updates)

	if r.Error != nil {
		return nil, r.Error
	}

	return updates, nil
}

// ModifyEventUpdateTx patches an event status update's text using the provided
// transaction and returns the updated row.
func (db *DB) ModifyEventUpdateTx(tx *gorm.DB, update IncidentStatus) (IncidentStatus, error) {
	now := time.Now().UTC()
	var updated IncidentStatus
	r := tx.Model(&IncidentStatus{}).
		Clauses(clause.Returning{}).
		Where("id = ? AND incident_id = ?", update.ID, update.IncidentID).
		Updates(map[string]interface{}{
			"text":        update.Text,
			"modified_at": now,
		}).
		Scan(&updated)

	if r.Error != nil {
		return IncidentStatus{}, r.Error
	}
	if r.RowsAffected == 0 {
		return IncidentStatus{}, ErrDBEventUpdateDSNotExist
	}

	return updated, nil
}

func (db *DB) ModifyEventUpdate(update IncidentStatus) (IncidentStatus, error) {
	return db.ModifyEventUpdateTx(db.g, update)
}
