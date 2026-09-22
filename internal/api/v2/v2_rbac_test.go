package v2

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/stackmon/otc-status-dashboard/internal/api/rbac"
	"github.com/stackmon/otc-status-dashboard/internal/db"
	"github.com/stackmon/otc-status-dashboard/internal/event"

	apiErrors "github.com/stackmon/otc-status-dashboard/internal/api/errors"
)

func TestResolveMaintenanceCreateStatus(t *testing.T) {
	tests := []struct {
		name           string
		role           rbac.Role
		expectedStatus event.Status
		expectForbid   bool
	}{
		{
			name:           "Admin creates maintenance with planned status",
			role:           rbac.Admin,
			expectedStatus: event.MaintenancePlanned,
			expectForbid:   false,
		},
		{
			name:           "Operator creates maintenance with planned status",
			role:           rbac.Operator,
			expectedStatus: event.MaintenancePlanned,
			expectForbid:   false,
		},
		{
			name:           "Creator creates maintenance with pending review status",
			role:           rbac.Creator,
			expectedStatus: event.MaintenancePendingReview,
			expectForbid:   false,
		},
		{
			name:           "NoRole is forbidden",
			role:           rbac.NoRole,
			expectedStatus: "",
			expectForbid:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, err := resolveMaintenanceCreateStatus(tc.role)

			assert.Equal(t, tc.expectedStatus, status)
			if tc.expectForbid {
				assert.ErrorIs(t, err, apiErrors.ErrInsufficientRole)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestAllowMaintenancePatch(t *testing.T) {
	tests := []struct {
		name           string
		role           rbac.Role
		storedStatus   event.Status
		incomingStatus event.Status
		expectAllow    bool
		expectStatus   int
	}{
		// Admin tests - always allowed
		{
			name:           "Admin can patch pending review",
			role:           rbac.Admin,
			storedStatus:   event.MaintenancePendingReview,
			incomingStatus: event.MaintenanceReviewed,
			expectAllow:    true,
		},
		{
			name:           "Admin can patch reviewed",
			role:           rbac.Admin,
			storedStatus:   event.MaintenanceReviewed,
			incomingStatus: event.MaintenancePlanned,
			expectAllow:    true,
		},
		{
			name:           "Admin can patch planned",
			role:           rbac.Admin,
			storedStatus:   event.MaintenancePlanned,
			incomingStatus: event.MaintenanceCancelled,
			expectAllow:    true,
		},

		// Operator tests — unrestricted (event admin), same as Admin
		{
			name:           "Operator can approve pending review to reviewed",
			role:           rbac.Operator,
			storedStatus:   event.MaintenancePendingReview,
			incomingStatus: event.MaintenanceReviewed,
			expectAllow:    true,
		},
		{
			name:           "Operator can cancel pending review",
			role:           rbac.Operator,
			storedStatus:   event.MaintenancePendingReview,
			incomingStatus: event.MaintenanceCancelled,
			expectAllow:    true,
		},
		{
			name:           "Operator can update pending review",
			role:           rbac.Operator,
			storedStatus:   event.MaintenancePendingReview,
			incomingStatus: event.MaintenancePendingReview,
			expectAllow:    true,
		},
		{
			name:           "Operator can patch reviewed status",
			role:           rbac.Operator,
			storedStatus:   event.MaintenanceReviewed,
			incomingStatus: event.MaintenancePlanned,
			expectAllow:    true,
		},
		{
			name:           "Operator can patch planned status",
			role:           rbac.Operator,
			storedStatus:   event.MaintenancePlanned,
			incomingStatus: event.MaintenanceCancelled,
			expectAllow:    true,
		},

		// Creator tests
		{
			name:           "Creator can update pending review",
			role:           rbac.Creator,
			storedStatus:   event.MaintenancePendingReview,
			incomingStatus: event.MaintenancePendingReview,
			expectAllow:    true,
		},
		{
			name:           "Creator can cancel pending review",
			role:           rbac.Creator,
			storedStatus:   event.MaintenancePendingReview,
			incomingStatus: event.MaintenanceCancelled,
			expectAllow:    true,
		},
		{
			name:           "Creator cannot approve to reviewed",
			role:           rbac.Creator,
			storedStatus:   event.MaintenancePendingReview,
			incomingStatus: event.MaintenanceReviewed,
			expectAllow:    false,
			expectStatus:   409,
		},
		{
			name:           "Creator cannot patch reviewed status",
			role:           rbac.Creator,
			storedStatus:   event.MaintenanceReviewed,
			incomingStatus: event.MaintenancePendingReview,
			expectAllow:    false,
			expectStatus:   409,
		},
		{
			name:           "Creator cannot patch planned status",
			role:           rbac.Creator,
			storedStatus:   event.MaintenancePlanned,
			incomingStatus: event.MaintenanceCancelled,
			expectAllow:    false,
			expectStatus:   409,
		},

		// NoRole tests
		{
			name:           "NoRole is always forbidden",
			role:           rbac.NoRole,
			storedStatus:   event.MaintenancePendingReview,
			incomingStatus: event.MaintenancePendingReview,
			expectAllow:    false,
			expectStatus:   403,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			logger := zap.NewNop()

			// Set up user context for creator tests
			userID := "test-user-123"
			c.Set(UserIDContextKey, userID)

			stored := &db.Incident{
				Status:    tc.storedStatus,
				CreatedBy: &userID,
			}
			incoming := &PatchIncidentData{
				Status: tc.incomingStatus,
			}

			result := allowMaintenancePatch(c, logger, tc.role, stored, incoming)

			assert.Equal(t, tc.expectAllow, result)
			if !tc.expectAllow {
				assert.Equal(t, tc.expectStatus, w.Code)
				switch tc.expectStatus {
				case 409:
					assert.Contains(t, w.Body.String(), apiErrors.ErrMaintenanceStatusTransitionConflict.Error())
				case 403:
					assert.Contains(t, w.Body.String(), apiErrors.ErrAuthForbidden.Error())
				}
			}
		})
	}
}

func TestAllowMaintenancePatchAsCreator(t *testing.T) {
	tests := []struct {
		name           string
		storedStatus   event.Status
		incomingStatus event.Status
		expectAllow    bool
	}{
		{
			name:           "Update: pending review stays pending review",
			storedStatus:   event.MaintenancePendingReview,
			incomingStatus: event.MaintenancePendingReview,
			expectAllow:    true,
		},
		{
			name:           "Cancel: pending review to cancelled",
			storedStatus:   event.MaintenancePendingReview,
			incomingStatus: event.MaintenanceCancelled,
			expectAllow:    true,
		},
		{
			name:           "Forbidden: approve to reviewed",
			storedStatus:   event.MaintenancePendingReview,
			incomingStatus: event.MaintenanceReviewed,
			expectAllow:    false,
		},
		{
			name:           "Forbidden: change to planned",
			storedStatus:   event.MaintenancePendingReview,
			incomingStatus: event.MaintenancePlanned,
			expectAllow:    false,
		},
		{
			name:           "Forbidden: reviewed status",
			storedStatus:   event.MaintenanceReviewed,
			incomingStatus: event.MaintenanceCancelled,
			expectAllow:    false,
		},
		{
			name:           "Forbidden: planned status",
			storedStatus:   event.MaintenancePlanned,
			incomingStatus: event.MaintenanceCancelled,
			expectAllow:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			logger := zap.NewNop()

			// Set up user context
			userID := "test-user-123"
			c.Set(UserIDContextKey, userID)

			stored := &db.Incident{
				Status:    tc.storedStatus,
				CreatedBy: &userID,
			}
			incoming := &PatchIncidentData{Status: tc.incomingStatus}

			result := allowMaintenancePatchAsCreator(c, logger, stored, incoming)

			assert.Equal(t, tc.expectAllow, result)
			if !tc.expectAllow {
				assert.Equal(t, 409, w.Code)
				assert.Contains(t, w.Body.String(), apiErrors.ErrMaintenanceStatusTransitionConflict.Error())
			}
		})
	}
}

func TestGetRoleFromContext(t *testing.T) {
	tests := []struct {
		name         string
		setRole      bool
		roleVal      interface{}
		expectRole   rbac.Role
		expectOk     bool
		expectStatus int
	}{
		{
			name:         "Valid role in context",
			setRole:      true,
			roleVal:      rbac.Creator,
			expectRole:   rbac.Creator,
			expectOk:     true,
			expectStatus: 200,
		},
		{
			name:         "Missing role in context",
			setRole:      false,
			expectRole:   rbac.NoRole,
			expectOk:     false,
			expectStatus: 403,
		},
		{
			name:         "Wrong type in context",
			setRole:      true,
			roleVal:      "not-a-role",
			expectRole:   rbac.NoRole,
			expectOk:     false,
			expectStatus: 403,
		},
		{
			name:         "Integer in context instead of rbac.Role",
			setRole:      true,
			roleVal:      42,
			expectRole:   rbac.NoRole,
			expectOk:     false,
			expectStatus: 403,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			logger := zap.NewNop()

			if tc.setRole {
				c.Set("role", tc.roleVal)
			}

			role, ok := getRoleFromContext(c, logger)

			assert.Equal(t, tc.expectOk, ok)
			assert.Equal(t, tc.expectRole, role)
			if !tc.expectOk {
				assert.Equal(t, tc.expectStatus, w.Code)
			}
		})
	}
}

func TestGetUserIDFromContext(t *testing.T) {
	tests := []struct {
		name      string
		setValue  bool
		value     interface{}
		expectNil bool
		expectUID string
	}{
		{
			name:      "Valid userID",
			setValue:  true,
			value:     "test-user",
			expectNil: false,
			expectUID: "test-user",
		},
		{
			name:      "Missing userID key",
			setValue:  false,
			expectNil: true,
		},
		{
			name:      "Empty string userID",
			setValue:  true,
			value:     "",
			expectNil: true,
		},
		{
			name:      "Non-string type",
			setValue:  true,
			value:     12345,
			expectNil: true,
		},
		{
			name:      "Nil value",
			setValue:  true,
			value:     nil,
			expectNil: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			if tc.setValue {
				c.Set(UserIDContextKey, tc.value)
			}

			result := getUserIDFromContext(c)

			if tc.expectNil {
				assert.Nil(t, result)
			} else {
				assert.NotNil(t, result)
				assert.Equal(t, tc.expectUID, *result)
			}
		})
	}
}

func TestAllowMaintenancePatchAsCreatorOwnership(t *testing.T) {
	otherUser := "other-user"
	sameUser := "user-a"
	emptyUser := ""

	tests := []struct {
		name           string
		setUser        bool
		userID         string
		createdBy      *string
		incomingStatus event.Status
		expectAllow    bool
		expectStatus   int
		expectErrMsg   string
	}{
		{
			name:           "No userID in context",
			setUser:        false,
			createdBy:      &otherUser,
			incomingStatus: event.MaintenancePendingReview,
			expectAllow:    false,
			expectStatus:   403,
			expectErrMsg:   apiErrors.ErrAuthForbidden.Error(),
		},
		{
			name:           "CreatedBy is nil",
			setUser:        true,
			userID:         "user-a",
			createdBy:      nil,
			incomingStatus: event.MaintenancePendingReview,
			expectAllow:    false,
			expectStatus:   403,
			expectErrMsg:   apiErrors.ErrAuthForbidden.Error(),
		},
		{
			name:           "Both nil: no userID and nil CreatedBy",
			setUser:        false,
			createdBy:      nil,
			incomingStatus: event.MaintenancePendingReview,
			expectAllow:    false,
			expectStatus:   403,
			expectErrMsg:   apiErrors.ErrAuthForbidden.Error(),
		},
		{
			name:           "Mismatched users",
			setUser:        true,
			userID:         "user-a",
			createdBy:      &otherUser,
			incomingStatus: event.MaintenancePendingReview,
			expectAllow:    false,
			expectStatus:   403,
			expectErrMsg:   apiErrors.ErrAuthForbidden.Error(),
		},
		{
			name:           "Empty string userID vs non-empty CreatedBy",
			setUser:        true,
			userID:         "",
			createdBy:      &otherUser,
			incomingStatus: event.MaintenancePendingReview,
			expectAllow:    false,
			expectStatus:   403,
			expectErrMsg:   apiErrors.ErrAuthForbidden.Error(),
		},
		{
			name:           "Non-empty userID vs empty string CreatedBy",
			setUser:        true,
			userID:         "user-a",
			createdBy:      &emptyUser,
			incomingStatus: event.MaintenancePendingReview,
			expectAllow:    false,
			expectStatus:   403,
			expectErrMsg:   apiErrors.ErrAuthForbidden.Error(),
		},
		// SUCCESS cases: owner patches own event
		{
			name:           "Owner updates pending_review (stays pending_review)",
			setUser:        true,
			userID:         "user-a",
			createdBy:      &sameUser,
			incomingStatus: event.MaintenancePendingReview,
			expectAllow:    true,
			expectStatus:   200,
		},
		{
			name:           "Owner cancels pending_review event",
			setUser:        true,
			userID:         "user-a",
			createdBy:      &sameUser,
			incomingStatus: event.MaintenanceCancelled,
			expectAllow:    true,
			expectStatus:   200,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			logger := zap.NewNop()

			if tc.setUser {
				c.Set(UserIDContextKey, tc.userID)
			}

			stored := &db.Incident{
				Status:    event.MaintenancePendingReview,
				CreatedBy: tc.createdBy,
			}
			incoming := &PatchIncidentData{Status: tc.incomingStatus}

			result := allowMaintenancePatchAsCreator(c, logger, stored, incoming)

			assert.Equal(t, tc.expectAllow, result)
			assert.Equal(t, tc.expectStatus, w.Code)

			if tc.expectErrMsg != "" {
				assert.Contains(t, w.Body.String(), tc.expectErrMsg)
			}
		})
	}
}

func TestPrepareIncidentCreateNonMaintenance(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set(RoleContextKey, rbac.Creator)
	logger := zap.NewNop()

	impact := 1
	startDate := time.Now().Add(-time.Hour).UTC()
	incData := &IncidentData{
		Title:       "Test incident",
		Description: "desc",
		Impact:      &impact,
		Components:  []int{1},
		StartDate:   startDate,
		Type:        event.TypeIncident,
	}

	result := prepareIncidentCreate(c, logger, incData)

	assert.True(t, result, "non-maintenance should pass without status resolution")
	assert.Empty(t, incData.Status, "status should not be set for non-maintenance")
}

func TestPrepareIncidentCreateReporterScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger := zap.NewNop()

	impact := 1
	startDate := time.Now().Add(-time.Hour).UTC()

	tests := []struct {
		name         string
		typ          string
		impact       int
		system       *bool
		expectAllow  bool
		expectStatus int
	}{
		{
			name:         "reporter creates system incident",
			typ:          event.TypeIncident,
			impact:       1,
			system:       boolPtr(true),
			expectAllow:  true,
			expectStatus: 200,
		},
		{
			name:         "reporter creates non-system incident",
			typ:          event.TypeIncident,
			impact:       1,
			system:       boolPtr(false),
			expectAllow:  false,
			expectStatus: 403,
		},
		{
			name:         "reporter creates incident without the system flag",
			typ:          event.TypeIncident,
			impact:       1,
			expectAllow:  false,
			expectStatus: 403,
		},
		{
			name:         "reporter creates info event",
			typ:          event.TypeInformation,
			impact:       0,
			system:       boolPtr(false),
			expectAllow:  false,
			expectStatus: 403,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Set(RoleContextKey, rbac.Reporter)

			impactVal := tc.impact
			incData := &IncidentData{
				Title:      "Reporter event",
				Impact:     &impactVal,
				Components: []int{1},
				StartDate:  startDate,
				Type:       tc.typ,
				System:     tc.system,
			}

			result := prepareIncidentCreate(c, logger, incData)

			assert.Equal(t, tc.expectAllow, result)
			assert.Equal(t, tc.expectStatus, w.Code)
		})
	}

	t.Run("creator keeps access to non-system incidents", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set(RoleContextKey, rbac.Creator)

		incData := &IncidentData{
			Title:      "Creator incident",
			Impact:     &impact,
			Components: []int{1},
			StartDate:  startDate,
			Type:       event.TypeIncident,
			System:     boolPtr(false),
		}

		assert.True(t, prepareIncidentCreate(c, logger, incData))
		assert.Equal(t, 200, w.Code)
	})

	t.Run("missing role in context is forbidden", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		incData := &IncidentData{
			Title:      "Anonymous incident",
			Impact:     &impact,
			Components: []int{1},
			StartDate:  startDate,
			Type:       event.TypeIncident,
			System:     boolPtr(true),
		}

		assert.False(t, prepareIncidentCreate(c, logger, incData))
		assert.Equal(t, http.StatusForbidden, w.Code)
	})
}

func boolPtr(v bool) *bool { return &v }

func TestPrepareIncidentPatchNonMaintenance(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	logger := zap.NewNop()

	impact := 2
	stored := &db.Incident{
		Type:   event.TypeIncident,
		Status: event.IncidentAnalysing,
		Impact: &impact,
	}
	version := 1
	incoming := &PatchIncidentData{
		Message:    "update",
		Status:     event.IncidentResolved,
		UpdateDate: time.Now().UTC(),
		Version:    &version,
	}

	result := prepareIncidentPatch(c, logger, stored, incoming)

	assert.True(t, result, "non-maintenance incident patch should pass without RBAC check")
}

func TestGetComponentsToExtract(t *testing.T) {
	stored := []db.Component{
		{ID: 1, Name: "Comp-A"},
		{ID: 2, Name: "Comp-B"},
		{ID: 3, Name: "Comp-C"},
	}

	tests := []struct {
		name        string
		requestIDs  []int
		expectCount int
		expectErr   string
	}{
		{
			name:        "Valid: extract one component",
			requestIDs:  []int{1},
			expectCount: 1,
		},
		{
			name:        "Valid: extract two of three components",
			requestIDs:  []int{1, 3},
			expectCount: 2,
		},
		{
			name:       "Error: component not in incident",
			requestIDs: []int{999},
			expectErr:  "component 999 is not in the incident",
		},
		{
			name:       "Error: one valid and one invalid",
			requestIDs: []int{1, 42},
			expectErr:  "component 42 is not in the incident",
		},
		{
			name:       "Error: move all components",
			requestIDs: []int{1, 2, 3},
			expectErr:  "can not move all components",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := getComponentsToExtract(tc.requestIDs, stored)
			if tc.expectErr != "" {
				require.ErrorContains(t, err, tc.expectErr)
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				assert.Len(t, result, tc.expectCount)
			}
		})
	}
}

func TestExtractRoleRestriction(t *testing.T) {
	tests := []struct {
		name         string
		role         rbac.Role
		eventType    string
		expectStatus int
		expectErr    string
	}{
		{
			name:         "Admin can extract incident",
			role:         rbac.Admin,
			eventType:    event.TypeIncident,
			expectStatus: 400, // passes role+type checks, fails on empty body
		},
		{
			name:         "Operator can extract incident",
			role:         rbac.Operator,
			eventType:    event.TypeIncident,
			expectStatus: 400, // passes role+type checks, fails on empty body
		},
		{
			name:         "Creator cannot extract incident",
			role:         rbac.Creator,
			eventType:    event.TypeIncident,
			expectStatus: 403,
			expectErr:    apiErrors.ErrExtractForbiddenRole.Error(),
		},
		{
			name:         "NoRole cannot extract incident",
			role:         rbac.NoRole,
			eventType:    event.TypeIncident,
			expectStatus: 403,
			expectErr:    apiErrors.ErrAuthForbidden.Error(),
		},
		{
			name:         "Admin cannot extract maintenance",
			role:         rbac.Admin,
			eventType:    event.TypeMaintenance,
			expectStatus: 403,
			expectErr:    apiErrors.ErrExtractForbiddenType.Error(),
		},
		{
			name:         "Admin cannot extract info",
			role:         rbac.Admin,
			eventType:    event.TypeInformation,
			expectStatus: 403,
			expectErr:    apiErrors.ErrExtractForbiddenType.Error(),
		},
		{
			name:         "Operator cannot extract maintenance",
			role:         rbac.Operator,
			eventType:    event.TypeMaintenance,
			expectStatus: 403,
			expectErr:    apiErrors.ErrExtractForbiddenType.Error(),
		},
		{
			name:         "Operator cannot extract info",
			role:         rbac.Operator,
			eventType:    event.TypeInformation,
			expectStatus: 403,
			expectErr:    apiErrors.ErrExtractForbiddenType.Error(),
		},
		{
			name:         "Creator cannot extract maintenance",
			role:         rbac.Creator,
			eventType:    event.TypeMaintenance,
			expectStatus: 403,
			expectErr:    apiErrors.ErrExtractForbiddenRole.Error(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			// Provide an HTTP request so body binding doesn't panic
			// when role+type checks pass through.
			c.Request = httptest.NewRequest(
				http.MethodPost, "/", strings.NewReader(`{}`),
			)
			c.Request.Header.Set("Content-Type", "application/json")

			impact := 2
			stored := &db.Incident{
				ID:     1,
				Type:   tc.eventType,
				Impact: &impact,
				Components: []db.Component{
					{ID: 1, Name: "Comp-A"},
					{ID: 2, Name: "Comp-B"},
				},
			}
			c.Set("event", stored)

			if tc.role != rbac.NoRole {
				c.Set(RoleContextKey, tc.role)
			}

			handler := PostIncidentExtractHandler(nil, zap.NewNop())
			handler(c)

			assert.Equal(t, tc.expectStatus, w.Code)
			if tc.expectErr != "" {
				assert.Contains(t, w.Body.String(), tc.expectErr)
			}
		})
	}
}
