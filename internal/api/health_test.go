package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/stackmon/otc-status-dashboard/internal/conf"
	"github.com/stackmon/otc-status-dashboard/internal/db"
)

const unreachableDBAddr = "127.0.0.1:1"

func newHealthTestAPI(t *testing.T) *API {
	t.Helper()

	database, err := db.New(&conf.Config{DB: "postgres://user:pass@" + unreachableDBAddr + "/test"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = database.Close() })

	r := gin.New()
	a := &API{r: r, db: database, log: zaptest.NewLogger(t)}
	a.initHealthRoutes()
	return a
}

func TestHealthz_ReturnsOKWithoutDatabase(t *testing.T) {
	a := newHealthTestAPI(t)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	a.r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestReadyz_ReturnsServiceUnavailableWhenDatabaseIsDown(t *testing.T) {
	a := newHealthTestAPI(t)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	a.r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestHealthRoutesAreRegisteredWithoutAuth(t *testing.T) {
	a := newHealthTestAPI(t)

	paths := map[string]bool{}
	for _, route := range a.r.Routes() {
		if route.Method == http.MethodGet {
			paths[route.Path] = true
		}
	}
	assert.True(t, paths["/healthz"], "/healthz must be a top-level GET route")
	assert.True(t, paths["/readyz"], "/readyz must be a top-level GET route")
}
