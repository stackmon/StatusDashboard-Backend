package rss

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/stackmon/otc-status-dashboard/internal/baseurl"
	"github.com/stackmon/otc-status-dashboard/internal/db"
)

// The DB is never touched on the guarded paths, so nil is safe here and
// pins that no query is issued for rejected input.
func newTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/rss/", HandleRSS(nil, zap.NewNop(), ""))
	return r
}

func doGet(t *testing.T, r *gin.Engine, target string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
	return w
}

// NUL bytes in the component filter must be rejected with a generic 400,
// never forwarded to the database (see issue #63).
func TestHandleRSSRejectsNulInComponent(t *testing.T) {
	w := doGet(t, newTestRouter(), "/rss/?mt=Global&srv=%00")

	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.NotContains(t, w.Body.String(), "SQLSTATE")
	assert.NotContains(t, w.Body.String(), "UTF8")
}

func TestHandleRSSRejectsInvalidUTF8InComponent(t *testing.T) {
	w := doGet(t, newTestRouter(), "/rss/?mt=Global&srv=%FF%FE")

	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.NotContains(t, w.Body.String(), "SQLSTATE")
	assert.NotContains(t, w.Body.String(), "UTF8")
}

// Pre-existing behavior must stay: srv without mt -> 404 with the static
// documentation hint.
func TestHandleRSSComponentWithoutRegion(t *testing.T) {
	w := doGet(t, newTestRouter(), "/rss/?srv=ECS")

	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "errMsg")
}

func testIncident() *db.Incident {
	impact := 2
	text := "ECS outage"
	start := time.Date(2026, 11, 5, 10, 30, 0, 0, time.UTC)

	return &db.Incident{ID: 42, Text: &text, Impact: &impact, StartDate: &start}
}

// Feed links must be absolute. Behind an ingress the request carries no scheme,
// so the scheme comes from the forwarded header or from the configured base URL.
func TestBuildFeedLinks(t *testing.T) {
	tests := []struct {
		name       string
		proto      string
		configured string
		expectBase string
	}{
		{name: "no forwarded proto", expectBase: "https://status.example.com"},
		{name: "forwarded proto", proto: "http", expectBase: "http://status.example.com"},
		{
			name:       "configured base URL",
			configured: "https://public.example.com",
			expectBase: "https://public.example.com",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/rss/?mt=Global", nil)
			req.Host = "status.example.com"
			if tc.proto != "" {
				req.Header.Set("X-Forwarded-Proto", tc.proto)
			}

			feed := buildFeed("Incidents", baseurl.Resolve(req, tc.configured), req,
				[]*db.Incident{testIncident()})

			require.NotNil(t, feed.Link)
			assert.Equal(t, tc.expectBase+"/rss/?mt=Global", feed.Link.Href)

			require.Len(t, feed.Items, 1)
			assert.Equal(t, tc.expectBase+"/incidents/42", feed.Items[0].Link.Href)

			for _, link := range []string{feed.Link.Href, feed.Items[0].Link.Href} {
				u, err := url.Parse(link)
				require.NoError(t, err)
				assert.NotEmpty(t, u.Scheme)
				assert.NotEmpty(t, u.Host)
			}
		})
	}
}
