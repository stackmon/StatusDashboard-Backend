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
	"github.com/stackmon/otc-status-dashboard/internal/event"
)

func testTime() time.Time {
	return time.Date(2026, 11, 5, 10, 30, 0, 0, time.UTC)
}

func testBaseURL(t *testing.T) *url.URL {
	t.Helper()

	u, err := url.Parse("https://status.example.com")
	require.NoError(t, err)

	return u
}

func componentWithRegion(name, region string) db.Component {
	return db.Component{
		Name: name,
		Attrs: []db.ComponentAttr{
			{Name: "region", Value: region},
			{Name: "type", Value: "compute"},
		},
	}
}

func baseIncident(components []db.Component) *db.Incident {
	impact := 2
	text := "ECS outage"
	start := testTime()

	return &db.Incident{
		ID:         42,
		Text:       &text,
		Type:       event.TypeIncident,
		Impact:     &impact,
		StartDate:  &start,
		Components: components,
	}
}

// Incident events without components must not panic and must produce a
// generic item instead.
func TestCreateIncidentFeedItemsNoComponents(t *testing.T) {
	incident := baseIncident(nil)
	status := "fixing"
	incident.Statuses = []db.IncidentStatus{
		{Status: event.IncidentDetected, Text: "detected", Timestamp: testTime()},
		{Status: event.Status(status), Text: "we are on it", Timestamp: testTime().Add(time.Hour)},
	}

	items := createIncidentFeedItems(incident, testBaseURL(t))
	require.Len(t, items, 2, "main item plus one update (detected is skipped)")

	main := items[0]
	assert.Equal(t, "Status change to Major incident", main.Title)
	assert.Equal(t,
		"A Major incident was detected at 2026-11-05 10:30:00 UTC.",
		main.Description)
	assert.NotContains(t, main.Description, " for .", "description must not dangle on 'for'")
	assert.Equal(t, "https://status.example.com/incidents/42", main.Link.Href)

	upd := items[1]
	assert.Equal(t, "Update published for: ECS outage", upd.Title)
	assert.Equal(t,
		"An update was provided at 2026-11-05 11:30:00 UTC: fixing - we are on it",
		upd.Description)
}

func TestCreateIncidentFeedItemsSingleComponent(t *testing.T) {
	incident := baseIncident([]db.Component{componentWithRegion("ECS", "EU-DE")})
	incident.Statuses = []db.IncidentStatus{
		{Status: event.IncidentDetected, Text: "detected", Timestamp: testTime()},
		{Status: event.IncidentFixing, Text: "we are on it", Timestamp: testTime().Add(time.Hour)},
	}

	items := createIncidentFeedItems(incident, testBaseURL(t))
	require.Len(t, items, 2)

	assert.Equal(t, "ECS status changed to Major incident", items[0].Title)
	assert.Equal(t,
		"A Major incident was detected at 2026-11-05 10:30:00 UTC for ECS in EU-DE.",
		items[0].Description)
	assert.Equal(t,
		"An update was provided at 2026-11-05 11:30:00 UTC for ECS in EU-DE: fixing - we are on it",
		items[1].Description)
}

func TestCreateIncidentFeedItemsMultipleComponents(t *testing.T) {
	incident := baseIncident([]db.Component{
		componentWithRegion("ECS", "EU-DE"),
		componentWithRegion("EVS", "EU-NL"),
	})
	incident.Statuses = []db.IncidentStatus{
		{Status: event.IncidentDetected, Text: "detected", Timestamp: testTime()},
		{Status: event.IncidentFixing, Text: "we are on it", Timestamp: testTime().Add(time.Hour)},
	}

	items := createIncidentFeedItems(incident, testBaseURL(t))
	require.Len(t, items, 2)

	assert.Equal(t, "Status change of multiple services to Major incident", items[0].Title)
	assert.Equal(t,
		"A Major incident was detected at 2026-11-05 10:30:00 UTC for ECS in EU-DE, EVS in EU-NL.",
		items[0].Description)
	assert.Equal(t,
		"An update was provided at 2026-11-05 11:30:00 UTC for ECS (EU-DE), EVS (EU-NL): fixing - we are on it",
		items[1].Description)
}

// Maintenance events with no components must not panic either.
func TestCreateMaintenanceFeedItemsNoComponents(t *testing.T) {
	incident := baseIncident(nil)
	incident.Type = event.TypeMaintenance
	end := testTime().Add(2 * time.Hour)
	incident.EndDate = &end
	incident.Statuses = []db.IncidentStatus{
		{Status: event.MaintenancePlanned, Text: "planned", Timestamp: testTime()},
	}

	assert.NotPanics(t, func() {
		items := createFeedItems(incident, testBaseURL(t))
		require.NotEmpty(t, items)
	})
}

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
// never forwarded to the database.
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

func TestHandleRSSComponentWithoutRegion(t *testing.T) {
	w := doGet(t, newTestRouter(), "/rss/?srv=ECS")

	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "errMsg")
}

// createFeedItems must route by type without touching Components[0].
func TestCreateFeedItemsDispatchNoPanicOnEmptyComponents(t *testing.T) {
	for _, typ := range []string{event.TypeIncident, event.TypeMaintenance, event.TypeInformation} {
		t.Run(typ, func(t *testing.T) {
			incident := baseIncident([]db.Component{})
			incident.Type = typ
			end := testTime().Add(2 * time.Hour)
			incident.EndDate = &end
			incident.Statuses = []db.IncidentStatus{
				{Status: event.IncidentDetected, Text: "detected", Timestamp: testTime()},
			}

			assert.NotPanics(t, func() {
				createFeedItems(incident, testBaseURL(t))
			})
		})
	}
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
			req := httptest.NewRequest(http.MethodGet, "/v2/rss/?mt=Global", nil)
			req.Host = "status.example.com"
			if tc.proto != "" {
				req.Header.Set("X-Forwarded-Proto", tc.proto)
			}

			feed := buildFeed("Incidents", baseurl.Resolve(req, tc.configured), req,
				[]*db.Incident{baseIncident(nil)})

			require.NotNil(t, feed.Link)
			assert.Equal(t, tc.expectBase+"/v2/rss/?mt=Global", feed.Link.Href)

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
