package v2

import (
	"net/mail"
	"strconv"
	"strings"

	apiErrors "github.com/stackmon/otc-status-dashboard/internal/api/errors"
	"github.com/stackmon/otc-status-dashboard/internal/db"
	"github.com/stackmon/otc-status-dashboard/internal/event"
)

const (
	maxComponentID = 2048
	// MaxEventComponents caps the number of components a single event can
	// reference; larger lists are rejected to bound incident processing and
	// notification fan-out.
	MaxEventComponents = 50
)

// ValidateEventComponents validates the component list of an event payload.
func ValidateEventComponents(components []int) error {
	if len(components) > MaxEventComponents {
		return apiErrors.ErrIncidentComponentsTooMany
	}

	seen := make(map[int]struct{}, len(components))
	for _, id := range components {
		if _, ok := seen[id]; ok {
			return apiErrors.ErrIncidentComponentsDuplicate
		}
		seen[id] = struct{}{}
	}

	return nil
}

// IsValidIncidentFilterStatus checks if the status is valid for maintenance or incidents.
func IsValidIncidentFilterStatus(status event.Status) bool {
	if event.IsMaintenanceStatus(status) {
		return true
	}

	if event.IsInformationStatus(status) {
		return true
	}

	if event.IsIncidentOpenStatus(status) {
		return true
	}

	if event.IsIncidentClosedStatus(status) {
		return true
	}

	return false
}

// validateAndSetStatus validates the query status and sets it on db.IncidentsParams.
func validateAndSetStatus(queryStatus *event.Status, params *db.IncidentsParams) error {
	if queryStatus != nil {
		if !IsValidIncidentFilterStatus(*queryStatus) {
			return apiErrors.ErrIncidentFQueryInvalidFormat
		}
		params.Status = queryStatus
	}
	return nil
}

// validateAndSetLimit validates the limit in the pagination query.
func validateAndSetLimit(queryLimit *int, params *db.IncidentsParams) error {
	var allowedLimits = map[int]struct{}{
		10: {},
		20: {},
		50: {},
	}

	if queryLimit != nil {
		if _, valid := allowedLimits[*queryLimit]; !valid {
			return apiErrors.ErrIncidentFQueryInvalidFormat
		}
		params.Limit = queryLimit
	}
	return nil
}

// parseAndSetComponents parses component IDs from a comma-separated string and sets them on db.IncidentsParams.
func parseAndSetComponents(queryComponents *string, params *db.IncidentsParams) error {
	if queryComponents == nil {
		return nil
	}

	compsStr := strings.Split(*queryComponents, ",")
	comps := make([]int, 0, len(compsStr))

	for _, comp := range compsStr {
		uid, err := strconv.Atoi(comp)
		if err != nil || uid <= 0 || uid > maxComponentID {
			return apiErrors.ErrIncidentFQueryInvalidFormat
		}
		comps = append(comps, uid)
	}

	params.ComponentIDs = comps

	return nil
}

// parseAndSetComponents parses component IDs from a comma-separated string and sets them on db.IncidentsParams.
func parseAndSetTypes(queryTypes *string, params *db.IncidentsParams) error {
	if queryTypes == nil {
		return nil
	}

	typesStr := strings.Split(*queryTypes, ",")
	types := make([]string, 0, len(typesStr))

	for _, t := range typesStr {
		if t != event.TypeMaintenance && t != event.TypeInformation && t != event.TypeIncident {
			return apiErrors.ErrIncidentFQueryInvalidFormat
		}

		types = append(types, t)
	}

	params.Types = types

	return nil
}

// validateMaintenanceCreation validates maintenance-specific fields at creation time.
// Note: EndDate nil check is handled by validateEventCreation before this function is called.
func validateMaintenanceCreation(incData IncidentData) error {
	// Optional: prepareIncidentCreate falls back to the address from the token.
	if incData.ContactEmail != "" {
		if _, err := mail.ParseAddress(incData.ContactEmail); err != nil {
			return apiErrors.ErrMaintenanceContactEmailInvalid
		}
	}

	if incData.EndDate != nil && !incData.EndDate.After(incData.StartDate) {
		return apiErrors.ErrMaintenanceEndDateBeforeStart
	}

	if incData.Description == "" {
		return apiErrors.ErrMaintenanceDescriptionRequired
	}

	return nil
}
