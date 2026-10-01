package v2

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stackmon/otc-status-dashboard/internal/api/errors"
	"github.com/stackmon/otc-status-dashboard/internal/db"
	"github.com/stackmon/otc-status-dashboard/internal/event"
)

func TestValidateMaintenanceCreation(t *testing.T) {
	futureTime := time.Now().Add(24 * time.Hour)
	laterTime := futureTime.Add(2 * time.Hour)
	pastTime := time.Now().Add(-24 * time.Hour)

	tests := []struct {
		name        string
		incData     IncidentData
		expectError bool
		errorMsg    string
	}{
		{
			name: "Valid maintenance with all required fields",
			incData: IncidentData{
				ContactEmail: "user@example.com",
				StartDate:    futureTime,
				EndDate:      &laterTime,
				Description:  "Scheduled maintenance",
			},
			expectError: false,
		},
		{
			name: "Missing contact_email is allowed, resolved from the token later",
			incData: IncidentData{
				ContactEmail: "",
				StartDate:    futureTime,
				EndDate:      &laterTime,
				Description:  "Scheduled maintenance",
			},
			expectError: false,
		},
		{
			name: "Invalid contact_email format",
			incData: IncidentData{
				ContactEmail: "not-an-email",
				StartDate:    futureTime,
				EndDate:      &laterTime,
				Description:  "Scheduled maintenance",
			},
			expectError: true,
			errorMsg:    "invalid format",
		},
		{
			name: "End date before start date",
			incData: IncidentData{
				ContactEmail: "user@example.com",
				StartDate:    futureTime,
				EndDate:      &pastTime,
				Description:  "Scheduled maintenance",
			},
			expectError: true,
			errorMsg:    "end_date must be after start_date",
		},
		{
			name: "Empty description",
			incData: IncidentData{
				ContactEmail: "user@example.com",
				StartDate:    futureTime,
				EndDate:      &laterTime,
				Description:  "",
			},
			expectError: true,
			errorMsg:    "description is required",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateMaintenanceCreation(tc.incData)

			if tc.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errorMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateStatusesPatches(t *testing.T) {
	// Create test incidents for different types
	infoEvent := &db.Incident{
		Type: event.TypeInformation,
	}

	maintenance := &db.Incident{
		Type: event.TypeMaintenance,
	}

	incident := &db.Incident{
		Type: event.TypeIncident,
	}

	testCases := []struct {
		name        string
		incoming    *PatchIncidentData
		stored      *db.Incident
		expectError bool
		expectedErr error
	}{
		// Information event status tests - all information statuses
		{
			name: "Valid InfoPlanned status for info incident",
			incoming: &PatchIncidentData{
				Status: event.InfoPlanned,
			},
			stored:      infoEvent,
			expectError: false,
		},
		{
			name: "Valid InfoCompleted status for info incident",
			incoming: &PatchIncidentData{
				Status: event.InfoCompleted,
			},
			stored:      infoEvent,
			expectError: false,
		},
		{
			name: "Valid InfoCancelled status for info incident",
			incoming: &PatchIncidentData{
				Status: event.InfoCancelled,
			},
			stored:      infoEvent,
			expectError: false,
		},
		{
			name: "Valid MaintenancePlanned status for info incident, both have same status - planned",
			incoming: &PatchIncidentData{
				Status: event.MaintenancePlanned,
			},
			stored:      infoEvent,
			expectError: false,
		},

		// Maintenance event status tests - all maintenance statuses
		{
			name: "Valid MaintenancePlanned status for maintenance incident",
			incoming: &PatchIncidentData{
				Status: event.MaintenancePlanned,
			},
			stored:      maintenance,
			expectError: false,
		},
		{
			name: "Valid MaintenanceInProgress status for maintenance incident",
			incoming: &PatchIncidentData{
				Status: event.MaintenanceInProgress,
			},
			stored:      maintenance,
			expectError: false,
		},
		{
			name: "Valid MaintenanceCompleted status for maintenance incident",
			incoming: &PatchIncidentData{
				Status: event.MaintenanceCompleted,
			},
			stored:      maintenance,
			expectError: false,
		},
		{
			name: "Valid MaintenanceCancelled status for maintenance incident",
			incoming: &PatchIncidentData{
				Status: event.MaintenanceCancelled,
			},
			stored:      maintenance,
			expectError: false,
		},
		{
			name: "Valid InfoPlanned status for maintenance incident, both have same status - planned",
			incoming: &PatchIncidentData{
				Status: event.InfoPlanned,
			},
			stored:      maintenance,
			expectError: false,
		},

		// Incident event status tests - open statuses
		{
			name: "Valid IncidentDetected status for incident",
			incoming: &PatchIncidentData{
				Status: event.IncidentDetected,
			},
			stored:      incident,
			expectError: false,
		},
		{
			name: "Valid IncidentAnalysing status for incident",
			incoming: &PatchIncidentData{
				Status: event.IncidentAnalysing,
			},
			stored:      incident,
			expectError: false,
		},
		{
			name: "Valid IncidentImpactChanged status for incident",
			incoming: &PatchIncidentData{
				Status: event.IncidentImpactChanged,
			},
			stored:      incident,
			expectError: false,
		},
		{
			name: "Valid IncidentReopened status for incident",
			incoming: &PatchIncidentData{
				Status: event.IncidentReopened,
			},
			stored:      incident,
			expectError: false,
		},
		{
			name: "Valid IncidentChanged status for incident",
			incoming: &PatchIncidentData{
				Status: event.IncidentChanged,
			},
			stored:      incident,
			expectError: false,
		},

		// Incident event status tests - closed statuses
		{
			name: "Valid IncidentResolved status for incident",
			incoming: &PatchIncidentData{
				Status: event.IncidentResolved,
			},
			stored:      incident,
			expectError: false,
		},

		// Invalid status combinations - info incident with non-info statuses

		{
			name: "Invalid IncidentDetected status for info incident",
			incoming: &PatchIncidentData{
				Status: event.IncidentDetected,
			},
			stored:      infoEvent,
			expectError: true,
			expectedErr: errors.ErrIncidentPatchInfoStatus,
		},
		{
			name: "Invalid IncidentResolved status for info incident",
			incoming: &PatchIncidentData{
				Status: event.IncidentResolved,
			},
			stored:      infoEvent,
			expectError: true,
			expectedErr: errors.ErrIncidentPatchInfoStatus,
		},

		// Invalid status combinations - maintenance incident with non-maintenance statuses
		{
			name: "Invalid IncidentDetected status for maintenance incident",
			incoming: &PatchIncidentData{
				Status: event.IncidentDetected,
			},
			stored:      maintenance,
			expectError: true,
			expectedErr: errors.ErrIncidentPatchMaintenanceStatus,
		},
		{
			name: "Invalid IncidentResolved status for maintenance incident",
			incoming: &PatchIncidentData{
				Status: event.IncidentResolved,
			},
			stored:      maintenance,
			expectError: true,
			expectedErr: errors.ErrIncidentPatchMaintenanceStatus,
		},

		// Invalid status combinations - incident with non-incident statuses
		{
			name: "Invalid InfoPlanned status for incident",
			incoming: &PatchIncidentData{
				Status: event.InfoPlanned,
			},
			stored:      incident,
			expectError: true,
			expectedErr: errors.ErrIncidentPatchIncidentStatus,
		},
		{
			name: "Invalid InfoCompleted status for incident",
			incoming: &PatchIncidentData{
				Status: event.InfoCompleted,
			},
			stored:      incident,
			expectError: true,
			expectedErr: errors.ErrIncidentPatchIncidentStatus,
		},
		{
			name: "Invalid MaintenancePlanned status for incident",
			incoming: &PatchIncidentData{
				Status: event.MaintenancePlanned,
			},
			stored:      incident,
			expectError: true,
			expectedErr: errors.ErrIncidentPatchIncidentStatus,
		},
		{
			name: "Invalid MaintenanceCompleted status for incident",
			incoming: &PatchIncidentData{
				Status: event.MaintenanceCompleted,
			},
			stored:      incident,
			expectError: true,
			expectedErr: errors.ErrIncidentPatchIncidentStatus,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateStatusesPatch(tc.incoming, tc.stored)

			if tc.expectError {
				require.Error(t, err)
				if tc.expectedErr != nil {
					assert.Equal(t, tc.expectedErr, err)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateEventCreation(t *testing.T) {
	zeroImpact := 0
	highImpact := 1
	futureTime := time.Now().Add(1 * time.Hour)

	tests := []struct {
		name        string
		incData     IncidentData
		expectError bool
		errorMsg    string
	}{
		{
			name: "Valid maintenance event",
			incData: IncidentData{
				Type:      event.TypeMaintenance,
				Impact:    &zeroImpact,
				StartDate: futureTime,
				EndDate:   &futureTime,
			},
			expectError: false,
		},
		{
			name: "Invalid maintenance impact",
			incData: IncidentData{
				Type:      event.TypeMaintenance,
				Impact:    &highImpact,
				StartDate: futureTime,
				EndDate:   &futureTime,
			},
			expectError: true,
			errorMsg:    errors.ErrIncidentTypeImpactMismatch.Error(),
		},
		{
			name: "Invalid incident start date (future)",
			incData: IncidentData{
				Type:      event.TypeIncident,
				Impact:    &highImpact,
				StartDate: futureTime,
			},
			expectError: true,
			errorMsg:    errors.ErrIncidentStartDateInFuture.Error(),
		},
		{
			name: "Invalid updates not empty",
			incData: IncidentData{
				Type:      event.TypeMaintenance,
				Impact:    &zeroImpact,
				StartDate: futureTime,
				EndDate:   &futureTime,
				Updates:   []EventUpdateData{{Text: "update"}},
			},
			expectError: true,
			errorMsg:    errors.ErrIncidentUpdatesShouldBeEmpty.Error(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateEventCreation(tc.incData)
			if tc.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errorMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateEventCreationImpact(t *testing.T) {
	zeroImpact := 0
	highImpact := 1

	tests := []struct {
		name        string
		incData     IncidentData
		expectError bool
		expectedErr error
	}{
		{
			name: "Maintenance with zero impact",
			incData: IncidentData{
				Type:   event.TypeMaintenance,
				Impact: &zeroImpact,
			},
			expectError: false,
		},
		{
			name: "Maintenance with high impact",
			incData: IncidentData{
				Type:   event.TypeMaintenance,
				Impact: &highImpact,
			},
			expectError: true,
			expectedErr: errors.ErrIncidentTypeImpactMismatch,
		},
		{
			name: "Info with zero impact",
			incData: IncidentData{
				Type:   event.TypeInformation,
				Impact: &zeroImpact,
			},
			expectError: false,
		},
		{
			name: "Info with high impact",
			incData: IncidentData{
				Type:   event.TypeInformation,
				Impact: &highImpact,
			},
			expectError: true,
			expectedErr: errors.ErrIncidentTypeImpactMismatch,
		},
		{
			name: "Incident with high impact",
			incData: IncidentData{
				Type:   event.TypeIncident,
				Impact: &highImpact,
			},
			expectError: false,
		},
		{
			name: "Incident with zero impact",
			incData: IncidentData{
				Type:   event.TypeIncident,
				Impact: &zeroImpact,
			},
			expectError: true,
			expectedErr: errors.ErrIncidentTypeImpactMismatch,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateEventCreationImpact(tc.incData)
			if tc.expectError {
				require.Error(t, err)
				assert.Equal(t, tc.expectedErr, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateEventCreationTimes(t *testing.T) {
	futureTime := time.Now().Add(1 * time.Hour)
	pastTime := time.Now().Add(-1 * time.Hour)

	tests := []struct {
		name        string
		incData     IncidentData
		expectError bool
		expectedErr error
	}{
		{
			name: "Incident with end date (forbidden)",
			incData: IncidentData{
				Type:      event.TypeIncident,
				StartDate: pastTime,
				EndDate:   &pastTime,
			},
			expectError: true,
			expectedErr: errors.ErrIncidentEndDateShouldBeEmpty,
		},
		{
			name: "Incident with future start date (forbidden)",
			incData: IncidentData{
				Type:      event.TypeIncident,
				StartDate: futureTime,
			},
			expectError: true,
			expectedErr: errors.ErrIncidentStartDateInFuture,
		},
		{
			name: "Maintenance without end date (forbidden)",
			incData: IncidentData{
				Type:      event.TypeMaintenance,
				StartDate: futureTime,
				EndDate:   nil,
			},
			expectError: true,
			expectedErr: errors.ErrMaintenanceEndDateEmpty,
		},
		{
			name: "Valid incident times",
			incData: IncidentData{
				Type:      event.TypeIncident,
				StartDate: pastTime,
			},
			expectError: false,
		},
		{
			name: "Valid maintenance times",
			incData: IncidentData{
				Type:      event.TypeMaintenance,
				StartDate: futureTime,
				EndDate:   &futureTime,
			},
			expectError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateEventCreationTimes(tc.incData)
			if tc.expectError {
				require.Error(t, err)
				assert.Equal(t, tc.expectedErr, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
