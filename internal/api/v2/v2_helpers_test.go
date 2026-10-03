package v2

import (
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/stackmon/otc-status-dashboard/internal/conf"
	"github.com/stackmon/otc-status-dashboard/internal/db"
)

// unreachableDSN lets tests build a *db.DB without opening a connection; the
// handler paths they exercise reject the request before any query runs.
const unreachableDSN = "postgres://postgres:postgres@127.0.0.1:1/postgres?sslmode=disable"

func getYearAndMonth(year, month, offset int) (int, int) {
	newMonth := month - offset
	for newMonth <= 0 {
		year--
		newMonth += 12
	}
	return year, newMonth
}

// initRouterWithStoredEvent returns a *gin.Engine with a single PATCH /v2/events/:eventID route
// that injects the given incident into the gin context (simulating CheckEventExistenceMW) so that
// PatchIncidentHandler can be exercised without a real database lookup.
func initRouterWithStoredEvent(t *testing.T, incident *db.Incident) *gin.Engine {
	t.Helper()

	d, err := db.New(&conf.Config{DB: unreachableDSN})
	require.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	gin.SetMode(gin.TestMode)
	r := gin.New()
	log, _ := zap.NewDevelopment()

	r.PATCH("/v2/events/:eventID", func(c *gin.Context) {
		c.Set("event", incident)
		c.Next()
	}, PatchIncidentHandler(d, log))

	return r
}
