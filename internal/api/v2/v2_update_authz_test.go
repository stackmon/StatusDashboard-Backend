package v2

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"github.com/stackmon/otc-status-dashboard/internal/api/rbac"
	"github.com/stackmon/otc-status-dashboard/internal/db"
)

func TestAllowEventUpdatePatch(t *testing.T) {
	creator := "creator-1"
	other := "other-1"
	endDate := time.Now().UTC()

	tests := []struct {
		name   string
		role   rbac.Role
		userID *string
		stored *db.Incident
		allow  bool
	}{
		{
			name:   "creator edits an open event",
			role:   rbac.Creator,
			userID: &creator,
			stored: &db.Incident{CreatedBy: &creator},
			allow:  true,
		},
		{
			name:   "another creator is forbidden",
			role:   rbac.Creator,
			userID: &other,
			stored: &db.Incident{CreatedBy: &creator},
		},
		{
			name:   "operator who is the event creator",
			role:   rbac.Operator,
			userID: &creator,
			stored: &db.Incident{CreatedBy: &creator},
			allow:  true,
		},
		{
			name:   "reporter who is the event creator is forbidden",
			role:   rbac.Reporter,
			userID: &creator,
			stored: &db.Incident{CreatedBy: &creator},
		},
		{
			name:   "operator edits another user's open event",
			role:   rbac.Operator,
			userID: &other,
			stored: &db.Incident{CreatedBy: &creator},
			allow:  true,
		},
		{
			name:   "operator edits another user's closed event",
			role:   rbac.Operator,
			userID: &other,
			stored: &db.Incident{CreatedBy: &creator, EndDate: &endDate},
			allow:  true,
		},
		{
			name:   "admin edits another user's open event",
			role:   rbac.Admin,
			userID: &other,
			stored: &db.Incident{CreatedBy: &creator},
			allow:  true,
		},
		{
			name:   "admin edits a closed event",
			role:   rbac.Admin,
			userID: &other,
			stored: &db.Incident{CreatedBy: &creator, EndDate: &endDate},
			allow:  true,
		},
		{
			name:   "creator cannot edit a closed event",
			role:   rbac.Creator,
			userID: &creator,
			stored: &db.Incident{CreatedBy: &creator, EndDate: &endDate},
		},
		{
			name:   "event without a creator is not editable by role alone",
			role:   rbac.Creator,
			userID: &creator,
			stored: &db.Incident{},
		},
		{
			name:   "anonymous caller is forbidden",
			role:   rbac.NoRole,
			stored: &db.Incident{CreatedBy: &creator},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			if tc.userID != nil {
				c.Set(UserIDContextKey, *tc.userID)
			}

			allowed := allowEventUpdatePatch(c, zap.NewNop(), tc.role, tc.stored)

			assert.Equal(t, tc.allow, allowed)
			if !tc.allow {
				assert.Equal(t, http.StatusForbidden, w.Code)
			}
		})
	}
}
