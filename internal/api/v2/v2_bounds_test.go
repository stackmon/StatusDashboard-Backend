package v2

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apiErrors "github.com/stackmon/otc-status-dashboard/internal/api/errors"
	"github.com/stackmon/otc-status-dashboard/internal/db"
)

func TestParsePaginationParamsRejectsOutOfRangeValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		query   string
		wantErr bool
	}{
		{
			name:    "page above int32",
			query:   "page=2147483648&limit=10",
			wantErr: true,
		},
		{
			name:    "maximum int64 page",
			query:   "page=9223372036854775807&limit=10",
			wantErr: true,
		},
		{
			name:    "negative page",
			query:   "page=-1&limit=10",
			wantErr: true,
		},
		{
			name:    "maximum int64 limit",
			query:   "page=1&limit=9223372036854775807",
			wantErr: true,
		},
		{
			name:    "negative limit",
			query:   "page=1&limit=-10",
			wantErr: true,
		},
		{
			name:    "valid pagination",
			query:   "page=2&limit=50",
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/v2/events?"+tc.query, nil)

			err := parsePaginationParams(c, &db.IncidentsParams{})
			if tc.wantErr {
				require.ErrorIs(t, err, apiErrors.ErrIncidentFQueryInvalidFormat)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestIDBindingRejectsOutOfRangeValues(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.GET("/v2/events/:eventID", func(c *gin.Context) {
		var id IncidentID
		if err := c.ShouldBindUri(&id); err != nil {
			c.Status(http.StatusBadRequest)
			return
		}
		c.Status(http.StatusOK)
	})
	r.GET("/v2/components/:id", func(c *gin.Context) {
		var id ComponentID
		if err := c.ShouldBindUri(&id); err != nil {
			c.Status(http.StatusBadRequest)
			return
		}
		c.Status(http.StatusOK)
	})

	tests := []struct {
		name       string
		path       string
		wantStatus int
	}{
		{
			name:       "event ID above int32",
			path:       "/v2/events/2147483648",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "maximum int64 event ID",
			path:       "/v2/events/9223372036854775807",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "negative event ID",
			path:       "/v2/events/-1",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "valid event ID",
			path:       "/v2/events/42",
			wantStatus: http.StatusOK,
		},
		{
			name:       "component ID above int32",
			path:       "/v2/components/2147483648",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "negative component ID",
			path:       "/v2/components/-1",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "valid component ID",
			path:       "/v2/components/42",
			wantStatus: http.StatusOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)

			r.ServeHTTP(w, req)

			assert.Equal(t, tc.wantStatus, w.Code)
		})
	}
}
