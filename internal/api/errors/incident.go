package errors

import (
	"errors"
	"fmt"
	"strings"
)

var ErrIncidentDSNotExist = errors.New("event does not exist")
var ErrIncidentEndDateShouldBeEmpty = errors.New("event end_date should be empty")
var ErrIncidentStartDateInFuture = errors.New("event start_date should not be in the future")
var ErrIncidentUpdatesShouldBeEmpty = errors.New("event updates should be empty")
var ErrIncidentDescriptionTooLong = errors.New("event description should be 1500 characters or fewer")
var ErrIncidentTypeImpactMismatch = errors.New(
	"impact must be 0 for type 'maintenance' or 'info' and gt 0 for 'incident'",
)
var ErrIncidentInvalidType = errors.New("event type is invalid, must be 'maintenance' 'info' or 'incident'")

// Errors for creating incident

var ErrIncidentSystemCreationWrongType = errors.New("system incident must be of type 'incident'")
var ErrIncidentCreationMaintenanceExists = errors.New(
	"event creation failed, component has active or planned maintenance",
)
var ErrIncidentCreationLowImpact = errors.New(
	"incident creation failed, exists the incident with higher impact for component",
)

var ErrIncidentFQueryInvalidFormat = errors.New("filter query parameter has an invalid format or value")

// Errors for patching incident

var ErrIncidentPatchMaintenanceImpactForbidden = errors.New("can not change impact for maintenance")
var ErrIncidentPatchMaintenanceStatus = errors.New("wrong status for maintenance")
var ErrIncidentPatchInfoStatus = errors.New("wrong status for info event")
var ErrIncidentPatchIncidentStatus = errors.New("wrong status for incident")
var ErrIncidentPatchClosedStatus = errors.New("wrong status for closed incident")
var ErrIncidentPatchOpenedStartDate = errors.New("can not change start date for open incident")
var ErrIncidentPatchOpenedEndDateMissing = errors.New("wrong end date with resolved status")
var ErrIncidentPatchImpactStatusWrong = errors.New("wrong status for changing impact")
var ErrIncidentPatchTypeForbidden = errors.New("can not change event type")

var ErrMaintenanceEndDateEmpty = errors.New("maintenance end_date is empty")

var ErrUpdateTextEmpty = errors.New("text field is required")
var ErrUpdateDSNotExist = errors.New("update does not exist")

// Errors for maintenance validation

var ErrMaintenanceContactEmailRequired = errors.New("contact_email is required for maintenance")
var ErrMaintenanceContactEmailInvalid = errors.New("contact_email has invalid format")
var ErrMaintenanceEndDateBeforeStart = errors.New("end_date must be after start_date")
var ErrMaintenanceDescriptionRequired = errors.New("description is required for maintenance")

// NewErrMaintenanceContactEmailDomain names the permitted domains so the caller can
// fix the request without reading the deployment configuration.
func NewErrMaintenanceContactEmailDomain(allowed []string) error {
	return fmt.Errorf("contact_email domain is not allowed, permitted domains: %s", strings.Join(allowed, ", "))
}

// NewErrNotificationStatusInvalid names the statuses accepted by ?status=.
func NewErrNotificationStatusInvalid(allowed []string) error {
	return fmt.Errorf("invalid status, expected one of: %s", strings.Join(allowed, ", "))
}

// NewErrNotificationLimitInvalid states the accepted range for ?limit=.
func NewErrNotificationLimitInvalid(maxLimit int) error {
	return fmt.Errorf("invalid limit, expected a number in range 1:%d", maxLimit)
}

// NewErrNotificationRedriveLimitInvalid states the accepted maximum size for the
// re-drive id list.
func NewErrNotificationRedriveLimitInvalid(maxIDs int) error {
	return fmt.Errorf("too many ids, expected at most %d", maxIDs)
}

// Errors for extract restrictions

var ErrExtractForbiddenRole = errors.New("extract is only available for operators and admins")
var ErrExtractForbiddenType = errors.New("extract is only available for incident type events")

// Errors for version conflict (optimistic locking)

var ErrVersionConflict = errors.New("version conflict: event has been modified by another user")
var ErrMaintenanceStatusTransitionConflict = errors.New("status transition not allowed for current event state")
