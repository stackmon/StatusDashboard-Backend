package db

import "errors"

var ErrDBComponentDSNotExist = errors.New("component does not exist")
var ErrDBComponentExists = errors.New("component exists")
var ErrDBIncidentDSNotExist = errors.New("incident does not exist")
var ErrDBEventUpdateDSNotExist = errors.New("update does not exist")
var ErrDBIncidentFilterActiveFalse = errors.New("filter for inactive incidents is restricted")
var ErrVersionConflict = errors.New("version conflict")
var ErrNotificationSchemaMissing = errors.New(
	"notification_outbox table is missing: apply the pending database migrations")
